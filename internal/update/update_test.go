package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func release(t *testing.T, payload []byte, modify func(*Manifest)) (*Client, *httptest.Server) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{1, Repository, "v1.2.3", runtime.GOOS, runtime.GOARCH, Artifact("v1.2.3", runtime.GOOS, runtime.GOARCH), digest(payload), int64(len(payload))}
	if modify != nil {
		modify(&m)
	}
	data, _ := json.Marshal(m)
	signature := ed25519.Sign(key, data)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case m.Artifact:
			w.Write(payload)
		case m.Artifact + ".json":
			w.Write(data)
		case m.Artifact + ".json.sig":
			w.Write(signature)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &Client{key: pub, base: server.URL + "/", http: server.Client()}, server
}
func physical(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestVerifiedUpdateRollback(t *testing.T) {
	client, _ := release(t, []byte("new executable"), nil)
	path := filepath.Join(physical(t), "aikit.exe")
	os.WriteFile(path, []byte("old executable"), 0755)
	if err := client.Apply(context.Background(), "v1.2.3", path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new executable" {
		t.Fatal("replacement missing")
	}
	if err := client.Apply(context.Background(), "v1.2.3", path); err == nil {
		t.Fatal("overwrote retained backup")
	}
	if err := Rollback(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(path)
	if string(got) != "old executable" {
		t.Fatal("rollback lost original")
	}
}
func TestReleaseTrustBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func(*Manifest)
	}{
		{"source", func(m *Manifest) { m.Repository = "other/repo" }}, {"version", func(m *Manifest) { m.Version = "v1.2.4" }}, {"platform", func(m *Manifest) { m.OS = "invalid" }}, {"checksum", func(m *Manifest) { m.SHA256 = digest([]byte("different")) }}, {"size", func(m *Manifest) { m.Size = MaxArtifactBytes + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := release(t, []byte("payload"), test.modify)
			path := filepath.Join(physical(t), "aikit")
			os.WriteFile(path, []byte("original"), 0755)
			if c.Apply(context.Background(), "v1.2.3", path) == nil {
				t.Fatal("accepted invalid release")
			}
			got, _ := os.ReadFile(path)
			if string(got) != "original" {
				t.Fatal("damaged executable")
			}
		})
	}
	c, _ := release(t, []byte("payload"), nil)
	c.key = make([]byte, ed25519.PublicKeySize)
	if _, err := c.Check(context.Background(), "v1.2.3"); err == nil {
		t.Fatal("accepted invalid signature")
	}
	if _, err := New(); err == nil {
		t.Fatal("missing production key did not fail closed")
	}
	if _, err := c.Check(context.Background(), "../../latest"); err == nil {
		t.Fatal("accepted invalid version")
	}
}
func TestCancelledAndTamperedRollback(t *testing.T) {
	c, _ := release(t, []byte("payload"), nil)
	path := filepath.Join(physical(t), "aikit")
	os.WriteFile(path, []byte("original"), 0755)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Apply(ctx, "v1.2.3", path) == nil {
		t.Fatal("ignored cancellation")
	}
	if err := c.Apply(context.Background(), "v1.2.3", path); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path+".previous", []byte("tampered"), 0755)
	if Rollback(context.Background(), path) == nil {
		t.Fatal("restored tampered backup")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "payload" {
		t.Fatal("rollback corrupted installation")
	}
}

func TestLateExecutableReplacementPreserved(t *testing.T) {
	c, _ := release(t, []byte("new"), nil)
	dir := physical(t)
	path := filepath.Join(dir, "aikit")
	os.WriteFile(path, []byte("old"), 0755)
	c.beforePromote = func() {
		replacement := filepath.Join(dir, "replacement")
		if err := os.WriteFile(replacement, []byte("late user installation"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Fatal(err)
		}
	}
	if c.Apply(context.Background(), "v1.2.3", path) == nil {
		t.Fatal("overwrote late installation")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "late user installation" {
		t.Fatal("late replacement lost")
	}
	if _, err := os.Stat(path + ".previous"); !os.IsNotExist(err) {
		t.Fatal("unexpected backup")
	}
}

func TestPromotionFailureAndLateCancellationPreserveCurrent(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		c, _ := release(t, []byte("replacement"), nil)
		path := filepath.Join(physical(t), "aikit")
		os.WriteFile(path, []byte("original"), 0755)
		ctx, cancel := context.WithCancel(context.Background())
		c.beforePromote = func() {
			if cancelled {
				cancel()
			} else {
				os.Remove(path + ".staged")
			}
		}
		if c.Apply(ctx, "v1.2.3", path) == nil {
			t.Fatal("promotion unexpectedly succeeded")
		}
		cancel()
		got, _ := os.ReadFile(path)
		if string(got) != "original" {
			t.Fatal("failure lost current executable")
		}
		if _, err := os.Stat(path + ".previous"); !os.IsNotExist(err) {
			t.Fatal("successful restoration left backup")
		}
	}
}

// This executes the updater while the target image is running, including on
// Windows. The copied test binary is the original image; no production key exists.
func TestRunningExecutableReplacement(t *testing.T) {
	if os.Getenv("AIKIT_UPDATE_TEST_CHILD") == "1" {
		t.Skip("parent handles child entry")
	}
	original, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	dir := physical(t)
	path := filepath.Join(dir, "running-test.exe")
	if err = os.WriteFile(path, original, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-test.run=TestUpdateChild$")
	cmd.Env = append(os.Environ(), "AIKIT_UPDATE_TEST_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running executable replacement: %v %s", err, output)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "replacement image" {
		t.Fatal("running image was not replaced")
	}
	backup, _ := os.ReadFile(path + ".previous")
	if digest(backup) != digest(original) {
		t.Fatal("running original was not preserved")
	}
}
func TestUpdateChild(t *testing.T) {
	if os.Getenv("AIKIT_UPDATE_TEST_CHILD") != "1" {
		t.Skip("subprocess entry")
	}
	c, _ := release(t, []byte("replacement image"), nil)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Apply(context.Background(), "v1.2.3", path); err != nil {
		t.Fatal(err)
	}
}

func TestRunningExecutableRollback(t *testing.T) {
	original, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(physical(t), "rollback-test.exe")
	old := append(append([]byte{}, original...), []byte("original marker")...)
	next := append(append([]byte{}, original...), []byte("replacement marker")...)
	if err = os.WriteFile(path, old, 0755); err != nil {
		t.Fatal(err)
	}
	c, _ := release(t, next, nil)
	if err = c.Apply(context.Background(), "v1.2.3", path); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-test.run=TestRollbackChild$")
	cmd.Env = append(os.Environ(), "AIKIT_ROLLBACK_TEST_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running rollback: %v %s", err, output)
	}
	got, _ := os.ReadFile(path)
	if digest(got) != digest(old) {
		t.Fatal("running rollback lost original")
	}
}
func TestRollbackChild(t *testing.T) {
	if os.Getenv("AIKIT_ROLLBACK_TEST_CHILD") != "1" {
		t.Skip("subprocess entry")
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err = Rollback(context.Background(), path); err != nil && !(runtime.GOOS == "windows" && err.Error() == "UPDATE_CLEANUP: rollback restored executable; remove retained staged image after exit") {
		t.Fatal(err)
	}
}

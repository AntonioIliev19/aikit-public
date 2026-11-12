package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPrivateStateGitExclusionAndIgnorePreservation(t *testing.T) {
	project := physicalTemp(t)
	if err := os.Mkdir(filepath.Join(project, ".aikit"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := OpenRoot(project)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := EnsurePrivateState(root); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateState(root); err != nil {
		t.Fatal("not idempotent", err)
	}
	for _, name := range []string{".aikit/state/index.sqlite", ".aikit/state/harness-backup.json", ".aikit/state/harness-transaction/before.json"} {
		if !HardDenied(name, false) {
			t.Fatal("private metadata not hard denied")
		}
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable for ignore integration")
	}
	cmd := exec.Command("git", "init", "--quiet")
	cmd.Dir = project
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, data)
	}
	for _, name := range []string{".aikit/state/index.sqlite", ".aikit/state/harness-backup.json", ".aikit/state/harness-transaction/before.json"} {
		cmd := exec.Command("git", "check-ignore", "--no-index", name)
		cmd.Dir = project
		if err := cmd.Run(); err != nil {
			t.Fatalf("private artifact not Git ignored: %s", name)
		}
	}
	ignore := filepath.Join(project, ".aikit/state/.gitignore")
	if err := os.WriteFile(ignore, []byte("user-policy\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateState(root); err == nil {
		t.Fatal("unowned ignore adopted")
	}
	if data, err := os.ReadFile(ignore); err != nil || string(data) != "user-policy\n" {
		t.Fatal("user ignore overwritten")
	}
}

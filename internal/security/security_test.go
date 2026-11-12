package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func physicalTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPortablePathValidation(t *testing.T) {
	for _, name := range []string{"../x", "/absolute", "x/../y", `x\y`, "x:stream", "NUL.txt", "dir/COM1", "trailing.", "a//b", "a/", "a/\x01b"} {
		if ValidateRelative(name) == nil {
			t.Errorf("unsafe path accepted: %q", name)
		}
	}
	for _, name := range []string{"package-id", "src/code.ts", "LICENSE.md"} {
		if err := ValidateRelative(name); err != nil {
			t.Errorf("safe path rejected: %q", name)
		}
	}
	for _, name := range []string{"a/b", "..", "-option", "NUL", "x:ads"} {
		if ValidateID(name) == nil {
			t.Errorf("unsafe ID accepted: %q", name)
		}
	}
}

func TestHardDenyAndSensitiveContent(t *testing.T) {
	for _, name := range []string{".env.example", "a/ID_ECDSA", "id_dsa", "a/key.PEM", "node_modules/code.ts", ".aikit/state/index.sqlite", "creds/service-account.json", "api_key=longSyntheticValue123456.ts"} {
		if !HardDenied(name, false) {
			t.Errorf("hard deny missed: %q", name)
		}
	}
	for _, data := range []string{`{"client_secret":"longSyntheticValue123456"}`, `{"password":"longSyntheticValue123456"}`, "api_key=longSyntheticValue123456", "-----BEGIN PRIVATE KEY-----"} {
		if !SensitiveContent([]byte(data)) {
			t.Fatal("supported synthetic credential form missed")
		}
	}
	if SensitiveContent([]byte("export const message = 'hello';")) {
		t.Fatal("safe sample denied")
	}
}

func policyFixture(t *testing.T, files map[string]string) *Policy {
	t.Helper()
	dir := physicalTemp(t)
	for name, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(full), 0700)
		if err := os.WriteFile(full, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p := &Policy{}
	if err := p.LoadDirectory(r, "."); err != nil {
		t.Fatal(err)
	}
	if _, ok := files["nested/.gitignore"]; ok {
		if err := p.LoadDirectory(r, "nested"); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestIgnoreFamiliesCannotUndoEachOther(t *testing.T) {
	for _, files := range []map[string]string{
		{".gitignore": "private.md\n", ".aikitignore": "!private.md\n"},
		{".gitignore": "!private.md\n", ".aikitignore": "private.md\n"},
	} {
		if !policyFixture(t, files).Denied("private.md", false) {
			t.Fatal("independent ignore family was negated")
		}
	}
	p := policyFixture(t, map[string]string{".gitignore": "*.md\n!public.md\n!.env\n", ".aikitignore": "!.env\n"})
	if p.Denied("public.md", false) || !p.Denied("private.md", false) || !p.Denied(".env", false) {
		t.Fatal("negation or hard deny precedence incorrect")
	}
}

func TestIgnorePatternsAgainstGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git unavailable")
	}
	dir := physicalTemp(t)
	if err := exec.Command("git", "init", "--quiet", dir).Run(); err != nil {
		t.Fatal(err)
	}
	patterns := "/root-only.txt\ncache/\n*.tmp\n!keep.tmp\nspace\\ name.txt\n\\!literal.txt\n**/generated?.ts\nnested/*.md\n"
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(patterns), 0600)
	os.MkdirAll(filepath.Join(dir, "nested"), 0700)
	os.WriteFile(filepath.Join(dir, "nested", ".gitignore"), []byte("!keep.md\n"), 0600)
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p := &Policy{}
	if err := p.LoadDirectory(r, "."); err != nil {
		t.Fatal(err)
	}
	if err := p.LoadDirectory(r, "nested"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root-only.txt", "nested/root-only.txt", "cache/a.txt", "other.tmp", "keep.tmp", "space name.txt", "!literal.txt", "generated1.ts", "nested/generated2.ts", "nested/drop.md", "nested/keep.md"} {
		cmd := exec.Command("git", "-C", dir, "check-ignore", "--no-index", "--quiet", "--", name)
		err := cmd.Run()
		gitDenied := err != nil
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			gitDenied = false
		} else if err == nil {
			gitDenied = true
		} else {
			t.Fatal("git ignore comparison failed")
		}
		if got := p.Denied(name, false); got != gitDenied {
			t.Errorf("policy differs from Git for %q: got %t want %t", name, got, gitDenied)
		}
	}
}

func TestBoundedReadsAndLinks(t *testing.T) {
	dir := physicalTemp(t)
	os.WriteFile(filepath.Join(dir, "large.txt"), []byte(strings.Repeat("x", 129)), 0600)
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := ReadFileBounded(r, "large.txt", 128); err == nil {
		t.Fatal("oversize read allowed")
	}
	if _, err := ReadDirBounded(r, ".", 0); err == nil {
		t.Fatal("oversize directory allowed")
	}
	if err := os.Symlink(filepath.Join(dir, "large.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if _, err := ReadFileBounded(r, "link.txt", 256); err == nil {
		t.Fatal("source symlink accepted")
	}
}

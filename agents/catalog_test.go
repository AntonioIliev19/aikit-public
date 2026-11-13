package agents

import (
	"io/fs"
	"os"
	"testing"
)

// Every package directory in the checkout must be embedded, so the menu and
// `--available` never silently miss a predefined package.
func TestEveryPackageDirectoryIsEmbedded(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(e.Name() + "/conmesh-package.json"); err != nil {
			continue
		}
		found++
		if _, err := fs.Stat(FS, e.Name()+"/conmesh-package.json"); err != nil {
			t.Errorf("package directory %q is not in the go:embed list", e.Name())
		}
	}
	if found == 0 {
		t.Fatal("no package directories found")
	}
}

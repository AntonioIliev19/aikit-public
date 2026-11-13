package config

import (
	"errors"
	"os"
	"path/filepath"
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

func TestInitPreservesAndFindsConfig(t *testing.T) {
	dir := physicalTemp(t)
	c, created, err := Init(dir, Default())
	if err != nil || !created || c.Indexing.Enabled || c.Indexing.Semantic {
		t.Fatalf("init failed or unexpectedly opted in: %v", err)
	}
	requested := Default()
	requested.Indexing.Enabled = true
	if c, created, err = Init(dir, requested); err != nil || created || c.Indexing.Enabled {
		t.Fatalf("repeat init overwrote config: %v", err)
	}
	nested := filepath.Join(dir, "src", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	root, _, err := Find(nested)
	if err != nil || root != dir {
		t.Fatal("project discovery failed")
	}
}

func TestStrictConfig(t *testing.T) {
	for _, data := range []string{`{"schema_version":99}`, `{"schema_version":1,"indexing":{"profile":"balanced"},"unknown":true}`, `{"schema_version":1,"indexing":{"profile":"balanced","semantic":true}}`, `{} {}`} {
		dir := physicalTemp(t)
		os.Mkdir(filepath.Join(dir, ".conmesh"), 0700)
		name := filepath.Join(dir, ".conmesh", File)
		os.WriteFile(name, []byte(data), 0600)
		if _, err := Load(dir); err == nil {
			t.Fatal("invalid config accepted")
		}
		if _, _, err := Init(dir, Default()); err == nil {
			t.Fatal("malformed existing config overwritten")
		}
		actual, _ := os.ReadFile(name)
		if string(actual) != data {
			t.Fatal("existing config changed")
		}
	}
	if _, err := Load(physicalTemp(t)); !errors.Is(err, ErrNoProject) {
		t.Fatal("missing project not distinguished")
	}
}

func TestInitRejectsLink(t *testing.T) {
	dir, target := physicalTemp(t), physicalTemp(t)
	if err := os.Symlink(target, filepath.Join(dir, ".conmesh")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if _, _, err := Init(dir, Default()); err == nil {
		t.Fatal("symlinked config accepted")
	}
}

func TestLegacyProjectRequiresMigration(t *testing.T) {
	dir := physicalTemp(t)
	if err := os.Mkdir(filepath.Join(dir, ".aikit"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Init(dir, Default()); !errors.Is(err, errLegacyProject) {
		t.Fatalf("Init returned %v", err)
	}
	if _, _, err := Find(dir); !errors.Is(err, errLegacyProject) {
		t.Fatalf("Find returned %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".conmesh")); !os.IsNotExist(err) {
		t.Fatalf("Init created new state: %v", err)
	}
}

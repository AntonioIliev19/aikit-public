package packages

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExportEffectiveBoundedSnapshotAndIntegrity(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	for _, scope := range []string{"global", "project"} {
		src := sourceFixture(t, "review", "1")
		if _, err := m.Add(ctx, "skill", src, "", scope, true); err != nil {
			t.Fatal(err)
		}
	}
	items, err := m.Export(ctx, "skill")
	if err != nil || len(items) != 1 || items[0].Entry.Scope != "project" {
		t.Fatalf("precedence: %v %v", items, err)
	}
	override := filepath.Join(m.Project, ".aikit", "overrides", "skills", "review")
	writeFixture(t, override, "review", "skill", "2", "Reviewed override instructions.")
	fixtures := map[string]string{".env": "innocent text", "key.pem": "innocent text", "ignored.txt": "omit", "nested/.aikitignore": "private.txt\n", "nested/private.txt": "omit", "nested/keep.txt": "keep", "api_key=longSyntheticValue123456.txt": "omit", "content.txt": "password=longSyntheticValue123456", ".gitignore": "ignored.txt\n"}
	for name, body := range fixtures {
		full := filepath.Join(override, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	items, err = m.Export(ctx, "skill")
	if err != nil || len(items) != 1 || items[0].Entry.Scope != "override" {
		t.Fatal("override snapshot unavailable", err)
	}
	for _, name := range []string{".env", "key.pem", "ignored.txt", "nested/private.txt", "api_key=longSyntheticValue123456.txt", "content.txt"} {
		if _, ok := items[0].Files[name]; ok {
			t.Fatalf("excluded path exported: %s", name)
		}
	}
	if string(items[0].Files["nested/keep.txt"]) != "keep" {
		t.Fatal("authorized support missing")
	}
	if err := os.WriteFile(filepath.Join(override, "SKILL.md"), []byte("changed later"), 0600); err != nil {
		t.Fatal(err)
	}
	if string(items[0].Files["SKILL.md"]) != "Reviewed override instructions." {
		t.Fatal("snapshot aliases source")
	}
	if err := os.WriteFile(filepath.Join(installed(m, "review"), "SKILL.md"), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Export(ctx, "skill"); err == nil {
		t.Fatal("tampered managed store exported")
	}
}

func TestExportDoesNotCreateAbsentGlobalAndHonorsLock(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	if _, err := m.Export(ctx, "skill"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Global); !os.IsNotExist(err) {
		t.Fatal("export created global store")
	}
	if err := os.WriteFile(filepath.Join(m.Project, ".aikit", ".packages-mutation.lock"), []byte("busy"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Export(ctx, "skill"); err == nil {
		t.Fatal("export ignored package transaction lock")
	}
}

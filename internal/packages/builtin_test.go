package packages

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestCatalogListsEmbeddedPackages(t *testing.T) {
	all, err := Catalog("agent")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]CatalogEntry{}
	for _, e := range all {
		ids[e.ID] = e
	}
	for _, id := range []string{"angular-agent", "dotnet-agent", "repository-research"} {
		e, ok := ids[id]
		if !ok || e.Source != BuiltinPrefix+id || e.Type != "agent" || e.Title == "" || e.Description == "" {
			t.Fatalf("catalog entry %s = %+v", id, e)
		}
	}
	if skills, err := Catalog("skill"); err != nil || len(skills) != 0 {
		t.Fatal("unexpected predefined skills", skills, err)
	}
	if _, err := Catalog("bogus"); err == nil {
		t.Fatal("invalid kind accepted")
	}
}

func TestBuiltinInstallUpdateAndLock(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	r, err := m.Add(ctx, "agent", BuiltinPrefix+"repository-research", "", "project", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Entry.Source != BuiltinPrefix+"repository-research" || r.Entry.Ref != "" || r.Entry.ID != "repository-research" {
		t.Fatalf("entry %+v", r.Entry)
	}
	if _, err := os.Stat(filepath.Join(m.Project, ".conmesh", "packages", "agents", "repository-research", "AGENT.md")); err != nil {
		t.Fatal("instructions not installed", err)
	}
	// The lock must reload, and adding again is a no-op.
	again, err := m.Add(ctx, "agent", BuiltinPrefix+"repository-research", "", "project", true)
	if err != nil || !again.Unchanged {
		t.Fatal("second add", again, err)
	}
	items, err := m.List(ctx, "agent", "")
	if err != nil || len(items) != 1 || items[0].Source != BuiltinPrefix+"repository-research" {
		t.Fatal("list", items, err)
	}
	// Update reads the copy in the running binary.
	saved := builtinFS
	t.Cleanup(func() { builtinFS = saved })
	builtinFS = fstest.MapFS{
		"repository-research/conmesh-package.json": {Data: []byte(`{"schema_version":1,"id":"repository-research","type":"agent","version":"1.1.0","license":"Apache-2.0"}`)},
		"repository-research/LICENSE":              {Data: []byte("license text")},
		"repository-research/AGENT.md":             {Data: []byte("newer instructions")},
	}
	updated, err := m.Update(ctx, "agent", "repository-research", "", "project", true)
	if err != nil || updated.Entry.Version != "1.1.0" || updated.Entry.Source != BuiltinPrefix+"repository-research" {
		t.Fatal("update", updated, err)
	}
}

func TestBuiltinRejectsUnknownAndMismatched(t *testing.T) {
	m := newManager(t)
	ctx := context.Background()
	for _, source := range []string{BuiltinPrefix + "missing", BuiltinPrefix + "../agents", BuiltinPrefix} {
		if _, err := m.Add(ctx, "agent", source, "", "project", true); err == nil {
			t.Fatalf("%q accepted", source)
		}
	}
	if _, err := m.Add(ctx, "skill", BuiltinPrefix+"dotnet-agent", "", "project", true); err == nil {
		t.Fatal("agent package installed as a skill")
	}
	if _, err := m.Add(ctx, "agent", BuiltinPrefix+"dotnet-agent", "", "project", false); err == nil {
		t.Fatal("predefined package installed without --trust")
	}
	saved := builtinFS
	t.Cleanup(func() { builtinFS = saved })
	builtinFS = fstest.MapFS{
		"renamed/conmesh-package.json": {Data: []byte(`{"schema_version":1,"id":"other","type":"agent","version":"1.0.0","license":"MIT"}`)},
		"renamed/LICENSE":              {Data: []byte("license text")},
	}
	if _, err := m.Add(ctx, "agent", BuiltinPrefix+"renamed", "", "project", true); err == nil {
		t.Fatal("identity mismatch accepted")
	}
}

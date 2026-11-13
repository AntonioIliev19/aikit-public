package packages

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/config"
)

func physicalTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func newManager(t *testing.T) *Manager {
	t.Helper()
	project := physicalTemp(t)
	if _, _, err := config.Init(project, config.Default()); err != nil {
		t.Fatal(err)
	}
	return New(project, filepath.Join(physicalTemp(t), "global"))
}

func writeFixture(t *testing.T, dir, id, kind, version, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(PackageManifest{SchemaVersion: 1, ID: id, Type: kind, Version: version, License: "Test-License"})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{SourceManifest: data, "LICENSE": []byte("Synthetic test content, authored for this test."), "SKILL.md": []byte(body)} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func sourceFixture(t *testing.T, id, version string) string {
	t.Helper()
	dir := physicalTemp(t)
	writeFixture(t, dir, id, "skill", version, "Follow the project tests.")
	return dir
}
func installed(m *Manager, id string) string {
	return filepath.Join(m.Project, ".conmesh", "packages", "skills", id)
}
func stateBytes(t *testing.T, m *Manager) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.Project, ".conmesh", "packages", "lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLifecycleScopesAndOverrides(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	src := sourceFixture(t, "sample", "1.0.0")
	if _, err := m.Add(ctx, "skill", src, "", "project", false); err == nil {
		t.Fatal("unreviewed package installed")
	}
	first, err := m.Add(ctx, "skill", src, "", "project", true)
	if err != nil {
		t.Fatal(err)
	}
	if first.Unchanged || len(first.Entry.SHA256) != 64 || first.Entry.Source != src {
		t.Fatal("lock provenance missing")
	}
	second, err := m.Add(ctx, "skill", src, "", "project", true)
	if err != nil || !second.Unchanged {
		t.Fatalf("repeated add not idempotent: %v", err)
	}
	if _, err := m.Add(ctx, "skill", src, "", "global", true); err != nil {
		t.Fatal(err)
	}
	items, err := m.List(ctx, "skill", "")
	if err != nil || len(items) != 2 {
		t.Fatalf("list: %v", err)
	}
	if !items[0].Effective || items[0].Scope != "project" || items[1].ShadowedBy != "project" {
		t.Fatal("project/global precedence incorrect")
	}
	override := filepath.Join(m.Project, ".conmesh", "overrides", "skills", "sample")
	writeFixture(t, override, "sample", "skill", "local", "User-owned override.")
	items, err = m.List(ctx, "skill", "")
	if err != nil || len(items) != 3 || items[0].Scope != "override" || !items[0].Effective || items[1].ShadowedBy != "override" || items[2].ShadowedBy != "override" {
		t.Fatalf("override precedence failed: %v", err)
	}
	if removed, err := m.Remove(ctx, "skill", "sample", "project"); err != nil || !removed {
		t.Fatalf("remove failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(override, "SKILL.md")); err != nil {
		t.Fatal("remove touched user override")
	}
	if removed, err := m.Remove(ctx, "skill", "sample", "project"); err != nil || removed {
		t.Fatal("repeated remove not idempotent")
	}
	if err := os.RemoveAll(override); err != nil {
		t.Fatal(err)
	}
	items, err = m.List(ctx, "skill", "")
	if err != nil || len(items) != 1 || items[0].Scope != "global" || !items[0].Effective {
		t.Fatal("global package not revealed after removing project entries")
	}
	if removed, err := m.Remove(ctx, "skill", "sample", "global"); err != nil || !removed {
		t.Fatal("global remove failed")
	}
}

func TestExplicitUpdateAndCollision(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	src := sourceFixture(t, "sample", "1")
	first, err := m.Add(ctx, "skill", src, "", "project", true)
	if err != nil {
		t.Fatal(err)
	}
	other := sourceFixture(t, "sample", "1")
	if _, err := m.Add(ctx, "skill", other, "", "project", true); err == nil {
		t.Fatal("different source ID collision accepted")
	}
	writeFixture(t, src, "sample", "skill", "2", "Updated instructions.")
	if _, err := m.Add(ctx, "skill", src, "", "project", true); err == nil {
		t.Fatal("add silently updated existing package")
	}
	updated, err := m.Update(ctx, "skill", "sample", "2", "project", true)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Entry.Version != "2" || updated.Entry.SHA256 == first.Entry.SHA256 {
		t.Fatal("update not persisted")
	}
	if _, err := m.Update(ctx, "skill", "sample", "3", "project", true); err == nil {
		t.Fatal("local version mismatch accepted")
	}
	if _, err := m.Update(ctx, "skill", "absent", "", "project", true); err == nil {
		t.Fatal("missing package updated")
	}
}

func TestOwnedContentPreserved(t *testing.T) {
	ctx := context.Background()
	for _, action := range []string{"remove", "update", "add"} {
		t.Run(action, func(t *testing.T) {
			m := newManager(t)
			src := sourceFixture(t, "sample", "1")
			if _, err := m.Add(ctx, "skill", src, "", "project", true); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(installed(m, "sample"), "SKILL.md")
			os.WriteFile(file, []byte("User customization."), 0600)
			var err error
			switch action {
			case "remove":
				_, err = m.Remove(ctx, "skill", "sample", "project")
			case "update":
				_, err = m.Update(ctx, "skill", "sample", "", "project", true)
			case "add":
				_, err = m.Add(ctx, "skill", src, "", "project", true)
			}
			if err == nil {
				t.Fatal("modified package overwritten")
			}
			data, _ := os.ReadFile(file)
			if string(data) != "User customization." {
				t.Fatal("user modification lost")
			}
		})
	}
	m := newManager(t)
	store := filepath.Join(m.Project, ".conmesh", "packages")
	os.MkdirAll(store, 0700)
	os.WriteFile(filepath.Join(store, "handwritten.txt"), []byte("User-owned."), 0600)
	if _, err := m.Add(ctx, "skill", sourceFixture(t, "sample", "1"), "", "project", true); err == nil {
		t.Fatal("unowned store replaced")
	}
}

func TestTransactionRollbackAndLateEdit(t *testing.T) {
	for _, failure := range []string{"staging", "promotion", "late-edit"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			m := newManager(t)
			src := sourceFixture(t, "sample", "1")
			if _, err := m.Add(ctx, "skill", src, "", "project", true); err != nil {
				t.Fatal(err)
			}
			before := string(stateBytes(t, m))
			writeFixture(t, src, "sample", "skill", "2", "New content.")
			switch failure {
			case "staging":
				m.beforeSwap = func(string) error { return errors.New("injected staging failure") }
			case "promotion":
				m.rename = func(root *os.Root, a, b string) error {
					if b == "packages" {
						return errors.New("injected promotion failure")
					}
					return root.Rename(a, b)
				}
			case "late-edit":
				m.beforeSwap = func(string) error {
					return os.WriteFile(filepath.Join(installed(m, "sample"), "SKILL.md"), []byte("Late user edit."), 0600)
				}
			}
			if _, err := m.Update(ctx, "skill", "sample", "2", "project", true); err == nil {
				t.Fatal("injected failure accepted")
			}
			if string(stateBytes(t, m)) != before {
				t.Fatal("failed transaction changed lock")
			}
			data, err := os.ReadFile(filepath.Join(installed(m, "sample"), "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			want := "Follow the project tests."
			if failure == "late-edit" {
				want = "Late user edit."
			}
			if string(data) != want {
				t.Fatal("failed transaction lost prior/customized content")
			}
		})
	}
}

func TestMalformedLockAndUnownedDirectory(t *testing.T) {
	for _, failure := range []string{"schema", "path", "empty-dir"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			m := newManager(t)
			src := sourceFixture(t, "sample", "1")
			if _, err := m.Add(ctx, "skill", src, "", "project", true); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(m.Project, ".conmesh", "packages", "lock.json")
			data := stateBytes(t, m)
			switch failure {
			case "schema":
				data = []byte(`{"schema_version":99,"scope":"project","packages":[]}`)
			case "path":
				data = []byte(strings.Replace(string(data), `"id": "sample"`, `"id": "../escape"`, 1))
			case "empty-dir":
				os.Mkdir(filepath.Join(installed(m, "sample"), "user-empty"), 0700)
			}
			if failure != "empty-dir" {
				os.WriteFile(lockPath, data, 0600)
			}
			if _, err := m.Remove(ctx, "skill", "sample", "project"); err == nil {
				t.Fatal("invalid/unowned state mutated")
			}
			if _, err := os.Stat(filepath.Join(installed(m, "sample"), "SKILL.md")); err != nil {
				t.Fatal("invalid state destroyed package")
			}
		})
	}
}

func TestSafeSourceFilteringAndLimits(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	src := sourceFixture(t, "sample", "1")
	for name, data := range map[string]string{".env.example": "fixture", "private.md": "ignored", ".gitignore": "private.md\n", ".conmeshignore": "!private.md\n", "credentials.json": `{"password":"longSyntheticValue123456"}`} {
		os.WriteFile(filepath.Join(src, name), []byte(data), 0600)
	}
	result, err := m.Add(ctx, "skill", src, "", "project", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Omitted < 3 {
		t.Fatal("unsafe/ignored inputs not counted")
	}
	for _, name := range []string{".env.example", "private.md", "credentials.json"} {
		if _, err := os.Stat(filepath.Join(installed(m, "sample"), name)); !os.IsNotExist(err) {
			t.Fatal("excluded file persisted")
		}
	}
	archive := filepath.Join(physicalTemp(t), "package.zip")
	os.WriteFile(archive, []byte("not extracted"), 0600)
	if _, err := m.Add(ctx, "skill", archive, "", "project", true); err == nil {
		t.Fatal("archive accepted")
	}
	large := sourceFixture(t, "large", "1")
	os.WriteFile(filepath.Join(large, "large.txt"), make([]byte, MaxFileBytes+1), 0600)
	if _, err := m.Add(ctx, "skill", large, "", "project", true); err == nil {
		t.Fatal("oversize input accepted")
	}
	deep := sourceFixture(t, "deep", "1")
	os.MkdirAll(filepath.Join(deep, strings.Repeat("a/", MaxDepth+2)), 0700)
	if _, err := m.Add(ctx, "skill", deep, "", "project", true); err == nil {
		t.Fatal("unbounded depth accepted")
	}
}

func TestSourceAndDestinationLinks(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	src := sourceFixture(t, "sample", "1")
	if err := os.Symlink(filepath.Join(src, "SKILL.md"), filepath.Join(src, "link.md")); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if _, err := m.Add(ctx, "skill", src, "", "project", true); err == nil {
		t.Fatal("source symlink accepted")
	}
	os.Remove(filepath.Join(src, "link.md"))
	outside := physicalTemp(t)
	os.Symlink(outside, filepath.Join(m.Project, ".conmesh", "packages"))
	if _, err := m.Add(ctx, "skill", src, "", "project", true); err == nil {
		t.Fatal("destination symlink accepted")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("destination escaped project")
	}
}

func TestConcurrentMutationFailsWithoutLosingState(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	first := sourceFixture(t, "first", "1")
	second := sourceFixture(t, "second", "1")
	entered, release := make(chan struct{}), make(chan struct{})
	m.beforeSwap = func(string) error { close(entered); <-release; return nil }
	done := make(chan error, 1)
	go func() { _, err := m.Add(ctx, "skill", first, "", "project", true); done <- err }()
	<-entered
	other := New(m.Project, m.Global)
	if _, err := other.Add(ctx, "skill", second, "", "project", true); err == nil {
		t.Fatal("concurrent writer bypassed mutation lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := other.Add(ctx, "skill", second, "", "project", true); err != nil {
		t.Fatal(err)
	}
	items, err := other.List(ctx, "skill", "")
	if err != nil || len(items) != 2 {
		t.Fatal("retry lost a registry entry")
	}
}

func TestScopedListsIgnoreUnrelatedCorruptStore(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	src := sourceFixture(t, "sample", "1")
	if _, err := m.Add(ctx, "skill", src, "", "project", true); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(m.Global, "packages"), 0700)
	os.WriteFile(filepath.Join(m.Global, "packages", "lock.json"), []byte("corrupt"), 0600)
	if items, err := m.List(ctx, "skill", "project"); err != nil || len(items) != 1 {
		t.Fatal("unrelated global corruption blocked project list")
	}
}

func TestGitSourceRefAndObjects(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	manifestData, _ := json.Marshal(PackageManifest{SchemaVersion: 1, ID: "remote", Type: "skill", Version: "1.0.0", License: "Test-License"})
	objects := map[string][]byte{strings.Repeat("1", 40): manifestData, strings.Repeat("2", 40): []byte("Test-authored license fixture."), strings.Repeat("3", 40): []byte("Test instructions.")}
	tree := fmt.Sprintf("100644 blob %s\t%s\x00100644 blob %s\tLICENSE\x00100644 blob %s\tSKILL.md\x00", strings.Repeat("1", 40), SourceManifest, strings.Repeat("2", 40), strings.Repeat("3", 40))
	fetches := 0
	m.git = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch args[0] {
		case "init":
			return nil, nil
		case "fetch":
			fetches++
			if args[len(args)-2] != "https://example.invalid/repo.git" || args[len(args)-1] != "v1" {
				t.Fatal("unexpected fetch identity")
			}
			return nil, nil
		case "rev-parse":
			return []byte(strings.Repeat("a", 40) + "\n"), nil
		case "ls-tree":
			return []byte(tree), nil
		case "cat-file":
			data := objects[args[2]]
			if args[1] == "-s" {
				return []byte(fmt.Sprint(len(data))), nil
			}
			return data, nil
		default:
			t.Fatal("unexpected Git operation")
			return nil, nil
		}
	}
	for _, bad := range []struct{ source, ref string }{{"https://user:password@example.invalid/repo.git", "v1"}, {"http://example.invalid/repo.git", "v1"}, {"https://example.invalid/repo.git", ""}, {"https://example.invalid/repo.git", "--upload-pack=bad"}} {
		if _, err := m.Add(ctx, "skill", bad.source, bad.ref, "project", true); err == nil {
			t.Fatal("unsafe Git source/ref accepted")
		}
	}
	if fetches != 0 {
		t.Fatal("unsafe source reached Git")
	}
	result, err := m.Add(ctx, "skill", "https://example.invalid/repo.git", "v1", "project", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Entry.Commit != strings.Repeat("a", 40) || result.Entry.Ref != "v1" {
		t.Fatal("Git commit/ref not locked")
	}
	before := string(stateBytes(t, m))
	m.git = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("offline fixture") }
	if _, err := m.Update(ctx, "skill", "remote", "v2", "project", true); err == nil {
		t.Fatal("offline failure accepted")
	}
	if string(stateBytes(t, m)) != before {
		t.Fatal("offline failure changed installed state")
	}
}

func TestStoreBudgetRejectsNextStateAndPreservesOld(t *testing.T) {
	ctx := context.Background()
	m := newManager(t)
	first := sourceFixture(t, "first", "1")
	second := sourceFixture(t, "second", "1")
	if _, err := m.Add(ctx, "skill", first, "", "project", true); err != nil {
		t.Fatal(err)
	}
	files, err := loadLocal(ctx, first, "skill", "")
	if err != nil {
		t.Fatal(err)
	}
	bytes := 0
	for _, data := range files.files {
		bytes += len(data)
	}
	m.storeLimit = bytes + 1
	before := string(stateBytes(t, m))
	if _, err := m.Add(ctx, "skill", second, "", "project", true); err == nil {
		t.Fatal("next state exceeded store budget")
	}
	if string(stateBytes(t, m)) != before {
		t.Fatal("budget rejection changed lock")
	}
	items, err := m.List(ctx, "skill", "")
	if err != nil || len(items) != 1 || items[0].ID != "first" {
		t.Fatal("budget rejection damaged prior package")
	}
}

func TestLocalPortableCaseCollision(t *testing.T) {
	src := sourceFixture(t, "sample", "1")
	os.WriteFile(filepath.Join(src, "Case.md"), []byte("a"), 0600)
	os.WriteFile(filepath.Join(src, "case.md"), []byte("b"), 0600)
	upper, _ := os.Stat(filepath.Join(src, "Case.md"))
	lower, _ := os.Stat(filepath.Join(src, "case.md"))
	if os.SameFile(upper, lower) {
		t.Skip("case-insensitive filesystem; Git collision covered separately")
	}
	if _, err := newManager(t).Add(context.Background(), "skill", src, "", "project", true); err == nil {
		t.Fatal("case-folded local collision accepted")
	}
}

func TestGitRejectsUnsafeObjectsBeforeMaterializing(t *testing.T) {
	for _, tree := range []string{
		"120000 blob " + strings.Repeat("1", 40) + "\tlink\x00",
		"160000 commit " + strings.Repeat("1", 40) + "\tsubmodule\x00",
		"100644 blob " + strings.Repeat("1", 40) + "\t.gitmodules\x00",
		"100644 blob " + strings.Repeat("1", 40) + "\t../escape\x00",
		"100644 blob " + strings.Repeat("1", 40) + "\tA/one\x00100644 blob " + strings.Repeat("1", 40) + "\ta/two\x00",
	} {
		git := func(_ context.Context, _ string, args ...string) ([]byte, error) {
			switch args[0] {
			case "init", "fetch":
				return nil, nil
			case "rev-parse":
				return []byte(strings.Repeat("a", 40)), nil
			case "ls-tree":
				return []byte(tree), nil
			case "cat-file":
				if args[1] == "-s" {
					return []byte("1"), nil
				}
				return []byte("x"), nil
			}
			t.Fatal("unexpected Git command")
			return nil, nil
		}
		if _, err := loadGit(context.Background(), "https://example.invalid/repo.git", "skill", "v1", git); err == nil {
			t.Fatal("unsafe Git tree accepted")
		}
	}
}

func TestDirectoryEntryBudget(t *testing.T) {
	src := sourceFixture(t, "sample", "1")
	for i := 0; i < MaxEntries; i++ {
		if err := os.Mkdir(filepath.Join(src, fmt.Sprintf("empty-%04d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := newManager(t).Add(context.Background(), "skill", src, "", "project", true); err == nil {
		t.Fatal("directory entry budget not enforced")
	}
}

// A program that briefly holds a file in the store (a virus scanner) must not
// fail the update; one that keeps holding it gets the reason and keeps the state.
func TestStoreRenameRetriesAndExplainsLockedFiles(t *testing.T) {
	saved := renameDelays
	renameDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { renameDelays = saved })
	ctx := context.Background()
	m := newManager(t)
	src := sourceFixture(t, "sample", "1")
	if _, err := m.Add(ctx, "skill", src, "", "project", true); err != nil {
		t.Fatal(err)
	}
	busy := 2
	m.rename = func(root *os.Root, a, b string) error {
		if a == "packages" && busy > 0 {
			busy--
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("Access is denied.")}
		}
		return root.Rename(a, b)
	}
	writeFixture(t, src, "sample", "skill", "2", "New content.")
	if r, err := m.Update(ctx, "skill", "sample", "", "project", true); err != nil || r.Entry.Version != "2" {
		t.Fatal("transient lock should be retried", r, err)
	}
	busy = 100
	writeFixture(t, src, "sample", "skill", "3", "Newer content.")
	before := stateBytes(t, m)
	_, err := m.Update(ctx, "skill", "sample", "", "project", true)
	if err == nil || !strings.Contains(err.Error(), "Access is denied.") || !strings.Contains(err.Error(), "another program has a file in that folder open") {
		t.Fatal("persistent lock not explained", err)
	}
	if !bytes.Equal(before, stateBytes(t, m)) {
		t.Fatal("failed update changed the package state")
	}
	if _, err := os.Stat(filepath.Join(m.Project, ".conmesh", ".packages-backup")); !os.IsNotExist(err) {
		t.Fatal("failed update left a backup that blocks later commands")
	}
}

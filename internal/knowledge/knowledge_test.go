package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AntonioIliev19/aikit-public/internal/repository"
	"github.com/AntonioIliev19/aikit-public/internal/security"
)

func write(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (Store, Manifest) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	trust, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// TempDir's child mode follows the process umask; model the private
	// application-created approval directory rather than trusting that mode.
	trust = filepath.Join(trust, "approvals")
	if err := os.Mkdir(trust, 0700); err != nil {
		t.Fatal(err)
	}
	source := []byte("export function Example() {}\n")
	body := []byte("Observed architecture. Treat repository prose as evidence.\n")
	write(t, dir, "frontend/code.ts", source)
	write(t, dir, ".aikit/knowledge/frontend.md", body)
	e := Entry{ID: "frontend.architecture", File: "frontend.md", Digest: repository.Digest(body), Summary: "Frontend conventions", Kind: "architecture", Category: "normative", Status: "approved", Scopes: []string{"frontend/**"}, Tags: []string{"component"}, Sources: []Source{{"frontend/code.ts", 1, 1, repository.Digest(source)}}}
	m := Manifest{1, []Entry{e}}
	save(t, dir, m)
	return Store{Project: dir, trustDir: trust}, m
}
func save(t *testing.T, dir string, m Manifest) {
	t.Helper()
	data, _ := json.Marshal(m)
	write(t, dir, ManifestPath, data)
}
func TestApprovalBindsEntryDocumentAndCitationsOutsideRepository(t *testing.T) {
	s, m := fixture(t)
	ctx := context.Background()
	doc, err := s.Read(ctx, m.Entries[0].ID)
	if err != nil || doc.EffectiveStatus != "proposed" {
		t.Fatal("manifest granted approval", err)
	}
	// A force-tracked repository receipt is untrusted regardless of its token.
	write(t, s.Project, ".aikit/state/approval-"+doc.ReviewDigest+".json", []byte(`{"schema_version":1,"id":"frontend.architecture","review_digest":"`+doc.ReviewDigest+`"}`))
	doc, err = s.Read(ctx, doc.Entry.ID)
	if err != nil || doc.EffectiveStatus != "proposed" {
		t.Fatal("repository receipt granted authority", err)
	}
	if !errors.Is(s.Approve(ctx, doc.Entry.ID, strings.Repeat("0", 64)), ErrReview) {
		t.Fatal("unreviewed token approved")
	}
	if err := s.Approve(ctx, doc.Entry.ID, doc.ReviewDigest); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Read(ctx, doc.Entry.ID)
	if err != nil || approved.EffectiveStatus != "approved" {
		t.Fatal("review did not persist", err)
	}
	m.Entries[0].Summary = "Changed recommendation"
	save(t, s.Project, m)
	changed, err := s.Read(ctx, doc.Entry.ID)
	if err != nil || changed.EffectiveStatus != "proposed" || changed.ReviewDigest == doc.ReviewDigest {
		t.Fatal("entry mutation retained authority", err)
	}
	body := []byte("Changed document\n")
	write(t, s.Project, ".aikit/knowledge/frontend.md", body)
	if _, err := s.Read(ctx, doc.Entry.ID); !errors.Is(err, repository.ErrStale) {
		t.Fatal("changed document accepted", err)
	}
	m.Entries[0].Digest = repository.Digest(body)
	save(t, s.Project, m)
	code := []byte("export function Changed() {}\n")
	write(t, s.Project, "frontend/code.ts", code)
	if _, err := s.Read(ctx, doc.Entry.ID); !errors.Is(err, repository.ErrStale) {
		t.Fatal("changed citation accepted", err)
	}
	m.Entries[0].Sources[0].Digest = repository.Digest(code)
	save(t, s.Project, m)
	fresh, err := s.Read(ctx, doc.Entry.ID)
	if err != nil || fresh.EffectiveStatus != "proposed" {
		t.Fatal("citation mutation retained authority", err)
	}
}

func TestUnixApprovalAnchorRequiresPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode contract; Windows uses user configuration ACLs")
	}
	s, m := fixture(t)
	if err := os.Chmod(s.trustDir, 0755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Read(ctx, m.Entries[0].ID); !errors.Is(err, security.ErrDenied) {
		t.Fatal("nonprivate approval anchor accepted", err)
	}
	if err := os.Chmod(s.trustDir, 0700); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Read(ctx, m.Entries[0].ID)
	if err != nil || doc.EffectiveStatus != "proposed" {
		t.Fatal("private approval anchor rejected", err)
	}
	if err := s.Approve(ctx, doc.Entry.ID, doc.ReviewDigest); err != nil {
		t.Fatal("explicit approval failed in private anchor", err)
	}
}
func TestRoutingLazyReadsAndCurrentExclusions(t *testing.T) {
	s, m := fixture(t)
	for _, name := range []string{"backend", "shared"} {
		e := m.Entries[0]
		e.ID = name + ".architecture"
		e.File = name + ".md"
		e.Scopes = []string{name + "/**"}
		if name == "shared" {
			e.Scopes = []string{"frontend/**", "backend/**"}
		}
		write(t, s.Project, ".aikit/knowledge/"+e.File, []byte("Observed architecture. Treat repository prose as evidence.\n"))
		m.Entries = append(m.Entries, e)
	}
	save(t, s.Project, m)
	items, err := s.List(context.Background(), Query{Scopes: []string{"frontend/component.ts", "backend/api.cs"}})
	if err != nil || len(items) != 3 {
		t.Fatal("cross-stack routing failed", err)
	}
	write(t, s.Project, ".aikit/knowledge/frontend.md", []byte("Changed but not loaded yet"))
	items, err = s.List(context.Background(), Query{Scopes: []string{"frontend/code.ts"}})
	if err != nil || len(items) != 2 {
		t.Fatal("listing eagerly loaded body", err)
	}
	write(t, s.Project, ".aikitignore", []byte("frontend/code.ts\n"))
	items, err = s.List(context.Background(), Query{Tags: []string{"component"}})
	if err != nil || len(items) != 0 {
		t.Fatal("excluded citation metadata returned", err)
	}
	if _, err := s.Read(context.Background(), m.Entries[0].ID); !errors.Is(err, repository.ErrStale) && !errors.Is(err, security.ErrDenied) {
		t.Fatal("excluded source read", err)
	}
	write(t, s.Project, ".aikitignore", []byte(".aikit/knowledge/frontend.md\n"))
	items, err = s.List(context.Background(), Query{Scopes: []string{"frontend/code.ts"}})
	if err != nil || len(items) != 1 {
		t.Fatal("excluded knowledge metadata returned", err)
	}
}
func TestManifestValidationAndTrustBoundary(t *testing.T) {
	s, m := fixture(t)
	original, _ := json.Marshal(m)
	for _, bad := range []string{`{"schema_version":1,"schema_version":1,"entries":[]}`, strings.Replace(string(original), `"frontend.md"`, `"../escape.md"`, 1), strings.Replace(string(original), `"start_line":1`, `"start_line":0`, 1), strings.Replace(string(original), `"status":"approved"`, `"status":"automatic"`, 1)} {
		write(t, s.Project, ManifestPath, []byte(bad))
		if _, err := s.List(context.Background(), Query{Tags: []string{"component"}}); err == nil {
			t.Fatal("hostile manifest accepted")
		}
	}
	save(t, s.Project, m)
	s.trustDir = filepath.Join(s.Project, "trust")
	if _, err := s.Read(context.Background(), m.Entries[0].ID); !errors.Is(err, security.ErrDenied) {
		t.Fatal("repository trust anchor accepted", err)
	}
	if _, err := os.Stat(s.trustDir); !os.IsNotExist(err) {
		t.Fatal("read created trust anchor")
	}
	for _, dir := range []string{s.Project, filepath.Join(s.Project, "child"), filepath.Join(s.Project, "alias", "..", "child")} {
		if outsideProject(s.Project, dir) {
			t.Fatal("contained trust path accepted")
		}
	}
	if runtime.GOOS == "windows" {
		if !outsideProject(`F:\AIKIT`, `C:\Users\test\trust`) {
			t.Fatal("cross-volume external anchor rejected")
		}
		if outsideProject(`F:\AIKIT`, `f:\aikit\trust`) {
			t.Fatal("case alias accepted")
		}
	}
}
func TestScaffoldAndMissingUserConfigAncestors(t *testing.T) {
	s, _ := fixture(t)
	missing := filepath.Join(s.trustDir, "missing", "config")
	s.trustDir = filepath.Join(missing, "aikit", "knowledge-approvals")
	doc, err := s.Read(context.Background(), "frontend.architecture")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Approve(context.Background(), doc.Entry.ID, doc.ReviewDigest); err != nil {
		t.Fatal("first approval failed", err)
	}
	before, err := os.ReadFile(filepath.Join(s.Project, ManifestPath))
	if err != nil {
		t.Fatal(err)
	}
	if s.Scaffold() == nil {
		t.Fatal("existing manifest overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(s.Project, ManifestPath))
	if string(before) != string(after) {
		t.Fatal("scaffold clobbered manifest")
	}
}

package index

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/embeddings"
	"github.com/AntonioIliev19/conmesh/internal/repository"
	"github.com/AntonioIliev19/conmesh/internal/security"
)

func TestMain(m *testing.M) {
	switch os.Getenv("CONMESH_FAKE_RG_MODE") {
	case "deny":
		os.WriteFile(os.Getenv("CONMESH_FAKE_RG_IGNORE"), []byte("src/code.ts\n"), 0600)
		io.WriteString(os.Stdout, "1\n")
		os.Exit(0)
	case "wait":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// This adapter fixture exercises real HTTP/persistence flow with mocked vectors.
// It is not a real-model benchmark or retrieval-quality measurement.
func semanticFixture(t *testing.T, files map[string]string, onShow func()) (*Core, string, *atomic.Int32) {
	t.Helper()
	c, dir := fixture(t, true, files)
	calls := new(atomic.Int32)
	license := "fixture Apache-2.0 identity"
	modelDigest := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]string{"name": "nomic-embed-text:v1.5", "digest": modelDigest}}})
		case "/api/show":
			if onShow != nil {
				onShow()
			}
			json.NewEncoder(w).Encode(map[string]any{"license": license, "capabilities": []string{"embedding"}, "model_info": map[string]int{"nomic-bert.embedding_length": embeddings.Dimensions}})
		case "/api/embed":
			calls.Add(1)
			var req struct {
				Model string   `json:"model"`
				Input []string `json:"input"`
			}
			if json.NewDecoder(r.Body).Decode(&req) != nil {
				t.Error("bad embedding request")
			}
			out := [][]float32{}
			for range req.Input {
				v := make([]float32, embeddings.Dimensions)
				v[0] = 1
				out = append(out, v)
			}
			json.NewEncoder(w).Encode(map[string]any{"model": req.Model, "embeddings": out})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	cfg := config.Default()
	cfg.Indexing.Enabled = true
	cfg.Indexing.Semantic = true
	cfg.Semantic = &embeddings.Settings{Endpoint: server.URL, Model: "nomic-embed-text:v1.5", ModelDigest: modelDigest, LicenseDigest: repository.Digest([]byte(license)), Consent: true, LocalOnly: true}
	data, _ := json.Marshal(cfg)
	write(t, dir, ".conmesh/"+config.File, string(data))
	return c, dir, calls
}

func TestSemanticMockReuseFusionPurgeAndBasicOptionality(t *testing.T) {
	native(t)
	ctx := context.Background()
	c, dir, calls := semanticFixture(t, map[string]string{"src/code.ts": "export function Alpha(){ return 'orchidfixtureword'; }\nexport function Beta(){ return 'secondfixtureword'; }\n"}, nil)
	update(t, c)
	if len(search(t, c, "orchidfixtureword")) != 1 || calls.Load() != 0 {
		t.Fatal("basic path contacted runtime")
	}
	first, err := c.IndexSemantic(ctx, "src")
	if err != nil || first.Embedded != 2 || first.Reused != 0 {
		t.Fatal("mock embedding persistence failed", first, err)
	}
	again, err := c.IndexSemantic(ctx, "src")
	if err != nil || again.Embedded != 0 || again.Reused != 2 {
		t.Fatal("unchanged vectors not reused", again, err)
	}
	result, err := c.Hybrid(ctx, Query{Text: "orchidfixtureword", Scope: "src"})
	if err != nil || len(result.Evidence) == 0 || result.Candidates != 2 || result.Evidence[0].Mechanism != "hybrid_rrf" {
		t.Fatal("fusion failed", err)
	}
	write(t, dir, "src/code.ts", "export function Alpha(){ return 'orchidfixtureword'; }\nexport function Beta(){ return 'changedfixtureword'; }\n")
	update(t, c)
	changed, err := c.IndexSemantic(ctx, "src")
	if err != nil || changed.Reused != 1 || changed.Embedded != 1 {
		t.Fatal("sibling edit discarded unchanged chunk", changed, err)
	}
	write(t, dir, ".conmeshignore", "src/code.ts\n")
	update(t, c)
	cache, err := c.semanticDB(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	cache.QueryRowContext(ctx, "SELECT count(*) FROM vectors").Scan(&count)
	cache.Close()
	if count != 0 {
		t.Fatal("ignored vectors persisted")
	}
	// Optional broken cache never turns a committed basic index into a failure.
	write(t, dir, ".conmesh/state/semantic.sqlite", "corrupted optional cache")
	write(t, dir, "other.txt", "newbasicfixtureword\n")
	report, err := c.Index(ctx, nil)
	if err != nil || !report.SemanticCleanupWarning || len(search(t, c, "newbasicfixtureword")) != 1 {
		t.Fatal("optional cache blocked basic indexing", err)
	}
}
func TestSemanticReauthorizesAfterRuntimeIdentityBeforeSending(t *testing.T) {
	native(t)
	var dir string
	c, project, calls := semanticFixture(t, map[string]string{"src/code.ts": "export function Alpha() {}\n"}, func() { write(t, dir, ".conmeshignore", "src/code.ts\n") })
	dir = project
	update(t, c)
	if _, err := c.IndexSemantic(context.Background(), "src"); !errors.Is(err, security.ErrDenied) || calls.Load() != 0 {
		t.Fatal("newly excluded batch crossed runtime boundary", err)
	}
}
func TestSemanticStaleAndMissingConfiguration(t *testing.T) {
	c, _ := fixture(t, true, nil)
	if _, err := c.Hybrid(context.Background(), Query{Text: "example"}); !errors.Is(err, embeddings.ErrUnavailable) {
		t.Fatal("missing model error unclear", err)
	}
	native(t)
	c, dir, calls := semanticFixture(t, map[string]string{"src/code.ts": "export function Alpha() {}\n"}, nil)
	update(t, c)
	write(t, dir, "src/code.ts", "export function Changed() {}\n")
	if _, err := c.IndexSemantic(context.Background(), "src"); !errors.Is(err, repository.ErrStale) || calls.Load() != 0 {
		t.Fatal("stale source embedded", err)
	}
}
func TestReferenceCandidatesAndFallbackGateAudit(t *testing.T) {
	native(t)
	ctx := context.Background()
	c, dir := fixture(t, true, map[string]string{"src/code.ts": "export function Alpha(){ return 'prefixneedlesuffix'; }\n", "ignored.txt": "needle", ".conmeshignore": "ignored.txt\n", "secret.txt": "password = abcdefghijklmnop\n"})
	update(t, c)
	refs, err := c.FindReferenceCandidates(ctx, Query{Text: "Alpha"})
	if err != nil || len(refs) != 1 || refs[0].Mechanism != "lexical_reference_candidate_no_lsp" {
		t.Fatal("reference labeling failed", err)
	}
	hit, err := c.Fallback(ctx, Query{Text: "Alpha"})
	if err != nil || hit.Used || len(hit.Evidence) == 0 {
		t.Fatal("fallback bypassed indexed hit", err)
	}
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("real ripgrep integration unavailable")
	}
	result, err := c.Fallback(ctx, Query{Text: "needle"})
	if err != nil || !result.Used || result.Counter != 1 || len(result.Evidence) != 1 || result.Evidence[0].Path != "src/code.ts" {
		t.Fatal("bounded real ripgrep fallback failed", result.Used, err)
	}
	db, err := c.open(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var reason string
	db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='fallback_reason'").Scan(&reason)
	db.Close()
	if reason != "fresh_index_systematic_miss" {
		t.Fatal("fallback audit missing")
	}
	write(t, dir, "src/new.txt", "a newly added file\n")
	if _, err := c.Fallback(ctx, Query{Text: "unmatchedliteral"}); !errors.Is(err, repository.ErrStale) {
		t.Fatal("fallback ignored stale additions", err)
	}
	if _, err := c.Fallback(ctx, Query{Text: "needle\n--pre=unsafe"}); !errors.Is(err, ErrQuery) {
		t.Fatal("hostile query accepted", err)
	}
}
func TestSemanticCandidateCapAndDeterministicFusion(t *testing.T) {
	native(t)
	c, _ := fixture(t, true, map[string]string{"same.txt": "safe\n"})
	update(t, c)
	db, err := c.open(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<4097) INSERT INTO chunks(file_id,text,start_line,end_line,digest) SELECT (SELECT id FROM files WHERE path='same.txt'),'safe',1,1,? FROM n`, repository.Digest([]byte("safe")))
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	chunks, limited, err := c.semanticChunks(context.Background(), "same.txt")
	if err != nil || !limited || len(chunks) != MaxSemanticCandidates {
		t.Fatal("candidate scan unbounded", err)
	}
	a := Evidence{Path: "b.ts", StartLine: 1, EndLine: 1}
	b := Evidence{Path: "a.ts", StartLine: 1, EndLine: 1}
	out := fuse(2, []Evidence{a}, []Evidence{b})
	if out[0].Path != "a.ts" {
		t.Fatal("unstable rank tie")
	}
}
func TestSemanticSidecarPathIsPrivate(t *testing.T) {
	c, dir := fixture(t, true, nil)
	root, err := security.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := security.EnsurePrivateState(root); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(external, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".conmesh/state/semantic.sqlite-wal")
	if err := os.Symlink(external, link); err != nil {
		t.Skip("symlink unavailable")
	}
	if _, err := c.semanticDB(context.Background(), true); !errors.Is(err, security.ErrDenied) {
		t.Fatal("SQLite companion symlink accepted", err)
	}
}
func TestFallbackRechecksAfterSubprocessAndDeadlines(t *testing.T) {
	native(t)
	c, dir := fixture(t, true, map[string]string{"src/code.ts": "export function Alpha(){return 'prefixneedlesuffix';}\n"})
	update(t, c)
	tools := t.TempDir()
	name := "rg"
	if filepath.Ext(os.Args[0]) == ".exe" {
		name += ".exe"
	}
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(tools, name)
	if err := os.WriteFile(fake, data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools)
	t.Setenv("CONMESH_FAKE_RG_MODE", "deny")
	t.Setenv("CONMESH_FAKE_RG_IGNORE", filepath.Join(dir, ".conmeshignore"))
	if _, err := c.Fallback(context.Background(), Query{Text: "needle", Limit: 1}); !errors.Is(err, security.ErrDenied) {
		t.Fatal("post-process exclusion bypassed", err)
	}
	t.Setenv("CONMESH_FAKE_RG_MODE", "wait")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runRipgrep(ctx, fake, "safe", []byte("safe")); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("subprocess caller deadline ignored", err)
	}
	start = time.Now()
	if _, err := runRipgrep(context.Background(), fake, "safe", []byte("safe")); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 4*time.Second {
		t.Fatal("subprocess fixed deadline ignored", err)
	}
}
func TestFinalEvidenceCheckIncludesLexicalOutsideSemanticPrefix(t *testing.T) {
	native(t)
	c, dir := fixture(t, true, map[string]string{"z.txt": "lexicalfixtureword\n"})
	update(t, c)
	items := search(t, c, "lexicalfixtureword")
	write(t, dir, ".conmeshignore", "z.txt\n")
	if err := c.checkEvidence(context.Background(), items); !errors.Is(err, security.ErrDenied) {
		t.Fatal("final lexical evidence unchecked", err)
	}
}

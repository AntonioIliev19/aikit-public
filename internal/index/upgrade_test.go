package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/repository"
)

// plainText lets the index pipeline run in CGO-disabled builds. Fixtures must
// only contain files that repository.Language maps to "text".
func plainText(t *testing.T) {
	t.Helper()
	previous := parserAvailable
	parserAvailable = func() bool { return true }
	t.Cleanup(func() { parserAvailable = previous })
}

// rawDB opens the index file directly, bypassing open() and its upgrade path.
func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, ".conmesh", "state", "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func indexNames(t *testing.T, db *sql.DB) map[string]bool {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='index'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	return out
}

func TestCamelCaseSubtokenSearch(t *testing.T) {
	plainText(t)
	c, _ := fixture(t, true, map[string]string{
		"notes.txt": "the requestHandler talks to HTTPServer via snake_case_value\n",
		"other.txt": "unrelated content\n",
	})
	update(t, c)
	for _, q := range []string{"handler", "Handler", "request", "server", "http", "snake", "case_value", "request handler", "requestHandler"} {
		got := search(t, c, q)
		if len(got) != 1 || got[0].Path != "notes.txt" {
			t.Fatalf("query %q: got %v", q, got)
		}
		if strings.Contains(got[0].Snippet, "request handler") {
			t.Fatal("FTS expansion leaked into evidence snippet", got[0].Snippet)
		}
	}
	if got := search(t, c, "handlers"); len(got) != 0 {
		t.Fatal("unexpected match", got)
	}
}

func TestSchemaV1IndexStaysReadableAndUpgrades(t *testing.T) {
	plainText(t)
	ctx := context.Background()
	c, dir := fixture(t, true, map[string]string{"notes.txt": "the requestHandler handles upgradeword\n", ".conmeshignore": ".conmesh/\n.conmeshignore\n"})
	update(t, c)
	db := rawDB(t, dir)
	if v := userVersion(t, db); v != schemaVersion {
		t.Fatal("fresh index has wrong version", v)
	}
	for _, name := range []string{"chunks_file", "symbols_file", "chunks_digest"} {
		if !indexNames(t, db)[name] {
			t.Fatal("fresh index lacks", name)
		}
	}
	// Rewrite the database into exactly the version 1 layout: no child-key
	// indexes, schema marker 1, no extractor version, FTS rows without expansion.
	if _, err := db.Exec(`DROP INDEX chunks_file; DROP INDEX symbols_file; DROP INDEX chunks_digest;
		UPDATE metadata SET value='1' WHERE key='schema'; DELETE FROM metadata WHERE key='extractor';
		DELETE FROM fts_chunks; INSERT INTO fts_chunks(rowid,text) SELECT id,text FROM chunks;
		PRAGMA user_version=1;`); err != nil {
		t.Fatal(err)
	}
	// Readers accept version 1 without modifying it.
	if got := search(t, c, "upgradeword"); len(got) != 1 {
		t.Fatal("version 1 index unreadable", got)
	}
	if got := search(t, c, "handler"); len(got) != 0 {
		t.Fatal("version 1 fixture unexpectedly has subtokens", got)
	}
	if status, err := c.Status(ctx); err != nil || !status.Present || status.Files != 1 {
		t.Fatal("version 1 status", status, err)
	}
	if v := userVersion(t, db); v != 1 || indexNames(t, db)["chunks_file"] {
		t.Fatal("reader mutated version 1 index", v)
	}
	// The next writer upgrades in place and re-extracts unchanged files.
	p := update(t, c)
	if !p.Refreshed || p.Indexed != 1 || p.Unchanged != 0 {
		t.Fatal("upgrade did not re-extract", p)
	}
	if v := userVersion(t, db); v != schemaVersion {
		t.Fatal("writer did not upgrade", v)
	}
	var marker, extractor string
	if err := db.QueryRow("SELECT (SELECT value FROM metadata WHERE key='schema'),(SELECT value FROM metadata WHERE key='extractor')").Scan(&marker, &extractor); err != nil || marker != "2" || extractor != extractorVersion {
		t.Fatal("upgrade metadata", marker, extractor, err)
	}
	for _, name := range []string{"chunks_file", "symbols_file", "chunks_digest"} {
		if !indexNames(t, db)[name] {
			t.Fatal("upgrade lacks", name)
		}
	}
	if got := search(t, c, "handler"); len(got) != 1 {
		t.Fatal("re-extraction missing subtokens", got)
	}
	for table, index := range map[string]string{"chunks": "chunks_file", "symbols": "symbols_file"} {
		var plan strings.Builder
		rows, err := db.Query("EXPLAIN QUERY PLAN SELECT id FROM " + table + " WHERE file_id=1")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail)
		}
		rows.Close()
		if !strings.Contains(plan.String(), index) {
			t.Fatal("child lookup does not use", index, plan.String())
		}
	}
	if p := update(t, c); p.Refreshed || p.Unchanged != 1 || p.Indexed != 0 {
		t.Fatal("second run after upgrade not incremental", p)
	}
	// A version 1 database with an inconsistent marker is still rejected untouched.
	if _, err := db.Exec("UPDATE metadata SET value='7' WHERE key='schema'; PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Index(ctx, nil); err == nil {
		t.Fatal("inconsistent version 1 marker accepted")
	}
	if v := userVersion(t, db); v != 1 {
		t.Fatal("rejected database mutated", v)
	}
}

func TestStateErrorKeepsCause(t *testing.T) {
	cause := errors.New("database is locked")
	err := stateError(cause)
	if !errors.Is(err, ErrState) || !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), ErrState.Error()) || !strings.Contains(err.Error(), "database is locked") {
		t.Fatal("cause not preserved", err)
	}
	if stateError(nil) != ErrState {
		t.Fatal("nil cause must be plain ErrState")
	}
}

func TestExtractorVersionMismatchReextracts(t *testing.T) {
	plainText(t)
	c, dir := fixture(t, true, map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n"})
	if p := update(t, c); p.Refreshed || p.Indexed < 2 {
		t.Fatal("fresh index reported refresh", p)
	}
	if p := update(t, c); p.Refreshed || p.Indexed != 0 {
		t.Fatal("unchanged run re-extracted", p)
	}
	if _, err := rawDB(t, dir).Exec("UPDATE metadata SET value='parser-0/terms-0' WHERE key='extractor'"); err != nil {
		t.Fatal(err)
	}
	p := update(t, c)
	if !p.Refreshed || p.Unchanged != 0 || p.Indexed < 2 {
		t.Fatal("stale extractor entries kept", p)
	}
	if p := update(t, c); p.Refreshed || p.Indexed != 0 {
		t.Fatal("refresh repeated", p)
	}
}

func TestSkipReasonsSumToSkipped(t *testing.T) {
	plainText(t)
	c, _ := fixture(t, true, map[string]string{
		".gitignore":     "ignored.txt\n",
		".conmeshignore": ".conmesh/\n",
		"ignored.txt":    "x\n",
		"vendor/lib.txt": "x\n",
		"big.txt":        strings.Repeat("x", repository.MaxFileBytes+1),
		"secret.txt":     "password = abcdefghijklmnop\n",
		"binary.txt":     "\x00",
		"wide.txt":       strings.Repeat("y", 17<<10),
		"ok.txt":         "fine\n",
	})
	p := update(t, c)
	r := p.SkipReasons
	if r.Total() != p.Skipped {
		t.Fatalf("reasons %+v do not sum to skipped %d", r, p.Skipped)
	}
	if r.Ignored < 2 || r.HardDenied != 1 || r.TooLarge != 1 || r.SensitiveContent != 1 || r.Binary != 1 || r.ParserLimit != 1 || p.Limited != 1 {
		t.Fatalf("unexpected reasons %+v (limited %d)", r, p.Limited)
	}
	if len(search(t, c, "fine")) != 1 {
		t.Fatal("ordinary file missing")
	}
}

func TestSemanticBatchedVectorsAcrossBatches(t *testing.T) {
	plainText(t)
	files := map[string]string{}
	n := vectorBatch + 20
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("src/f%03d.txt", i)] = fmt.Sprintf("batchword unique%03d\n", i)
	}
	c, _, calls := semanticFixture(t, files, nil)
	update(t, c)
	ctx := context.Background()
	first, err := c.IndexSemantic(ctx, "src")
	if err != nil || first.Embedded != n || first.Reused != 0 || first.Chunks != n {
		t.Fatal("first semantic index", first, err)
	}
	before := calls.Load()
	again, err := c.IndexSemantic(ctx, "src")
	if err != nil || again.Embedded != 0 || again.Reused != n || calls.Load() != before {
		t.Fatal("batched reuse lookup", again, err)
	}
	result, err := c.Hybrid(ctx, Query{Text: "batchword", Scope: "src", Limit: 5})
	if err != nil || result.Candidates != n || len(result.Evidence) != 5 {
		t.Fatal("hybrid over batched vectors", result.Candidates, len(result.Evidence), err)
	}
	// One missing vector still reports the cache as incomplete.
	cache, err := c.semanticDB(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Exec("DELETE FROM vectors WHERE rowid=(SELECT max(rowid) FROM vectors)"); err != nil {
		t.Fatal(err)
	}
	cache.Close()
	if _, err := c.Hybrid(ctx, Query{Text: "batchword", Scope: "src"}); err == nil {
		t.Fatal("incomplete vector cache accepted")
	}
}

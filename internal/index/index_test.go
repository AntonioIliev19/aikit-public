package index

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"

	"github.com/AntonioIliev19/aikit-public/internal/config"
	"github.com/AntonioIliev19/aikit-public/internal/parser"
	"github.com/AntonioIliev19/aikit-public/internal/repository"
	"github.com/AntonioIliev19/aikit-public/internal/security"
)

func fixture(t *testing.T, enabled bool, files map[string]string) (*Core, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Indexing.Enabled = enabled
	if _, _, err := config.Init(dir, cfg); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		write(t, dir, name, data)
	}
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func native(t *testing.T) {
	t.Helper()
	if !parser.Available() {
		t.Skip("native CGO parser unavailable")
	}
}
func update(t *testing.T, c *Core) Progress {
	t.Helper()
	p, err := c.Index(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func search(t *testing.T, c *Core, text string) []Evidence {
	t.Helper()
	r, err := c.Search(context.Background(), Query{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOptInAndReadOnlyAbsent(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c, dir := fixture(t, enabled, nil)
		status, err := c.Status(context.Background())
		if err != nil || status.Present || status.Enabled != enabled {
			t.Fatal("unexpected absent status", status, err)
		}
		_, err = c.Search(context.Background(), Query{Text: "hello"})
		want := ErrAbsent
		if !enabled {
			want = ErrDisabled
		}
		if !errors.Is(err, want) {
			t.Fatal("wrong missing index error", err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".aikit", "state")); !os.IsNotExist(err) {
			t.Fatal("read-only API created state")
		}
		if err := os.Mkdir(filepath.Join(dir, ".aikit", "state"), 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Status(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".aikit", "state", "index.sqlite")); !os.IsNotExist(err) {
			t.Fatal("status created absent database")
		}
		if _, err := c.Search(context.Background(), Query{Text: "hello"}); !errors.Is(err, want) {
			t.Fatal("query accepted absent database", err)
		}
		if _, err := os.Stat(filepath.Join(dir, ".aikit", "state", "index.sqlite")); !os.IsNotExist(err) {
			t.Fatal("search created absent database")
		}
		if !enabled {
			if _, err := c.Index(context.Background(), nil); !errors.Is(err, ErrDisabled) {
				t.Fatal("missing consent accepted")
			}
		}
	}
	if !parser.Available() {
		c, _ := fixture(t, true, nil)
		if _, err := c.Index(context.Background(), nil); !errors.Is(err, parser.ErrUnavailable) {
			t.Fatal("CGO-disabled indexing must fail clearly", err)
		}
	}
}

func TestIncrementalReopenAndFreshness(t *testing.T) {
	native(t)
	c, dir := fixture(t, true, map[string]string{"src/a.ts": "export function greet() { return 'sunflower'; }\n", "src/G.cs": "public class Greeter { public string Hello() { return \"sunflower\"; } }\n"})
	p := update(t, c)
	if p.Indexed < 2 || p.Workers < 1 || p.Queue != p.Workers*2 {
		t.Fatal("bad initial progress", p)
	}
	got := search(t, c, "sunflower")
	if len(got) != 2 {
		t.Fatal("FTS language evidence missing", got)
	}
	if got[0].StartLine != 1 || got[0].Mechanism != "fts5" || !reflect.DeepEqual(got, search(t, c, "sunflower")) {
		t.Fatal("unexpected deterministic evidence", got)
	}
	syms, err := c.FindSymbol(context.Background(), Query{Text: "Hello"})
	if err != nil || len(syms) != 1 || syms[0].Kind != "method_declaration" {
		t.Fatal("C# symbols missing", err, syms)
	}
	syms, err = c.FindSymbol(context.Background(), Query{Text: "greet"})
	if err != nil || len(syms) != 1 {
		t.Fatal("TypeScript symbols missing", err, syms)
	}
	second := update(t, c)
	if second.Indexed != 0 || second.Unchanged != p.Indexed {
		t.Fatal("unchanged content was reparsed", second)
	}
	c, err = New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, search(t, c, "sunflower")) {
		t.Fatal("restart changed retrieval")
	}
	write(t, dir, "src/a.ts", "export function welcome() { return 'moonflower'; }\n")
	if _, err := c.Search(context.Background(), Query{Text: "sunflower", Scope: "src/a.ts"}); !errors.Is(err, repository.ErrStale) {
		t.Fatal("stale content served", err)
	}
	if err := os.Remove(filepath.Join(dir, "src", "G.cs")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "src/new.ts", "export const NewSymbol = 'moonflower';\n")
	p = update(t, c)
	if p.Indexed != 2 || p.Deleted != 1 {
		t.Fatal("wrong incremental delta", p)
	}
	if len(search(t, c, "sunflower")) != 0 || len(search(t, c, "moonflower")) != 2 {
		t.Fatal("incremental FTS stale rows")
	}
	syms, err = c.FindSymbol(context.Background(), Query{Text: "greet"})
	if err != nil || len(syms) != 0 {
		t.Fatal("stale symbol", err, syms)
	}
}

func TestExclusionsAcrossPersistenceAndRetrieval(t *testing.T) {
	native(t)
	const marker = "forbiddenfixtureword"
	files := map[string]string{
		".gitignore": "ignored/\nnested/*.ts\n", ".aikitignore": "own/\n!ignored/\n",
		"nested/.gitignore": "!allowed.ts\n", "nested/.aikitignore": "private.ts\n",
		"ignored/escape/.gitignore": "!code.ts\n", "ignored/escape/code.ts": "export const Hidden = '" + marker + "';",
		"own/code.ts": "export const Hidden = '" + marker + "';", "nested/blocked.ts": "export const Hidden = '" + marker + "';",
		"nested/private.ts": "export const Hidden = '" + marker + "';", ".env": "" + marker,
		"a/KEY.PEM": marker, "credentials/settings.json": marker, ".aikit/state/leak.txt": marker,
		"secret.ts":                           "export const token = '" + marker + "'; // api_key=longSyntheticValue123456\n",
		"api_key=longSyntheticValue123456.ts": "export const Hidden = '" + marker + "';",
		"bad.bin":                             "\x00" + marker, "big.txt": strings.Repeat("x", repository.MaxFileBytes+1),
		"malformed.ts":      "export function Broken( { " + marker,
		"nested/allowed.ts": "export function Allowed() { return 'allowedfixtureword'; }\n",
	}
	c, dir := fixture(t, true, files)
	p := update(t, c)
	if p.Skipped < 8 || p.Malformed != 1 {
		t.Fatal("missing explicit exclusion outcomes", p)
	}
	if len(search(t, c, marker)) != 0 || len(search(t, c, "allowedfixtureword")) != 1 {
		t.Fatal("policy retrieval mismatch")
	}
	db, err := c.open(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"files", "symbols", "chunks", "fts_chunks"} {
		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		for rows.Next() {
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for _, value := range values {
				if s, ok := value.(string); ok && strings.Contains(s, marker) {
					t.Fatal("excluded fixture persisted in", table)
				}
			}
		}
		rows.Close()
	}
	db.Close()
	write(t, dir, ".aikitignore", "own/\nnested/allowed.ts\n")
	if _, err := c.FindSymbol(context.Background(), Query{Text: "Allowed"}); !errors.Is(err, repository.ErrStale) {
		t.Fatal("new ignore not checked for symbols", err)
	}
	if _, err := c.Search(context.Background(), Query{Text: "allowedfixtureword"}); !errors.Is(err, repository.ErrStale) {
		t.Fatal("new ignore not checked for FTS", err)
	}
	p = update(t, c)
	if p.Deleted < 1 {
		t.Fatal("policy change did not purge")
	}
	if len(search(t, c, "allowedfixtureword")) != 0 {
		t.Fatal("denied rows remain")
	}
}

func TestMidRunPolicyAndCancellationRollback(t *testing.T) {
	native(t)
	c, dir := fixture(t, true, map[string]string{"a.ts": "export const Alpha = 'baselineword';", "b.ts": "export const Beta = 'baselineword';"})
	update(t, c)
	write(t, dir, "a.ts", "export const Alpha = 'replacementword';")
	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.Index(ctx, func(p Progress) {
		if p.Processed > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not reported", err)
	}
	db, err := c.open(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM fts_chunks WHERE fts_chunks MATCH 'replacementword'").Scan(&count); err != nil || count != 0 {
		t.Fatal("cancelled transaction persisted", err)
	}
	db.Close()
	once := sync.Once{}
	_, err = c.Index(context.Background(), func(p Progress) {
		if p.Processed > 0 {
			once.Do(func() { write(t, dir, ".aikitignore", "*.ts\n") })
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(search(t, c, "baselineword")) != 0 || len(search(t, c, "replacementword")) != 0 {
		t.Fatal("mid-run exclusion persisted")
	}
}

func TestQueriesBoundedAndConcurrent(t *testing.T) {
	native(t)
	c, dir := fixture(t, true, map[string]string{"src/a.ts": "export const Alpha = 'rankingword';", "other/a.ts": "export const Alpha = 'rankingword';"})
	update(t, c)
	for _, q := range []Query{{Text: "x", Scope: "../escape"}, {Text: "x", Scope: "a:ads"}, {Text: "x", Scope: `a\b`}, {Text: "x", Limit: 51}, {Text: "x", Limit: -1}, {Text: strings.Repeat("x", 257)}, {Text: "\n"}, {Text: "---"}} {
		if _, err := c.Search(context.Background(), q); !errors.Is(err, ErrQuery) {
			t.Fatal("invalid query accepted", q, err)
		}
	}
	r, err := c.Search(context.Background(), Query{Text: "rankingword", Scope: "src", Limit: 1})
	if err != nil || len(r) != 1 || r[0].Path != "src/a.ts" {
		t.Fatal("scope mismatch", err, r)
	}
	for _, text := range []string{"rankingword OR secret", `rankingword"`, "rankingword*"} {
		if _, err := c.Search(context.Background(), Query{Text: text}); err != nil {
			t.Fatal("literal FTS syntax handling", err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				if _, err := c.Search(context.Background(), Query{Text: "rankingword"}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Index(context.Background(), nil)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	// Every retrieval rechecks content secrets, including after an unchanged index run.
	write(t, dir, "src/a.ts", "export const Alpha = 'rankingword'; // password=longSyntheticValue123456")
	if _, err := c.FindSymbol(context.Background(), Query{Text: "Alpha", Scope: "src"}); !errors.Is(err, repository.ErrStale) {
		t.Fatal("new content secret served", err)
	}
}

func TestUnsafeAndIncompatibleState(t *testing.T) {
	c, dir := fixture(t, true, nil)
	state := filepath.Join(dir, ".aikit", "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(state, "index.sqlite")
	if err := os.WriteFile(name, nil, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE metadata(key TEXT,value TEXT); INSERT INTO metadata VALUES('schema','99'); PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if db, err := c.open(context.Background(), true); !errors.Is(err, ErrState) {
		if db != nil {
			db.Close()
		}
		t.Fatal("incompatible writer state accepted", err)
	}
	after, err := os.ReadFile(name)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("incompatible DB mutated")
	}
	if db, err := c.open(context.Background(), false); !errors.Is(err, ErrState) {
		if db != nil {
			db.Close()
		}
		t.Fatal("incompatible reader state accepted", err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.sqlite")
	os.WriteFile(outside, []byte("unchanged"), 0600)
	if err := os.Symlink(outside, name); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if _, err := c.open(context.Background(), true); !errors.Is(err, security.ErrDenied) {
		t.Fatal("state symlink accepted", err)
	}
}

func TestPoisonedEvidenceFailsClosed(t *testing.T) {
	native(t)
	for _, poison := range []struct {
		name, statement string
		symbolOnly      bool
	}{
		{"traversal", `UPDATE files SET path='../outside.ts' WHERE path='safe.ts'`, false},
		{"hard-denied", `UPDATE files SET path='.env' WHERE path='safe.ts'`, false},
		{"kind", `UPDATE symbols SET kind='arbitrary_untrusted_payload'`, true},
		{"large-kind", `UPDATE symbols SET kind=printf('%100000s','payload')`, true},
	} {
		t.Run(poison.name, func(t *testing.T) {
			c, _ := fixture(t, true, map[string]string{"safe.ts": "export function Safe() { return 'evidenceword'; }"})
			update(t, c)
			db, err := c.open(context.Background(), true)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(poison.statement); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if r, err := c.FindSymbol(context.Background(), Query{Text: "Safe"}); err == nil || len(r) != 0 {
				t.Fatal("poisoned symbol evidence returned")
			}
			if !poison.symbolOnly {
				if r, err := c.Search(context.Background(), Query{Text: "evidenceword"}); err == nil || len(r) != 0 {
					t.Fatal("poisoned FTS evidence returned")
				}
			}
		})
	}
	c, _ := fixture(t, true, map[string]string{"safe.ts": "export const Safe = 1;"})
	update(t, c)
	db, err := c.open(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE metadata SET value=printf('%100000s','untrusted') WHERE key='indexed_at'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	status, err := c.Status(context.Background())
	if !errors.Is(err, ErrState) || status.IndexedAt != "" {
		t.Fatal("untrusted timestamp returned")
	}
}

func TestSQLiteEscapedRootAndFutureVersion(t *testing.T) {
	native(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	component := "root#literal"
	if runtime.GOOS != "windows" {
		component += "?mode=ro"
	}
	dir := filepath.Join(base, component)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Indexing.Enabled = true
	if _, _, err := config.Init(dir, cfg); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "code.ts", "export const Escaped = 'escapedrootword';")
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	update(t, c)
	if len(search(t, c, "escapedrootword")) != 1 {
		t.Fatal("URI root interpretation changed")
	}
	db, err := c.open(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, writer := range []bool{false, true} {
		if db, err := c.open(context.Background(), writer); !errors.Is(err, ErrState) {
			if db != nil {
				db.Close()
			}
			t.Fatal("future schema accepted", writer, err)
		}
	}
}

func TestMemoryProfileRestoresLowerLimit(t *testing.T) {
	previous := debug.SetMemoryLimit(64 << 20)
	defer debug.SetMemoryLimit(previous)
	target, release, err := resources(context.Background(), "performance")
	if err != nil {
		t.Fatal(err)
	}
	if target > 64<<20 || target <= 0 {
		release()
		t.Fatal("existing lower Go limit raised")
	}
	release()
	if debug.SetMemoryLimit(-1) != 64<<20 {
		t.Fatal("Go memory limit not restored")
	}
	for _, profile := range []string{"eco", "balanced", "performance"} {
		if n := Workers(profile); n < 1 || n > 8 {
			t.Fatal("unbounded worker profile")
		}
	}
}

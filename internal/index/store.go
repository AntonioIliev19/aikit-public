// Package index owns the authorized, local SQLite repository index and query API.
package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/security"
	_ "modernc.org/sqlite"
)

var ErrDisabled = errors.New("INDEX_DISABLED: explicitly enable indexing in project configuration first")
var ErrAbsent = errors.New("INDEX_ABSENT: run conmesh index after explicit opt-in")
var ErrState = errors.New("INDEX_STATE: unsafe, incompatible or unavailable index")

// schemaVersion is stored both as PRAGMA user_version and metadata 'schema'.
// Version 1 lacked the child-key indexes below; it stays readable and the first
// writer upgrades it in place (see migrate). Older binaries reject version 2.
const schemaVersion = 2

// schemaIndexes keeps replace/remove and the ON DELETE CASCADE from scanning
// whole child tables, and lets semantic pruning look chunks up by digest.
const schemaIndexes = `
CREATE INDEX IF NOT EXISTS chunks_file ON chunks(file_id);
CREATE INDEX IF NOT EXISTS symbols_file ON symbols(file_id);
CREATE INDEX IF NOT EXISTS chunks_digest ON chunks(digest);
`

const schema = `
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT OR IGNORE INTO metadata VALUES ('schema','2');
CREATE TABLE IF NOT EXISTS files (id INTEGER PRIMARY KEY, path TEXT UNIQUE NOT NULL, digest TEXT NOT NULL, language TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS symbols (id INTEGER PRIMARY KEY, file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE, name TEXT NOT NULL, kind TEXT NOT NULL, start_line INTEGER NOT NULL, end_line INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS symbol_names ON symbols(name);
CREATE TABLE IF NOT EXISTS chunks (id INTEGER PRIMARY KEY, file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE, text TEXT NOT NULL, start_line INTEGER NOT NULL, end_line INTEGER NOT NULL, digest TEXT NOT NULL);
CREATE VIRTUAL TABLE IF NOT EXISTS fts_chunks USING fts5(text, tokenize='unicode61');
` + schemaIndexes

// stateError keeps ErrState as the classification while preserving the cause
// (for example "database is locked") for local diagnostics. MCP responses map
// errors to fixed codes and never include this text.
func stateError(cause error) error {
	if cause == nil {
		return ErrState
	}
	return fmt.Errorf("%w: %w", ErrState, cause)
}

type Core struct{ project string }

// New binds the API to a trusted CLI project root; request arguments never select roots.
func New(project string) (*Core, error) {
	root, err := security.OpenRoot(project)
	if err != nil {
		return nil, security.ErrDenied
	}
	root.Close()
	abs, err := filepath.Abs(project)
	if err != nil {
		return nil, security.ErrDenied
	}
	return &Core{project: abs}, nil
}

func (c *Core) enabled() (config.Config, error) {
	cfg, err := config.Load(c.project)
	if err != nil {
		return cfg, err
	}
	if !cfg.Indexing.Enabled {
		return cfg, ErrDisabled
	}
	return cfg, nil
}

func (c *Core) state(create bool) (string, error) {
	root, err := security.OpenRoot(c.project)
	if err != nil {
		return "", security.ErrDenied
	}
	defer root.Close()
	if create {
		if err := security.EnsurePrivateState(root); err != nil {
			return "", err
		}
	}
	for _, name := range []string{".conmesh", ".conmesh/state"} {
		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrAbsent
		}
		if err != nil || security.CheckEntry(info) != nil || !info.IsDir() {
			return "", security.ErrDenied
		}
		if runtime.GOOS != "windows" && name == ".conmesh/state" && info.Mode().Perm()&0077 != 0 {
			return "", security.ErrDenied
		}
	}
	for _, name := range []string{"index.sqlite", "index.sqlite-wal", "index.sqlite-shm", "index.sqlite-journal"} {
		rel := ".conmesh/state/" + name
		if err := security.CheckPath(filepath.Join(c.project, filepath.FromSlash(rel))); err != nil {
			return "", err
		}
		info, err := root.Lstat(rel)
		if errors.Is(err, fs.ErrNotExist) {
			if name == "index.sqlite" {
				if !create {
					return "", ErrAbsent
				}
				f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return "", ErrState
				}
				if err := f.Close(); err != nil {
					return "", ErrState
				}
			}
			continue
		}
		if err != nil || security.CheckEntry(info) != nil || !info.Mode().IsRegular() {
			return "", security.ErrDenied
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return "", security.ErrDenied
		}
	}
	return filepath.Join(c.project, ".conmesh", "state", "index.sqlite"), nil
}

func (c *Core) open(ctx context.Context, writer bool) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := c.state(writer)
	if err != nil {
		return nil, err
	}
	uriPath := filepath.ToSlash(name)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{}
	if writer {
		q.Set("mode", "rw")
	} else {
		q.Set("mode", "ro")
	}
	for _, pragma := range []string{"busy_timeout(5000)", "foreign_keys(ON)", "trusted_schema(OFF)"} {
		q.Add("_pragma", pragma)
	}
	if !writer {
		q.Add("_pragma", "query_only(ON)")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, stateError(err)
	}
	db.SetMaxOpenConns(1)
	fail := func(cause error) (*sql.DB, error) {
		db.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, stateError(cause)
	}
	if err := db.PingContext(ctx); err != nil {
		return fail(err)
	}
	if _, err := c.state(false); err != nil {
		db.Close()
		return nil, err
	}
	var userVersion, tables int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return fail(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&tables); err != nil {
		return fail(err)
	}
	empty := tables == 0 && userVersion == 0
	if !empty {
		if userVersion != 1 && userVersion != schemaVersion {
			return fail(nil)
		}
		if err := checkMarker(ctx, db, userVersion); err != nil {
			// An upgrade may commit between the two reads above; a real
			// mismatch is still a mismatch on the second look.
			if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
				return fail(err)
			}
			if userVersion != 1 && userVersion != schemaVersion {
				return fail(nil)
			}
			if err := checkMarker(ctx, db, userVersion); err != nil {
				return fail(err)
			}
		}
	} else if !writer {
		return fail(nil)
	}
	if writer {
		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA secure_delete=ON;"); err != nil {
			return fail(err)
		}
	}
	if writer && empty {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fail(err)
		}
		if _, err := tx.ExecContext(ctx, schema+"PRAGMA user_version="+strconv.Itoa(schemaVersion)+";"); err != nil {
			tx.Rollback()
			return fail(err)
		}
		if err := tx.Commit(); err != nil {
			return fail(err)
		}
		userVersion = schemaVersion
	}
	if writer && userVersion == 1 {
		if err := migrate(ctx, db); err != nil {
			return fail(err)
		}
		userVersion = schemaVersion
	}
	if err := checkMarker(ctx, db, userVersion); err != nil {
		return fail(err)
	}
	return db, nil
}

// checkMarker requires metadata 'schema' to agree with PRAGMA user_version.
func checkMarker(ctx context.Context, db *sql.DB, userVersion int) error {
	var marker string
	if err := db.QueryRowContext(ctx, "SELECT substr(value,1,2) FROM metadata WHERE key='schema'").Scan(&marker); err != nil {
		return err
	}
	if marker != strconv.Itoa(userVersion) {
		return errors.New("schema marker mismatch")
	}
	return nil
}

// migrate upgrades a version 1 index in place without discarding rows: it only
// adds indexes. Stale extraction is handled separately by the extractor version
// in Index, so readers keep working before and after the upgrade.
func migrate(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// BEGIN IMMEDIATE serializes concurrent upgraders; the version is re-read under the lock.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	rollback := func(err error) error {
		conn.ExecContext(context.Background(), "ROLLBACK")
		return err
	}
	var current int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return rollback(err)
	}
	if current == schemaVersion {
		return rollback(nil)
	}
	if current != 1 {
		return rollback(errors.New("unexpected schema version during upgrade"))
	}
	if _, err := conn.ExecContext(ctx, schemaIndexes+"UPDATE metadata SET value='"+strconv.Itoa(schemaVersion)+"' WHERE key='schema'; PRAGMA user_version="+strconv.Itoa(schemaVersion)+";"); err != nil {
		return rollback(err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return rollback(err)
	}
	return nil
}

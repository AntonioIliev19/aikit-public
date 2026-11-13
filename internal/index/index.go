package index

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/parser"
	"github.com/AntonioIliev19/conmesh/internal/repository"
	"github.com/AntonioIliev19/conmesh/internal/security"
)

type Progress struct {
	// Phase and Processed describe live work, without changing the final JSON report.
	Phase                  string `json:"-"`
	Processed              int    `json:"-"`
	SemanticCleanupWarning bool   `json:"semantic_cleanup_warning,omitempty"`
	Scanned                int    `json:"scanned"`
	Indexed                int    `json:"indexed"`
	Unchanged              int    `json:"unchanged"`
	Skipped                int    `json:"skipped"`
	// SkipReasons breaks Skipped down by cause; its fields sum to Skipped.
	SkipReasons SkipReasons `json:"skip_reasons"`
	// Malformed counts files parsed in this run that had syntax errors; they are
	// indexed as plain text (no symbols) and also counted in PlainText.
	Malformed int `json:"malformed"`
	// Limited counts files parsed in this run that exceeded parser limits; they
	// are indexed as plain text when possible (PlainText), otherwise skipped
	// (SkipReasons.ParserLimit). Unchanged files are not re-parsed or re-counted.
	Limited int `json:"limited"`
	// PlainText counts files indexed as plain text in this run after a native
	// parse failure.
	PlainText int `json:"plain_text_fallback"`
	// Refreshed is true when stored entries came from older extraction rules
	// (or an older schema) and every file was re-extracted in this run.
	Refreshed         bool  `json:"refreshed,omitempty"`
	Deleted           int   `json:"deleted"`
	Workers           int   `json:"workers"`
	Queue             int   `json:"queue"`
	MemoryTargetBytes int64 `json:"go_memory_target_bytes"`
	ElapsedMillis     int64 `json:"elapsed_ms"`
}

func Workers(profile string) int {
	fraction, cap := 2, 4
	if profile == "eco" {
		fraction, cap = 4, 2
	} else if profile == "performance" {
		fraction, cap = 1, 8
	}
	n := runtime.NumCPU() / fraction
	if profile == "performance" {
		n = runtime.NumCPU() * 3 / 4
	}
	if n < 1 {
		n = 1
	}
	if n > cap {
		n = cap
	}
	return n
}

// SkipReasons is re-exported so callers of Index need not import repository.
type SkipReasons = repository.SkipReasons

// extractorVersion is stored in metadata 'extractor'. A mismatch means stored
// rows came from different parsing, chunking or FTS term rules, so content
// digests alone cannot prove an entry is current and every file is re-extracted.
const extractorVersion = "parser-" + parser.Version + "/terms-" + termsVersion

// parserAvailable is a seam so plain-text indexing is testable without CGO.
var parserAvailable = parser.Available

type parsed struct {
	name, digest, language string
	result                 parser.Result
	unchanged              bool
	err                    error
}

// Index executes one bounded pipeline and commits all content changes atomically.
// The optional callback runs synchronously on the writer, never on worker goroutines.
func (c *Core) Index(ctx context.Context, progress func(Progress)) (report Progress, err error) {
	callerCtx := ctx
	defer func() {
		if err != nil && callerCtx.Err() != nil {
			err = callerCtx.Err()
		}
	}()
	start := time.Now()
	notify := func(phase string) {
		report.Phase = phase
		report.ElapsedMillis = time.Since(start).Milliseconds()
		if progress != nil {
			progress(report)
		}
	}
	defer func() {
		if err != nil {
			notify("failed")
		}
	}()
	cfg, err := c.enabled()
	if err != nil {
		return report, err
	}
	if !parserAvailable() {
		return report, parser.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	notify("scanning")
	target, release, err := resources(ctx, cfg.Indexing.Profile)
	if err != nil {
		return report, err
	}
	defer release()
	report.MemoryTargetBytes = target
	root, err := security.OpenRoot(c.project)
	if err != nil {
		return report, security.ErrDenied
	}
	defer root.Close()
	snapshot, err := repository.Scan(ctx, root)
	if err != nil {
		return report, err
	}
	report.Scanned, report.Skipped = len(snapshot.Files), snapshot.Skipped
	report.SkipReasons = snapshot.SkipReasons
	notify("processing")
	db, err := c.open(ctx, true)
	if err != nil {
		return report, err
	}
	defer db.Close()
	// BEGIN IMMEDIATE serializes independent writers before reading their baseline.
	conn, err := db.Conn(ctx)
	if err != nil {
		return report, stateError(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return report, stateError(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	var storedExtractor string
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE((SELECT substr(value,1,64) FROM metadata WHERE key='extractor'),'')").Scan(&storedExtractor); err != nil {
		return report, stateError(err)
	}
	// Entries from other extraction rules are re-extracted even if bytes are unchanged.
	refresh := storedExtractor != extractorVersion
	previous := map[string]string{}
	rows, err := conn.QueryContext(ctx, "SELECT path,digest FROM files")
	if err != nil {
		return report, stateError(err)
	}
	for rows.Next() {
		var name, digest string
		if err := rows.Scan(&name, &digest); err != nil {
			rows.Close()
			return report, stateError(err)
		}
		if len(previous) >= repository.MaxFiles {
			rows.Close()
			return report, repository.ErrLimit
		}
		previous[name] = digest
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return report, stateError(readErr)
	}
	report.Refreshed = refresh && len(previous) > 0
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	report.Workers = Workers(cfg.Indexing.Profile)
	report.Queue = report.Workers * 2
	// Reserve headroom for native ASTs and SQLite, outside Go's soft memory limit.
	if n := int(target / (32 << 20)); n < report.Workers {
		report.Workers = max(1, n)
	}
	report.Queue = report.Workers * 2
	jobs := make(chan string, report.Queue)
	results := make(chan parsed, report.Queue)
	var wg sync.WaitGroup
	var bytesRead atomic.Int64
	for i := 0; i < report.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				if ctx.Err() != nil {
					return
				}
				r := parsed{name: name, language: repository.Language(name)}
				info, statErr := root.Lstat(name)
				if statErr == nil && info.Mode().IsRegular() && bytesRead.Add(info.Size()) > repository.MaxSourceBytes {
					select {
					case results <- parsed{err: repository.ErrLimit}:
					case <-ctx.Done():
					}
					return
				}
				data, digest, err := repository.AuthorizedRead(root, name)
				r.digest, r.err = digest, err
				if err == nil {
					if !refresh && previous[name] == digest {
						r.unchanged = true
					} else {
						r.result, r.err = parser.Parse(ctx, r.language, data)
					}
				}
				select {
				case results <- r:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, name := range snapshot.Files {
			select {
			case jobs <- name:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	// seen maps each persisted path to the digest now stored for it.
	seen := map[string]string{}
	var runErr error
	for r := range results {
		if runErr != nil {
			continue
		}
		if errors.Is(r.err, repository.ErrLimit) {
			runErr = r.err
			cancel()
			continue
		}
		report.Processed++
		if r.err != nil {
			report.Skipped++
			if errors.Is(r.err, parser.ErrMalformed) {
				report.Malformed++
			}
			if errors.Is(r.err, parser.ErrLimit) {
				report.Limited++
			}
			if errors.Is(r.err, parser.ErrLimit) || errors.Is(r.err, parser.ErrMalformed) {
				report.SkipReasons.ParserLimit++
			} else {
				report.SkipReasons.CountReadError(r.err)
			}
			// Unreadable, secret or unparseable files lose any previously indexed content.
			notify("processing")
			continue
		}
		// Recheck current ancestor rules and bytes immediately before persistence.
		_, digest, err := repository.AuthorizedRead(root, r.name)
		if err != nil || digest != r.digest {
			report.Skipped++
			report.SkipReasons.Changed++
			notify("processing")
			continue
		}
		seen[r.name] = r.digest
		if r.unchanged {
			report.Unchanged++
		} else {
			if err := replace(ctx, conn, r); err != nil {
				runErr = err
				cancel()
				continue
			}
			report.Indexed++
			switch r.result.Fallback {
			case parser.FallbackMalformed:
				report.Malformed++
				report.PlainText++
			case parser.FallbackLimit:
				report.Limited++
				report.PlainText++
			}
		}
		notify("processing")
	}
	if runErr != nil {
		return report, runErr
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	notify("finalizing")
	// A policy may change after an earlier result was written in this transaction.
	for name, indexedDigest := range seen {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		// The stored digest is already known: unchanged rows kept previous[name],
		// which equalled r.digest, and replaced rows were written with r.digest.
		_, digest, err := repository.AuthorizedRead(root, name)
		if err != nil || digest != indexedDigest {
			delete(seen, name)
			if err := remove(ctx, conn, name); err != nil {
				return report, err
			}
		}
	}
	for name := range previous {
		if _, ok := seen[name]; !ok {
			if err := remove(ctx, conn, name); err != nil {
				return report, err
			}
			report.Deleted++
		}
	}
	if _, err := conn.ExecContext(ctx, "INSERT OR REPLACE INTO metadata VALUES ('indexed_at', ?), ('extractor', ?)", time.Now().UTC().Format(time.RFC3339Nano), extractorVersion); err != nil {
		return report, stateError(err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return report, stateError(err)
	}
	// Release the sole base connection before opening a bounded cache reconciler.
	conn.Close()
	if err := c.pruneSemantic(ctx); err != nil {
		report.SemanticCleanupWarning = true
	}
	notify("done")
	return report, nil
}

func remove(ctx context.Context, conn *sql.Conn, name string) error {
	if _, err := conn.ExecContext(ctx, "DELETE FROM fts_chunks WHERE rowid IN (SELECT chunks.id FROM chunks JOIN files ON files.id=chunks.file_id WHERE files.path=?)", name); err != nil {
		return stateError(err)
	}
	if _, err := conn.ExecContext(ctx, "DELETE FROM files WHERE path=?", name); err != nil {
		return stateError(err)
	}
	return nil
}

func replace(ctx context.Context, conn *sql.Conn, r parsed) error {
	if err := remove(ctx, conn, r.name); err != nil {
		return err
	}
	res, err := conn.ExecContext(ctx, "INSERT INTO files(path,digest,language) VALUES (?,?,?)", r.name, r.digest, r.language)
	if err != nil {
		return stateError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return stateError(err)
	}
	for _, symbol := range r.result.Symbols {
		if _, err := conn.ExecContext(ctx, "INSERT INTO symbols(file_id,name,kind,start_line,end_line) VALUES (?,?,?,?,?)", id, symbol.Name, symbol.Kind, symbol.StartLine, symbol.EndLine); err != nil {
			return stateError(err)
		}
	}
	for _, chunk := range r.result.Chunks {
		res, err := conn.ExecContext(ctx, "INSERT INTO chunks(file_id,text,start_line,end_line,digest) VALUES (?,?,?,?,?)", id, chunk.Text, chunk.StartLine, chunk.EndLine, repository.Digest([]byte(chunk.Text)))
		if err != nil {
			return stateError(err)
		}
		chunkID, err := res.LastInsertId()
		if err != nil {
			return stateError(err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO fts_chunks(rowid,text) VALUES (?,?)", chunkID, ftsText(chunk.Text)); err != nil {
			return stateError(err)
		}
	}
	return nil
}

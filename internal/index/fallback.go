package index

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/AntonioIliev19/aikit-public/internal/repository"
	"github.com/AntonioIliev19/aikit-public/internal/security"
)

var ErrFallback = errors.New("FALLBACK_UNAVAILABLE: explicit indexed miss and fresh bounded source set required; ripgrep must be installed")

type FallbackResult struct {
	Evidence []Evidence `json:"evidence"`
	Used     bool       `json:"fallback_used"`
	Reason   string     `json:"reason,omitempty"`
	Counter  int64      `json:"counter,omitempty"`
}

func (c *Core) FindReferenceCandidates(ctx context.Context, q Query) ([]Evidence, error) {
	if len(q.Text) > 128 {
		return nil, ErrQuery
	}
	for _, r := range q.Text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '$' {
			return nil, ErrQuery
		}
	}
	out, err := c.Search(ctx, q)
	for i := range out {
		out[i].Mechanism = "lexical_reference_candidate_no_lsp"
	}
	return out, err
}

// Fallback is CLI-only: it audits a fixed safe reason, never the query/source.
// The subprocess receives authorized bytes on stdin, never repository paths.
func (c *Core) Fallback(ctx context.Context, q Query) (FallbackResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := FallbackResult{Evidence: []Evidence{}}
	q, err := validate(q)
	if err != nil {
		return out, err
	}
	symbols, err := c.FindSymbol(ctx, q)
	if err != nil {
		return out, err
	}
	lexical, err := c.Search(ctx, q)
	if err != nil {
		return out, err
	}
	if len(symbols)+len(lexical) > 0 {
		out.Evidence = fuse(q.Limit, symbols, lexical)
		return out, nil
	}
	fields := strings.FieldsFunc(q.Text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	for _, field := range fields[:min(4, len(fields))] {
		broad := q
		broad.Text = field
		found, err := c.Search(ctx, broad)
		if err != nil {
			return out, err
		}
		if len(found) > 0 {
			out.Evidence = found
			return out, nil
		}
	}
	if _, err := c.enabled(); err != nil {
		return out, err
	}
	db, err := c.open(ctx, false)
	if err != nil {
		return out, err
	}
	rows, err := db.QueryContext(ctx, "SELECT substr(path,1,4097),substr(digest,1,65) FROM files WHERE (?='' OR path=? OR substr(path,1,length(?)+1)=?||'/') LIMIT ?", q.Scope, q.Scope, q.Scope, q.Scope, MaxSemanticCandidates+1)
	if err != nil {
		db.Close()
		return out, ErrState
	}
	previous := map[string]string{}
	for rows.Next() {
		var name, digest string
		if rows.Scan(&name, &digest) != nil || len(name) > 4096 || len(digest) != 64 {
			rows.Close()
			db.Close()
			return out, ErrState
		}
		if len(previous) >= MaxSemanticCandidates {
			rows.Close()
			db.Close()
			return out, repository.ErrLimit
		}
		previous[name] = digest
	}
	readErr := rows.Err()
	rows.Close()
	db.Close()
	if readErr != nil {
		return out, ErrState
	}
	root, err := security.OpenRoot(c.project)
	if err != nil {
		return out, security.ErrDenied
	}
	defer root.Close()
	snapshot, err := repository.Scan(ctx, root)
	if err != nil {
		return out, err
	}
	type source struct{ name, digest string }
	sources := []source{}
	seen := map[string]bool{}
	total := 0
	for _, name := range snapshot.Files {
		if !repository.InScope(name, q.Scope) {
			continue
		}
		data, digest, err := repository.AuthorizedRead(root, name)
		if errors.Is(err, security.ErrDenied) {
			continue
		}
		if err != nil {
			return out, err
		}
		if previous[name] != digest {
			return out, repository.ErrStale
		}
		seen[name] = true
		total += len(data)
		if total > 16<<20 || len(sources) >= MaxSemanticCandidates {
			return out, repository.ErrLimit
		}
		sources = append(sources, source{name, digest})
	}
	if len(seen) != len(previous) {
		return out, repository.ErrStale
	}
	executable, err := exec.LookPath("rg")
	if err != nil {
		return out, ErrFallback
	}
	writer, err := c.open(ctx, true)
	if err != nil {
		return out, err
	}
	defer writer.Close()
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		return out, ErrState
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('fallback_count','1') ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1`); err != nil {
		tx.Rollback()
		return out, ErrState
	}
	if tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='fallback_count'").Scan(&out.Counter) != nil {
		tx.Rollback()
		return out, ErrState
	}
	for key, value := range map[string]string{"fallback_reason": "fresh_index_systematic_miss", "fallback_at": time.Now().UTC().Format(time.RFC3339Nano)} {
		if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO metadata VALUES(?,?)", key, value); err != nil {
			tx.Rollback()
			return out, ErrState
		}
	}
	if tx.Commit() != nil {
		return out, ErrState
	}
	out.Used = true
	out.Reason = "fresh_index_systematic_miss"
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return FallbackResult{}, err
		}
		data, current, err := repository.AuthorizedRead(root, source.name)
		if err != nil {
			return FallbackResult{}, err
		}
		if current != source.digest {
			return FallbackResult{}, repository.ErrStale
		}
		// Count-only output prevents raw source reaching logs or a subprocess error.
		matched, err := runRipgrep(ctx, executable, q.Text, data)
		if err != nil {
			return FallbackResult{}, err
		}
		if !matched {
			continue
		}
		// Return bounded line evidence from the same authorized bytes, not rg output.
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, q.Text) {
				continue
			}
			e := Evidence{Path: source.name, Digest: source.digest, StartLine: i + 1, EndLine: i + 1, Snippet: line, Mechanism: "ripgrep_literal_fallback"}
			if len(e.Snippet) > MaxSnippetBytes {
				e.Snippet = strings.ToValidUTF8(e.Snippet[:MaxSnippetBytes], "")
				e.Truncated = true
			}
			out.Evidence = append(out.Evidence, e)
			if len(out.Evidence) == q.Limit {
				return c.finishFallback(ctx, out)
			}
		}
	}
	return c.finishFallback(ctx, out)
}
func (c *Core) finishFallback(ctx context.Context, out FallbackResult) (FallbackResult, error) {
	if err := c.checkEvidence(ctx, out.Evidence); err != nil {
		return FallbackResult{}, err
	}
	return out, nil
}
func runRipgrep(ctx context.Context, executable, query string, data []byte) (bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, "--no-config", "--fixed-strings", "--count-matches", "--color", "never", "--", query)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stderr = io.Discard
	var stdout cappedOutput
	cmd.Stdout = &stdout
	err := cmd.Run()
	if runCtx.Err() != nil {
		return false, runCtx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, ErrFallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err != nil || n < 1 {
		return false, ErrFallback
	}
	return true, nil
}

type cappedOutput struct{ bytes.Buffer }

func (b *cappedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64 {
		return 0, ErrFallback
	}
	return b.Buffer.Write(p)
}

package index

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/AntonioIliev19/conmesh/internal/parser"
	"github.com/AntonioIliev19/conmesh/internal/repository"
	"github.com/AntonioIliev19/conmesh/internal/security"
)

const MaxResults = 50
const MaxSnippetBytes = 4096

var ErrQuery = errors.New("invalid bounded index query or scope")

type Query struct {
	Text  string
	Scope string
	Limit int
}
type Evidence struct {
	Digest    string `json:"digest,omitempty"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Snippet   string `json:"snippet"`
	Mechanism string `json:"mechanism"`
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func validate(q Query) (Query, error) {
	if len(q.Text) == 0 || len(q.Text) > 256 || repository.ValidateScope(q.Scope) != nil {
		return q, ErrQuery
	}
	for _, r := range q.Text {
		if unicode.IsControl(r) {
			return q, ErrQuery
		}
	}
	if q.Limit == 0 {
		q.Limit = 10
	}
	if q.Limit < 1 || q.Limit > MaxResults {
		return q, ErrQuery
	}
	return q, nil
}

func (c *Core) Search(ctx context.Context, q Query) ([]Evidence, error) {
	return c.query(ctx, q, false)
}
func (c *Core) FindSymbol(ctx context.Context, q Query) ([]Evidence, error) {
	return c.query(ctx, q, true)
}

func (c *Core) query(ctx context.Context, q Query, symbol bool) ([]Evidence, error) {
	q, err := validate(q)
	if err != nil {
		return nil, err
	}
	if _, err := c.enabled(); err != nil {
		return nil, err
	}
	db, err := c.open(ctx, false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	root, err := security.OpenRoot(c.project)
	if err != nil {
		return nil, security.ErrDenied
	}
	defer root.Close()
	var sqlText, term, mechanism string
	if symbol {
		term, mechanism = q.Text, "symbol"
		sqlText = `SELECT substr(files.path,1,4097),substr(files.digest,1,65),symbols.start_line,symbols.end_line,symbols.name,substr(symbols.kind,1,65) FROM symbols JOIN files ON files.id=symbols.file_id WHERE symbols.name=? AND (?='' OR files.path=? OR substr(files.path,1,length(?)+1)=?||'/') ORDER BY files.path,symbols.start_line,symbols.end_line,symbols.kind LIMIT ?`
	} else {
		fields := strings.FieldsFunc(q.Text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' })
		if len(fields) == 0 || len(fields) > 16 {
			return nil, ErrQuery
		}
		for i := range fields {
			fields[i] = `"` + fields[i] + `"`
		}
		term, mechanism = strings.Join(fields, " AND "), "fts5"
		sqlText = `SELECT substr(files.path,1,4097),substr(files.digest,1,65),chunks.start_line,chunks.end_line,'','' FROM fts_chunks JOIN chunks ON chunks.id=fts_chunks.rowid JOIN files ON files.id=chunks.file_id WHERE fts_chunks MATCH ? AND (?='' OR files.path=? OR substr(files.path,1,length(?)+1)=?||'/') ORDER BY bm25(fts_chunks),files.path,chunks.start_line,chunks.end_line LIMIT ?`
	}
	rows, err := db.QueryContext(ctx, sqlText, term, q.Scope, q.Scope, q.Scope, q.Scope, q.Limit)
	if err != nil {
		return nil, ErrState
	}
	defer rows.Close()
	result := []Evidence{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var e Evidence
		var digest string
		if err := rows.Scan(&e.Path, &digest, &e.StartLine, &e.EndLine, &e.Name, &e.Kind); err != nil {
			return nil, ErrState
		}
		if len(e.Path) > 4096 || len(digest) != 64 || (symbol && !parser.SymbolKind(e.Kind)) {
			return nil, ErrState
		}
		data, current, err := repository.AuthorizedRead(root, e.Path)
		if err != nil || current != digest {
			return nil, repository.ErrStale
		}
		e.Digest = digest
		lines := strings.Split(string(data), "\n")
		if e.StartLine < 1 || e.EndLine < e.StartLine || e.EndLine > len(lines) {
			return nil, ErrState
		}
		e.Snippet = strings.Join(lines[e.StartLine-1:e.EndLine], "\n")
		if len(e.Snippet) > MaxSnippetBytes {
			e.Snippet = strings.ToValidUTF8(e.Snippet[:MaxSnippetBytes], "")
			e.Truncated = true
		}
		if security.SensitiveContent([]byte(e.Snippet + e.Name + e.Kind)) {
			return nil, security.ErrDenied
		}
		e.Mechanism = mechanism
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrState
	}
	return result, nil
}

type Status struct {
	Enabled      bool   `json:"enabled"`
	NativeParser bool   `json:"native_parser"`
	Present      bool   `json:"present"`
	Files        int    `json:"files"`
	Chunks       int    `json:"chunks"`
	Symbols      int    `json:"symbols"`
	IndexedAt    string `json:"indexed_at,omitempty"`
}

// Status never creates state or enables indexing; counts describe the last commit.
func (c *Core) Status(ctx context.Context) (Status, error) {
	status := Status{NativeParser: parser.Available()}
	_, err := c.enabled()
	if errors.Is(err, ErrDisabled) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	status.Enabled = true
	db, err := c.open(ctx, false)
	if errors.Is(err, ErrAbsent) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	defer db.Close()
	status.Present = true
	if err := db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM files),(SELECT count(*) FROM chunks),(SELECT count(*) FROM symbols),COALESCE((SELECT substr(value,1,36) FROM metadata WHERE key='indexed_at'),'')").Scan(&status.Files, &status.Chunks, &status.Symbols, &status.IndexedAt); err != nil {
		return Status{}, ErrState
	}
	if len(status.IndexedAt) > 35 {
		return Status{}, ErrState
	}
	if status.IndexedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, status.IndexedAt); err != nil {
			return Status{}, ErrState
		}
	}
	return status, nil
}

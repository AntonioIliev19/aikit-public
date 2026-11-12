package index

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io/fs"
	"math"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/AntonioIliev19/aikit-public/internal/embeddings"
	"github.com/AntonioIliev19/aikit-public/internal/parser"
	"github.com/AntonioIliev19/aikit-public/internal/repository"
	"github.com/AntonioIliev19/aikit-public/internal/security"
)

const MaxSemanticCandidates = 4096
const maxCachedVectors = 20000

type semanticChunk struct {
	Evidence
	digest     string
	text       string
	fileDigest string
}
type SemanticProgress struct {
	Chunks   int `json:"chunks"`
	Embedded int `json:"embedded"`
	Reused   int `json:"reused"`
}
type HybridResult struct {
	Evidence         []Evidence `json:"evidence"`
	CandidateLimited bool       `json:"candidate_limited"`
	Candidates       int        `json:"candidates"`
}

func (c *Core) semanticClient() (*embeddings.Client, string, error) {
	cfg, err := c.enabled()
	if err != nil {
		return nil, "", err
	}
	if !cfg.Indexing.Semantic || cfg.Semantic == nil {
		return nil, "", embeddings.ErrUnavailable
	}
	client, err := embeddings.New(*cfg.Semantic)
	return client, cfg.Semantic.Identity(), err
}

func (c *Core) semanticDB(ctx context.Context, writer bool) (*sql.DB, error) {
	root, err := security.OpenRoot(c.project)
	if err != nil {
		return nil, security.ErrDenied
	}
	defer root.Close()
	name, err := security.PrivateFile(root, "semantic.sqlite", writer)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, embeddings.ErrUnavailable
	}
	if err != nil {
		return nil, err
	}
	p := filepath.ToSlash(name)
	if runtime.GOOS == "windows" {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{}
	mode := "ro"
	if writer {
		mode = "rw"
	}
	q.Set("mode", mode)
	for _, pragma := range []string{"busy_timeout(5000)", "trusted_schema(OFF)"} {
		q.Add("_pragma", pragma)
	}
	if !writer {
		q.Add("_pragma", "query_only(ON)")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, ErrState
	}
	db.SetMaxOpenConns(1)
	fail := func() (*sql.DB, error) {
		db.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrState
	}
	if db.PingContext(ctx) != nil {
		return fail()
	}
	if _, err := security.PrivateFile(root, "semantic.sqlite", false); err != nil {
		db.Close()
		return nil, err
	}
	var version, tables int
	if db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil || db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&tables) != nil {
		return fail()
	}
	if version == 0 && tables == 0 && writer {
		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA secure_delete=ON; BEGIN IMMEDIATE; CREATE TABLE vectors(model TEXT NOT NULL,digest TEXT NOT NULL,vector BLOB NOT NULL,PRIMARY KEY(model,digest)); PRAGMA user_version=1; COMMIT;"); err != nil {
			return fail()
		}
	} else if version != 1 {
		return fail()
	}
	return db, nil
}

// semanticChunks verifies both live source digests and indexed chunk bytes before
// those bytes may cross the trusted local-runtime boundary.
func (c *Core) semanticChunks(ctx context.Context, scope string) ([]semanticChunk, bool, error) {
	if repository.ValidateScope(scope) != nil {
		return nil, false, ErrQuery
	}
	db, err := c.open(ctx, false)
	if err != nil {
		return nil, false, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT substr(files.path,1,4097),substr(files.digest,1,65),chunks.start_line,chunks.end_line,substr(chunks.digest,1,65),substr(chunks.text,1,16385) FROM chunks JOIN files ON files.id=chunks.file_id WHERE (?='' OR files.path=? OR substr(files.path,1,length(?)+1)=?||'/') ORDER BY files.path,chunks.start_line,chunks.end_line,chunks.id LIMIT ?`, scope, scope, scope, scope, MaxSemanticCandidates+1)
	if err != nil {
		return nil, false, ErrState
	}
	defer rows.Close()
	root, err := security.OpenRoot(c.project)
	if err != nil {
		return nil, false, security.ErrDenied
	}
	defer root.Close()
	out := []semanticChunk{}
	for rows.Next() {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		if len(out) == MaxSemanticCandidates {
			return out, true, nil
		}
		var v semanticChunk
		var fileDigest string
		if rows.Scan(&v.Path, &fileDigest, &v.StartLine, &v.EndLine, &v.digest, &v.text) != nil {
			return nil, false, ErrState
		}
		if len(v.Path) > 4096 || len(fileDigest) != 64 || len(v.digest) != 64 || len(v.text) > parser.MaxChunkBytes || v.digest != repository.Digest([]byte(v.text)) {
			return nil, false, ErrState
		}
		data, current, err := repository.AuthorizedRead(root, v.Path)
		if err != nil {
			return nil, false, err
		}
		if current != fileDigest {
			return nil, false, repository.ErrStale
		}
		lines := strings.Split(string(data), "\n")
		if v.StartLine < 1 || v.EndLine < v.StartLine || v.EndLine > len(lines) {
			return nil, false, ErrState
		}
		envelope := strings.Join(lines[v.StartLine-1:v.EndLine], "\n")
		if !strings.Contains(envelope+"\n", v.text) || security.SensitiveContent([]byte(v.text)) {
			return nil, false, security.ErrDenied
		}
		v.fileDigest = fileDigest
		v.Digest = fileDigest
		v.Snippet = envelope
		if len(v.Snippet) > MaxSnippetBytes {
			v.Snippet = strings.ToValidUTF8(v.Snippet[:MaxSnippetBytes], "")
			v.Truncated = true
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, ErrState
	}
	return out, false, nil
}
func encodeVector(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(x))
	}
	return b
}
func decodeVector(b []byte) ([]float32, error) {
	if len(b) != embeddings.Dimensions*4 {
		return nil, ErrState
	}
	v := make([]float32, embeddings.Dimensions)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	if !embeddings.ValidVector(v) {
		return nil, ErrState
	}
	return v, nil
}
func cached(ctx context.Context, db *sql.DB, model, digest string) ([]float32, error) {
	var b []byte
	err := db.QueryRowContext(ctx, "SELECT substr(vector,1,3073) FROM vectors WHERE model=? AND digest=?", model, digest).Scan(&b)
	if err != nil {
		return nil, err
	}
	return decodeVector(b)
}

// IndexSemantic is a separately selected write operation after lexical indexing.
// The existing base index schema and read-only basic search remain unchanged.
func (c *Core) IndexSemantic(ctx context.Context, scope string) (SemanticProgress, error) {
	report := SemanticProgress{}
	client, model, err := c.semanticClient()
	if err != nil {
		return report, err
	}
	chunks, limited, err := c.semanticChunks(ctx, scope)
	if err != nil {
		return report, err
	}
	if limited {
		return report, repository.ErrLimit
	}
	report.Chunks = len(chunks)
	db, err := c.semanticDB(ctx, true)
	if err != nil {
		return report, err
	}
	defer db.Close()
	var count int
	if db.QueryRowContext(ctx, "SELECT count(*) FROM vectors").Scan(&count) != nil || count > maxCachedVectors {
		return report, ErrState
	}
	// Only vectors survive cache writes. Reads always join current authorized chunks.
	if _, err := db.ExecContext(ctx, "DELETE FROM vectors WHERE model<>?", model); err != nil {
		return report, ErrState
	}
	if db.QueryRowContext(ctx, "SELECT count(*) FROM vectors").Scan(&count) != nil {
		return report, ErrState
	}
	pending := []semanticChunk{}
	seen := map[string]bool{}
	for _, chunk := range chunks {
		if seen[chunk.digest] {
			continue
		}
		seen[chunk.digest] = true
		_, err := cached(ctx, db, model, chunk.digest)
		if err == nil {
			report.Reused++
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return report, ErrState
		}
		pending = append(pending, chunk)
	}
	if count+len(pending) > maxCachedVectors {
		return report, repository.ErrLimit
	}
	for start := 0; start < len(pending); start += embeddings.MaxBatch {
		end := min(start+embeddings.MaxBatch, len(pending))
		texts := make([]string, end-start)
		for i := start; i < end; i++ {
			texts[i-start] = pending[i].text
		}
		check := func() error {
			root, err := security.OpenRoot(c.project)
			if err != nil {
				return security.ErrDenied
			}
			defer root.Close()
			for _, chunk := range pending[start:end] {
				_, digest, err := repository.AuthorizedRead(root, chunk.Path)
				if err != nil {
					return err
				}
				if digest != chunk.fileDigest {
					return repository.ErrStale
				}
			}
			return nil
		}
		vectors, err := client.EmbedChecked(ctx, texts, false, check)
		if err != nil {
			return report, err
		}
		// Re-read source/policy after inference and before persisting the batch.
		root, err := security.OpenRoot(c.project)
		if err != nil {
			return report, security.ErrDenied
		}
		for _, chunk := range pending[start:end] {
			_, current, err := repository.AuthorizedRead(root, chunk.Path)
			if err != nil || current != chunk.fileDigest {
				root.Close()
				return report, repository.ErrStale
			}
		}
		root.Close()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return report, ErrState
		}
		for i, v := range vectors {
			if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO vectors(model,digest,vector) VALUES(?,?,?)", model, pending[start+i].digest, encodeVector(v)); err != nil {
				break
			}
		}
		if err != nil {
			tx.Rollback()
			return report, ErrState
		}
		if tx.Commit() != nil {
			return report, ErrState
		}
		report.Embedded += len(vectors)
	}
	return report, nil
}

func (c *Core) Hybrid(ctx context.Context, q Query) (HybridResult, error) {
	result := HybridResult{}
	q, err := validate(q)
	if err != nil {
		return result, err
	}
	client, model, err := c.semanticClient()
	if err != nil {
		return result, err
	}
	lexical, err := c.Search(ctx, q)
	if err != nil {
		return result, err
	}
	symbols, err := c.FindSymbol(ctx, q)
	if err != nil {
		return result, err
	}
	chunks, limited, err := c.semanticChunks(ctx, q.Scope)
	if err != nil {
		return result, err
	}
	result.CandidateLimited = limited
	result.Candidates = len(chunks)
	db, err := c.semanticDB(ctx, false)
	if err != nil {
		return result, err
	}
	defer db.Close()
	type scored struct {
		e     Evidence
		score float64
	}
	semantic := []scored{}
	vectors := make([][]float32, len(chunks))
	for i, chunk := range chunks {
		v, err := cached(ctx, db, model, chunk.digest)
		if errors.Is(err, sql.ErrNoRows) {
			return result, embeddings.ErrUnavailable
		}
		if err != nil {
			return result, ErrState
		}
		vectors[i] = v
	}
	queryVectors, err := client.Embed(ctx, []string{q.Text}, true)
	if err != nil {
		return result, err
	}
	for i, chunk := range chunks {
		chunk.Mechanism = "semantic"
		semantic = append(semantic, scored{chunk.Evidence, embeddings.Cosine(queryVectors[0], vectors[i])})
	}
	sort.SliceStable(semantic, func(i, j int) bool { return semantic[i].score > semantic[j].score })
	sem := []Evidence{}
	for _, v := range semantic {
		sem = append(sem, v.e)
	}
	result.Evidence = fuse(q.Limit, symbols, lexical, sem)
	if err := c.checkEvidence(ctx, result.Evidence); err != nil {
		return HybridResult{}, err
	}
	return result, nil
}
func stringKey(n int) string { return strconv.Itoa(n) }
func fuse(limit int, lists ...[]Evidence) []Evidence {
	type ranked struct {
		e     Evidence
		score float64
	}
	entries := map[string]*ranked{}
	for _, list := range lists {
		seen := map[string]bool{}
		for i, e := range list {
			key := e.Path + ":" + stringKey(e.StartLine) + ":" + stringKey(e.EndLine)
			if seen[key] {
				continue
			}
			seen[key] = true
			item := entries[key]
			if item == nil {
				e.Mechanism = "hybrid_rrf"
				item = &ranked{e: e}
				entries[key] = item
			}
			item.score += 1 / float64(60+i+1)
		}
	}
	out := make([]*ranked, 0, len(entries))
	for _, v := range entries {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.e.Path != b.e.Path {
			return a.e.Path < b.e.Path
		}
		if a.e.StartLine != b.e.StartLine {
			return a.e.StartLine < b.e.StartLine
		}
		return a.e.EndLine < b.e.EndLine
	})
	evidence := []Evidence{}
	for _, v := range out[:min(limit, len(out))] {
		evidence = append(evidence, v.e)
	}
	return evidence
}

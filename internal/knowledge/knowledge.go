// Package knowledge validates proposed repository knowledge and local review receipts.
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/repository"
	"github.com/AntonioIliev19/conmesh/internal/security"
)

const ManifestPath = ".conmesh/knowledge/manifest.yaml"
const MaxManifestBytes = 128 << 10
const MaxDocumentBytes = 16 << 10
const MaxEntries = 128

var ErrManifest = errors.New("KNOWLEDGE_INVALID: invalid bounded knowledge manifest or query")
var ErrReview = errors.New("KNOWLEDGE_REVIEW: explicit review of the current entry, document and citations required")
var ErrAbsent = errors.New("KNOWLEDGE_ABSENT: no scoped knowledge manifest")

type Source struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Digest    string `json:"digest"`
}
type Entry struct {
	ID         string   `json:"id"`
	File       string   `json:"file"`
	Digest     string   `json:"digest"`
	Summary    string   `json:"summary"`
	Kind       string   `json:"kind"`
	Category   string   `json:"category"`
	Status     string   `json:"status"`
	Scopes     []string `json:"scopes"`
	Tags       []string `json:"tags"`
	Languages  []string `json:"languages,omitempty"`
	Frameworks []string `json:"frameworks,omitempty"`
	Sources    []Source `json:"sources"`
}
type Manifest struct {
	Schema  int     `json:"schema_version"`
	Entries []Entry `json:"entries"`
}
type Query struct {
	Scopes []string `json:"scopes,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Kind   string   `json:"kind,omitempty"`
	Limit  int      `json:"limit,omitempty"`
}
type Summary struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Kind     string `json:"kind"`
	Category string `json:"category"`
	Status   string `json:"status"`
}
type Document struct {
	Entry           Entry  `json:"entry"`
	Body            string `json:"body"`
	ReviewDigest    string `json:"review_digest"`
	EffectiveStatus string `json:"effective_status"`
}
type Store struct {
	Project  string
	trustDir string
}

func textOK(s string, n int) bool {
	if len(s) == 0 || len(s) > n || !utf8.ValidString(s) || security.SensitiveContent([]byte(s)) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func DigestOK(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// kindOK accepts guidance kinds and "agent", a project agent definition that
// sync publishes as a custom agent.
func kindOK(s string) bool {
	return s == "architecture" || s == "pattern" || s == "example" || s == "agent"
}
func scopeOK(s string) bool {
	s = strings.TrimSuffix(s, "/**")
	return len(s) <= 4096 && security.ValidateRelative(s) == nil && !security.HardDenied(s, false)
}
func listOK(items []string) bool {
	if len(items) > 16 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range items {
		if !textOK(s, 64) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func (e Entry) validate() error {
	if security.ValidateID(e.ID) != nil || security.ValidateRelative(e.File) != nil || !strings.HasSuffix(e.File, ".md") || len(e.File) > 256 || !DigestOK(e.Digest) || !textOK(e.Summary, 512) || !kindOK(e.Kind) || (e.Category != "observed" && e.Category != "normative") || (e.Status != "proposed" && e.Status != "approved") || len(e.Scopes) == 0 || len(e.Scopes) > 16 || !listOK(e.Tags) || !listOK(e.Languages) || !listOK(e.Frameworks) || len(e.Sources) == 0 || len(e.Sources) > 16 {
		return ErrManifest
	}
	if security.HardDenied(".conmesh/knowledge/"+e.File, false) {
		return security.ErrDenied
	}
	for _, scope := range e.Scopes {
		if !scopeOK(scope) {
			return ErrManifest
		}
	}
	for _, s := range e.Sources {
		if len(s.Path) > 4096 || security.HardDenied(s.Path, false) || s.StartLine < 1 || s.EndLine < s.StartLine || s.EndLine-s.StartLine > 200 || !DigestOK(s.Digest) {
			return ErrManifest
		}
	}
	return nil
}
func (s Store) load(ctx context.Context) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	root, err := security.OpenRoot(s.Project)
	if err != nil {
		return Manifest{}, security.ErrDenied
	}
	defer root.Close()
	if _, err := root.Lstat(ManifestPath); errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, ErrAbsent
	}
	data, _, err := repository.AuthorizedRead(root, ManifestPath)
	if err != nil {
		return Manifest{}, err
	}
	if len(data) > MaxManifestBytes {
		return Manifest{}, ErrManifest
	}
	var m Manifest
	if config.Decode(data, &m) != nil || m.Schema != 1 || len(m.Entries) > MaxEntries {
		return Manifest{}, ErrManifest
	}
	seen := map[string]bool{}
	files := map[string]bool{}
	for _, e := range m.Entries {
		if err := e.validate(); err != nil {
			return Manifest{}, err
		}
		if seen[e.ID] || files[e.File] {
			return Manifest{}, ErrManifest
		}
		seen[e.ID] = true
		files[e.File] = true
	}
	return m, nil
}
func (s Store) List(ctx context.Context, q Query) ([]Summary, error) {
	if len(q.Scopes) > 16 || !listOK(q.Tags) || (q.Kind != "" && !kindOK(q.Kind)) {
		return nil, ErrManifest
	}
	for _, scope := range q.Scopes {
		if !scopeOK(scope) {
			return nil, ErrManifest
		}
	}
	if q.Limit == 0 {
		q.Limit = 10
	}
	if q.Limit < 1 || q.Limit > 50 {
		return nil, ErrManifest
	}
	m, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	root, err := security.OpenRoot(s.Project)
	if err != nil {
		return nil, security.ErrDenied
	}
	defer root.Close()
	for _, e := range m.Entries {
		if q.Kind != "" && q.Kind != e.Kind {
			continue
		}
		match := false
		for _, requested := range q.Scopes {
			for _, scope := range e.Scopes {
				prefix := strings.TrimSuffix(scope, "/**")
				if requested == prefix || strings.HasSuffix(scope, "/**") && repository.InScope(requested, prefix) {
					match = true
				}
			}
		}
		for _, tag := range q.Tags {
			for _, own := range e.Tags {
				if tag == own {
					match = true
				}
			}
		}
		if !match {
			continue
		}
		if repository.AuthorizePath(root, ".conmesh/knowledge/"+e.File) != nil {
			continue
		}
		allowed := true
		for _, source := range e.Sources {
			if repository.AuthorizePath(root, source.Path) != nil {
				allowed = false
				break
			}
		}
		if !allowed {
			continue
		}
		// Listing is deliberately metadata-only; authority is checked on lazy Read.
		out = append(out, Summary{e.ID, e.Summary, e.Kind, e.Category, "proposed_until_loaded_and_verified"})
		if len(out) == q.Limit {
			break
		}
	}
	return out, nil
}
func (s Store) Read(ctx context.Context, id string) (Document, error) {
	if security.ValidateID(id) != nil {
		return Document{}, ErrManifest
	}
	m, err := s.load(ctx)
	if err != nil {
		return Document{}, err
	}
	var e Entry
	for _, item := range m.Entries {
		if item.ID == id {
			e = item
			break
		}
	}
	if e.ID == "" {
		return Document{}, ErrAbsent
	}
	root, err := security.OpenRoot(s.Project)
	if err != nil {
		return Document{}, security.ErrDenied
	}
	defer root.Close()
	data, digest, err := repository.AuthorizedRead(root, ".conmesh/knowledge/"+e.File)
	if err != nil {
		return Document{}, err
	}
	if len(data) > MaxDocumentBytes {
		return Document{}, ErrManifest
	}
	if digest != e.Digest {
		return Document{}, repository.ErrStale
	}
	for _, source := range e.Sources {
		if err := ctx.Err(); err != nil {
			return Document{}, err
		}
		code, current, err := repository.AuthorizedRead(root, source.Path)
		if err != nil {
			return Document{}, err
		}
		if current != source.Digest || source.EndLine > len(strings.Split(string(code), "\n")) {
			return Document{}, repository.ErrStale
		}
	}
	encoded, _ := json.Marshal(e)
	token := repository.Digest(encoded)
	out := Document{e, string(data), token, "proposed"}
	trust, project, err := s.trust(false)
	if err == nil {
		defer trust.Close()
		name := project + "-" + token + ".json"
		info, statErr := trust.Lstat(name)
		if errors.Is(statErr, fs.ErrNotExist) {
			return out, nil
		}
		if statErr != nil || security.CheckEntry(info) != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return Document{}, security.ErrDenied
		}
		receipt, err := security.ReadFileBounded(trust, name, 512)
		if err != nil {
			return Document{}, err
		}
		var r approval
		if config.Decode(receipt, &r) != nil || r.Schema != 1 || r.ID != id || r.Digest != token || r.Project != project {
			return Document{}, ErrReview
		}
		out.EffectiveStatus = "approved"
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Document{}, err
	}
	return out, nil
}

type approval struct {
	Schema  int    `json:"schema_version"`
	ID      string `json:"id"`
	Digest  string `json:"review_digest"`
	Project string `json:"project"`
}

func (s Store) trust(create bool) (*os.Root, string, error) {
	if security.CheckPath(s.Project) != nil {
		return nil, "", security.ErrDenied
	}
	project, err := filepath.EvalSymlinks(s.Project)
	if err != nil {
		return nil, "", security.ErrDenied
	}
	project = filepath.Clean(project)
	if runtime.GOOS == "windows" {
		project = strings.ToLower(project)
	}
	key := repository.Digest([]byte(filepath.ToSlash(project)))
	dir := s.trustDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, "", security.ErrDenied
		}
		dir = filepath.Join(base, "conmesh", "knowledge-approvals")
	}
	abs, err := canonicalPrefix(dir)
	if err != nil || !outsideProject(project, abs) {
		return nil, "", security.ErrDenied
	}
	if create {
		if security.EnsurePrivateDirectory(abs) != nil {
			return nil, "", security.ErrDenied
		}
	}
	root, err := security.OpenRoot(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", fs.ErrNotExist
		}
		return nil, "", security.ErrDenied
	}
	info, err := os.Stat(abs)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		root.Close()
		return nil, "", security.ErrDenied
	}
	return root, key, nil
}

// Resolve existing platform aliases before containment checks, preserving a
// missing suffix without following links/reparse points into repository data.
func canonicalPrefix(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil || security.CheckPath(abs) != nil {
		return "", security.ErrDenied
	}
	ancestor := abs
	suffix := []string{}
	for {
		_, err := os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", security.ErrDenied
		}
		suffix = append(suffix, filepath.Base(ancestor))
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", security.ErrDenied
		}
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", security.ErrDenied
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	return resolved, nil
}
func outsideProject(project, dir string) bool {
	project, err := filepath.Abs(project)
	if err != nil {
		return false
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		project = strings.ToLower(project)
		dir = strings.ToLower(dir)
	}
	if filepath.VolumeName(project) != filepath.VolumeName(dir) {
		return true
	}
	rel, err := filepath.Rel(project, dir)
	return err == nil && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Approve is called only by the explicit CLI reviewed-digest invocation, never MCP.
func (s Store) Approve(ctx context.Context, id, reviewed string) error {
	if !DigestOK(reviewed) {
		return ErrReview
	}
	doc, err := s.Read(ctx, id)
	if err != nil {
		return err
	}
	if doc.ReviewDigest != reviewed {
		return ErrReview
	}
	root, project, err := s.trust(true)
	if err != nil {
		return err
	}
	defer root.Close()
	name := project + "-" + reviewed + ".json"
	if doc.EffectiveStatus == "approved" {
		return nil
	}
	data, _ := json.Marshal(approval{1, id, reviewed, project})
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return ErrReview
	}
	_, err = f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err != nil || syncErr != nil || closeErr != nil {
		return ErrReview
	}
	return nil
}

// Approved returns every entry that is approved for the current user and whose
// document and citations still match, in manifest order. Entries that are
// proposed, stale or unreadable are left out and named in skipped, so callers
// publish only reviewed, current guidance. A missing manifest is not an error.
func (s Store) Approved(ctx context.Context) (docs []Document, skipped []string, err error) {
	m, err := s.load(ctx)
	if errors.Is(err, ErrAbsent) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, e := range m.Entries {
		doc, err := s.Read(ctx, e.ID)
		switch {
		case ctx.Err() != nil:
			return nil, nil, ctx.Err()
		case errors.Is(err, repository.ErrStale):
			skipped = append(skipped, e.ID+": document or cited source changed; review and approve it again to publish")
		case err != nil:
			skipped = append(skipped, e.ID+": cannot be verified; run conmesh knowledge show "+e.ID)
		case doc.EffectiveStatus != "approved":
			skipped = append(skipped, e.ID+": not approved yet; review it with conmesh knowledge show "+e.ID)
		default:
			docs = append(docs, doc)
		}
	}
	return docs, skipped, nil
}

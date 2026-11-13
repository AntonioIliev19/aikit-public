// Package repository applies the shared source policy to traversal and retrieval.
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/AntonioIliev19/conmesh/internal/security"
)

const MaxFileBytes = 1 << 20
const MaxFiles = 100000
const MaxEntries = 20000
const MaxDepth = 64
const MaxVisitedEntries = 100000
const MaxPolicyBytes = 4 << 20
const MaxSourceBytes = 512 << 20

var ErrLimit = errors.New("repository resource limit exceeded")
var ErrStale = errors.New("INDEX_STALE: source or policy changed; run conmesh index")

// ErrSensitive and ErrBinary refine security.ErrDenied (errors.Is still matches
// it, and the message is unchanged) so callers can report why content was excluded.
var ErrSensitive error = deniedReason("sensitive_content")
var ErrBinary error = deniedReason("binary")

type deniedReason string

func (deniedReason) Error() string    { return security.ErrDenied.Error() }
func (deniedReason) Unwrap() error    { return security.ErrDenied }
func (d deniedReason) Reason() string { return string(d) }

// SkipReasons breaks a Skipped count down by cause. Every skipped file is
// counted in exactly one field, so the fields sum to the Skipped total.
type SkipReasons struct {
	// Ignored entries matched .gitignore, .aikitignore or .conmeshignore rules.
	Ignored int `json:"ignored,omitempty"`
	// HardDenied entries matched built-in denied names (for example .git,
	// node_modules, vendor, build, bin, dist, .env, key files).
	HardDenied int `json:"hard_denied,omitempty"`
	// UnsupportedEntry covers symlinks, reparse points and other non-regular entries.
	UnsupportedEntry int `json:"unsupported_entry,omitempty"`
	// TooLarge files exceed MaxFileBytes.
	TooLarge int `json:"too_large,omitempty"`
	// SensitiveContent files matched a secret pattern.
	SensitiveContent int `json:"sensitive_content,omitempty"`
	// Binary files are not valid UTF-8 or contain NUL bytes.
	Binary int `json:"binary,omitempty"`
	// ParserLimit files exceeded parser limits even as plain text.
	ParserLimit int `json:"parser_limit,omitempty"`
	// Changed files changed or became denied between parsing and persistence.
	Changed int `json:"changed_during_index,omitempty"`
	// Unreadable files could not be read safely for any other reason, including
	// files replaced or newly denied between the scan and the read.
	Unreadable int `json:"unreadable,omitempty"`
}

// Total returns the sum of all reasons.
func (r SkipReasons) Total() int {
	return r.Ignored + r.HardDenied + r.UnsupportedEntry + r.TooLarge + r.SensitiveContent + r.Binary + r.ParserLimit + r.Changed + r.Unreadable
}

// CountReadError classifies an AuthorizedRead failure.
func (r *SkipReasons) CountReadError(err error) {
	switch {
	case errors.Is(err, ErrSensitive):
		r.SensitiveContent++
	case errors.Is(err, ErrBinary):
		r.Binary++
	default:
		r.Unreadable++
	}
}

type Snapshot struct {
	Root        *os.Root
	Policy      security.Policy
	Files       []string
	Skipped     int
	SkipReasons SkipReasons
}

func Scan(ctx context.Context, root *os.Root) (*Snapshot, error) {
	s := &Snapshot{Root: root}
	visited, policyBytes, sourceBytes := 0, int64(0), int64(0)
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > MaxDepth {
			return ErrLimit
		}
		for _, file := range []string{".gitignore", ".conmeshignore"} {
			name := file
			if dir != "." {
				name = dir + "/" + file
			}
			if info, err := root.Lstat(name); err == nil {
				policyBytes += info.Size()
			}
		}
		if policyBytes > MaxPolicyBytes {
			return ErrLimit
		}
		if err := s.Policy.LoadDirectory(root, dir); err != nil {
			return err
		}
		entries, err := security.ReadDirBounded(root, dir, MaxEntries)
		if err != nil {
			return err
		}
		visited += len(entries)
		if visited > MaxVisitedEntries {
			return ErrLimit
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			if dir != "." {
				name = dir + "/" + name
			}
			info, err := root.Lstat(name)
			if err != nil {
				return errors.New("repository entry unavailable")
			}
			if security.CheckEntry(info) != nil {
				s.Skipped++
				s.SkipReasons.UnsupportedEntry++
				continue
			}
			if s.Policy.Denied(name, info.IsDir()) {
				s.Skipped++
				if security.HardDenied(name, info.IsDir()) {
					s.SkipReasons.HardDenied++
				} else {
					s.SkipReasons.Ignored++
				}
				continue
			}
			if info.IsDir() {
				if err := walk(name, depth+1); err != nil {
					return err
				}
				continue
			}
			if info.Size() > MaxFileBytes {
				s.Skipped++
				s.SkipReasons.TooLarge++
				continue
			}
			sourceBytes += info.Size()
			if sourceBytes > MaxSourceBytes {
				return ErrLimit
			}
			if len(s.Files) >= MaxFiles {
				return ErrLimit
			}
			s.Files = append(s.Files, name)
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return nil, err
	}
	return s, nil
}

// Read checks the policy before opening any source, then excludes secret/binary data.
func (s *Snapshot) Read(name string) ([]byte, string, error) {
	if s.Policy.Denied(name, false) {
		return nil, "", security.ErrDenied
	}
	data, err := security.ReadFileBounded(s.Root, name, MaxFileBytes)
	if err != nil {
		return nil, "", err
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return nil, "", ErrBinary
	}
	if security.SensitiveContent(data) {
		return nil, "", ErrSensitive
	}
	return data, Digest(data), nil
}

func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// AuthorizedRead reloads only the path's ancestor rules, pruning denied parents.
func AuthorizedRead(root *os.Root, name string) ([]byte, string, error) {
	s, err := authorize(root, name)
	if err != nil {
		return nil, "", err
	}
	return s.Read(name)
}

// AuthorizePath checks current path policy without loading document contents.
func AuthorizePath(root *os.Root, name string) error {
	s, err := authorize(root, name)
	if err != nil {
		return err
	}
	if s.Policy.Denied(name, false) {
		return security.ErrDenied
	}
	info, err := root.Lstat(name)
	if err != nil || security.CheckEntry(info) != nil || !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
		return security.ErrDenied
	}
	return nil
}

func authorize(root *os.Root, name string) (*Snapshot, error) {
	if len(name) > 4096 || security.ValidateRelative(name) != nil {
		return nil, security.ErrDenied
	}
	s := &Snapshot{Root: root}
	if err := s.Policy.LoadDirectory(root, "."); err != nil {
		return nil, err
	}
	parts := strings.Split(name, "/")
	for i := 1; i < len(parts); i++ {
		dir := strings.Join(parts[:i], "/")
		if i > MaxDepth {
			return nil, ErrLimit
		}
		if s.Policy.Denied(dir, true) {
			return nil, security.ErrDenied
		}
		info, err := root.Lstat(dir)
		if err != nil || security.CheckEntry(info) != nil || !info.IsDir() {
			return nil, security.ErrDenied
		}
		if err := s.Policy.LoadDirectory(root, dir); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func ValidateScope(scope string) error {
	if scope == "" {
		return nil
	}
	return security.ValidateRelative(scope)
}

func InScope(name, scope string) bool {
	return scope == "" || name == scope || strings.HasPrefix(name, scope+"/")
}

func Language(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".cs":
		return "csharp"
	}
	return "text"
}

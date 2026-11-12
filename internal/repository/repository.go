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

	"github.com/AntonioIliev19/aikit-public/internal/security"
)

const MaxFileBytes = 1 << 20
const MaxFiles = 100000
const MaxEntries = 20000
const MaxDepth = 64
const MaxVisitedEntries = 100000
const MaxPolicyBytes = 4 << 20
const MaxSourceBytes = 512 << 20

var ErrLimit = errors.New("repository resource limit exceeded")
var ErrStale = errors.New("INDEX_STALE: source or policy changed; run aikit index")

type Snapshot struct {
	Root    *os.Root
	Policy  security.Policy
	Files   []string
	Skipped int
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
		for _, file := range []string{".gitignore", ".aikitignore"} {
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
			if security.CheckEntry(info) != nil || s.Policy.Denied(name, info.IsDir()) {
				s.Skipped++
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
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || security.SensitiveContent(data) {
		return nil, "", security.ErrDenied
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

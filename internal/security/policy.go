package security

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"

	gitignore "github.com/denormal/go-gitignore"
)

const MaxIgnoreBytes = 64 << 10

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?i)(?:api[_-]?key|access[_-]?token|password|client[_-]?secret)["']?\s*[=:]\s*["']?[a-z0-9_+/=-]{12,}`),
}

// SensitiveContent is deliberately conservative, not a complete secret detector.
func SensitiveContent(data []byte) bool {
	for _, pattern := range secretPatterns {
		if pattern.Match(data) {
			return true
		}
	}
	return false
}

// HardDenied cannot be overridden by either ignore file.
func HardDenied(name string, isDir bool) bool {
	if ValidateRelative(name) != nil || SensitiveContent([]byte(name)) {
		return true
	}
	parts := strings.Split(strings.ToLower(name), "/")
	for _, part := range parts {
		switch part {
		case ".git", "node_modules", "vendor", ".venv", "dist", "build", ".next", "bin", "obj", "coverage", ".ssh", ".aws", ".azure", ".gnupg", "secrets", "credentials":
			return true
		}
		if part == ".env" || strings.HasPrefix(part, ".env.") || part == "id_rsa" || part == "id_ed25519" || part == "id_dsa" || part == "id_ecdsa" ||
			strings.Contains(part, "service-account") || strings.Contains(part, "service_account") {
			return true
		}
		for _, ext := range []string{".pem", ".key", ".p12", ".pfx", ".keystore"} {
			if strings.HasSuffix(part, ext) {
				return true
			}
		}
	}
	for i := 0; i+1 < len(parts); i++ {
		if (parts[i] == ".conmesh" || parts[i] == ".aikit") && (parts[i+1] == "state" || parts[i+1] == "cache" || parts[i+1] == "packages") {
			return true
		}
	}
	return false
}

type ignoreFile struct {
	base   string
	family string
	rule   gitignore.GitIgnore
}

// Policy evaluates ancestor ignore rules without traversing their filesystem.
// Callers must walk directories in order and call LoadDirectory before children.
type Policy struct {
	rules []ignoreFile
}

// LoadDirectory reads only bounded, regular .gitignore and .conmeshignore inputs.
func (p *Policy) LoadDirectory(root *os.Root, dir string) error {
	for _, file := range []string{".gitignore", ".aikitignore", ".conmeshignore"} {
		name := file
		if dir != "." {
			name = dir + "/" + file
		}
		info, err := root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || CheckEntry(info) != nil || info.IsDir() || info.Size() > MaxIgnoreBytes {
			return ErrDenied
		}
		data, err := ReadFileBounded(root, name, MaxIgnoreBytes)
		if err != nil || len(data) > MaxIgnoreBytes {
			return ErrDenied
		}
		data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
		invalid := false
		rule := gitignore.New(bytes.NewReader(data), ".", func(gitignore.Error) bool { invalid = true; return false })
		if invalid {
			return errors.New("SECURITY_DENIED: invalid ignore policy")
		}
		p.rules = append(p.rules, ignoreFile{base: dir, family: file, rule: rule})
	}
	return nil
}

// Denied honors nested gitignore rules and applies .conmeshignore last. Callers
// prune denied directories, matching Git's no-reinclude-under-ignored-parent rule.
func (p *Policy) Denied(name string, isDir bool) bool {
	if HardDenied(name, isDir) {
		return true
	}
	parts := strings.Split(name, "/")
	for i := 1; i <= len(parts); i++ {
		if p.ignored(strings.Join(parts[:i], "/"), i < len(parts) || isDir) {
			return true
		}
	}
	return false
}

func (p *Policy) ignored(name string, isDir bool) bool {
	ignored := map[string]bool{}
	for _, rule := range p.rules {
		rel := name
		if rule.base != "." {
			prefix := rule.base + "/"
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			rel = strings.TrimPrefix(name, prefix)
		}
		if match := rule.rule.Relative(path.Clean(rel), isDir); match != nil {
			ignored[rule.family] = match.Ignore()
		}
	}
	return ignored[".gitignore"] || ignored[".aikitignore"] || ignored[".conmeshignore"]
}

// ReadFileBounded confines reads, checks the opened object, and caps allocation
// even when another process grows or replaces a file during inspection.
func ReadFileBounded(root *os.Root, name string, max int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil || CheckEntry(info) != nil || !info.Mode().IsRegular() || info.Size() > max {
		return nil, ErrDenied
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, ErrDenied
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > max {
		return nil, ErrDenied
	}
	current, err := root.Lstat(name)
	if err != nil || CheckEntry(current) != nil || !os.SameFile(current, opened) {
		return nil, ErrDenied
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, ErrDenied
	}
	return data, nil
}

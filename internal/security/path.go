// Package security owns path confinement and shared source exclusions.
package security

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var ErrDenied = errors.New("SECURITY_DENIED: unsafe path or source content")
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidateID accepts portable single-component package identifiers.
func ValidateID(id string) error {
	if !idPattern.MatchString(id) || ValidateRelative(id) != nil {
		return ErrDenied
	}
	return nil
}

// ValidateRelative rejects ambiguous cross-platform paths, even on Unix.
func ValidateRelative(name string) error {
	if name == "" || strings.ContainsAny(name, `\:`) || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return ErrDenied
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return ErrDenied
		}
		for _, c := range part {
			if c < 32 || c == 127 || strings.ContainsRune(`<>"|?*`, c) {
				return ErrDenied
			}
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return ErrDenied
		}
	}
	return nil
}

// CheckPath rejects links/reparse points in every existing ancestor.
// Subsequent reads/writes must also use os.Root to confine path-swap races.
func CheckPath(name string) error {
	abs, err := filepath.Abs(name)
	if err != nil {
		return ErrDenied
	}
	for current := abs; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return ErrDenied
		}
		if err == nil && IsLink(info) {
			return ErrDenied
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}

func OpenRoot(name string) (*os.Root, error) {
	if err := CheckPath(name); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// CheckEntry rejects links, devices and other non-regular source objects.
func CheckEntry(info fs.FileInfo) error {
	if IsLink(info) || (!info.IsDir() && !info.Mode().IsRegular()) {
		return ErrDenied
	}
	return nil
}

// ReadDirBounded limits enumeration allocation and rejects directory links.
func ReadDirBounded(root *os.Root, name string, max int) ([]fs.DirEntry, error) {
	info, err := root.Lstat(name)
	if err != nil || CheckEntry(info) != nil || !info.IsDir() {
		return nil, ErrDenied
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, ErrDenied
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.IsDir() {
		return nil, ErrDenied
	}
	entries, err := f.ReadDir(max + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, ErrDenied
	}
	if len(entries) > max {
		return nil, errors.New("directory entry limit exceeded")
	}
	return entries, nil
}

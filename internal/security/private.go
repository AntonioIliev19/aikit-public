package security

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnsurePrivateDirectory creates missing components through the closest existing
// safe ancestor. An existing directory's permissions are never silently changed.
func EnsurePrivateDirectory(name string) error {
	abs, err := filepath.Abs(name)
	if err != nil || CheckPath(abs) != nil {
		return ErrDenied
	}
	ancestor := abs
	for {
		info, err := os.Lstat(ancestor)
		if err == nil {
			if CheckEntry(info) != nil || !info.IsDir() {
				return ErrDenied
			}
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return ErrDenied
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return ErrDenied
		}
		ancestor = parent
	}
	root, err := OpenRoot(ancestor)
	if err != nil {
		return ErrDenied
	}
	defer root.Close()
	rel, err := filepath.Rel(ancestor, abs)
	if err != nil {
		return ErrDenied
	}
	if rel != "." {
		if root.MkdirAll(rel, 0700) != nil {
			return ErrDenied
		}
	}
	if CheckPath(abs) != nil {
		return ErrDenied
	}
	info, err := os.Stat(abs)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return ErrDenied
	}
	return nil
}

// PrivateFile confines local state and checks SQLite companions before opening.
// Callers still open through os.Root, except SQLite's path-based driver.
func PrivateFile(root *os.Root, name string, create bool) (string, error) {
	if ValidateRelative(name) != nil || strings.Contains(name, "/") {
		return "", ErrDenied
	}
	if create {
		if err := EnsurePrivateState(root); err != nil {
			return "", err
		}
	}
	for _, dir := range []string{".conmesh", ".conmesh/state"} {
		info, err := root.Lstat(dir)
		if err != nil {
			return "", err
		}
		if CheckEntry(info) != nil || !info.IsDir() || (dir == ".conmesh/state" && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return "", ErrDenied
		}
	}
	names := []string{name}
	if strings.HasSuffix(name, ".sqlite") {
		names = append(names, name+"-wal", name+"-shm", name+"-journal")
	}
	for _, n := range names {
		rel := ".conmesh/state/" + n
		if CheckPath(filepath.Join(root.Name(), filepath.FromSlash(rel))) != nil {
			return "", ErrDenied
		}
		info, err := root.Lstat(rel)
		if errors.Is(err, fs.ErrNotExist) {
			if n == name {
				if !create {
					return "", err
				}
				f, e := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					return "", ErrDenied
				}
				if f.Close() != nil {
					return "", ErrDenied
				}
			}
			continue
		}
		if err != nil || CheckEntry(info) != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
			return "", ErrDenied
		}
	}
	return filepath.Join(root.Name(), ".conmesh", "state", name), nil
}

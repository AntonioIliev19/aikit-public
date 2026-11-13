package security

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// EnsurePrivateState guards local databases and recoverable config backups from
// source scanning and default Git staging. It never overwrites a user's ignore.
func EnsurePrivateState(root *os.Root) error {
	if err := CheckPath(filepath.Join(root.Name(), ".conmesh", "state")); err != nil {
		return err
	}
	if err := root.MkdirAll(".conmesh/state", 0700); err != nil {
		return ErrDenied
	}
	for _, name := range []string{".conmesh", ".conmesh/state"} {
		info, err := root.Lstat(name)
		if err != nil || CheckEntry(info) != nil || !info.IsDir() {
			return ErrDenied
		}
		if name == ".conmesh/state" && runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			return ErrDenied
		}
	}
	state, err := root.OpenRoot(".conmesh/state")
	if err != nil {
		return ErrDenied
	}
	defer state.Close()
	f, err := state.OpenFile(".gitignore", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		data, err := ReadFileBounded(state, ".gitignore", 64)
		if err != nil || !bytes.Equal(bytes.TrimSpace(data), []byte("*")) {
			return ErrDenied
		}
		return nil
	}
	if err != nil {
		return ErrDenied
	}
	_, err = f.WriteString("*\n")
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrDenied
	}
	return nil
}

package packages

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/AntonioIliev19/conmesh/internal/security"
)

// Exported is an immutable caller-owned copy of a verified effective package.
type Exported struct {
	Entry Entry
	Files map[string][]byte
}

// Export takes bounded snapshots while managed stores are locked. Overrides are
// user-owned; their copied bytes and manifest are validated together.
func (m *Manager) Export(ctx context.Context, kind string) ([]Exported, error) {
	if !validKind(kind) {
		return nil, security.ErrDenied
	}
	effective := map[string]Exported{}
	total := 0
	for _, scope := range []string{"global", "project", "override"} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if scope != "global" && m.Project == "" {
			continue
		}
		var base string
		var err error
		if scope == "override" {
			base = filepath.Join(m.Project, ".conmesh", "overrides", kind+"s")
		} else {
			base, err = m.base(scope)
			if err != nil {
				return nil, err
			}
		}
		r, err := openBase(base, false)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, security.ErrDenied
		}
		err = func() error {
			defer r.Close()
			var entries []Entry
			if scope == "override" {
				dirs, err := security.ReadDirBounded(r, ".", 128)
				if err != nil {
					return err
				}
				for _, d := range dirs {
					if security.ValidateID(d.Name()) != nil {
						return security.ErrDenied
					}
					entries = append(entries, Entry{ID: d.Name(), Type: kind, Scope: scope})
				}
			} else {
				release, err := acquire(r)
				if err != nil {
					return err
				}
				defer release()
				entries, err = loadState(r, scope)
				if err != nil {
					return err
				}
			}
			for _, e := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if e.Type != kind {
					continue
				}
				name := "packages/" + packagePath(kind, e.ID)
				if scope == "override" {
					name = e.ID
				}
				// Reuse the import scanner, including both ignore families, hard denies,
				// secret detection and bounds. User-owned overrides need identical policy.
				snapshot, err := loadLocal(ctx, filepath.Join(base, filepath.FromSlash(name)), kind, "")
				if err != nil {
					return err
				}
				files, pm := snapshot.files, snapshot.manifest
				if pm.ID != e.ID {
					return errors.New("invalid export package manifest")
				}
				digest := digestFiles(files)
				if scope == "override" {
					e = Entry{ID: pm.ID, Type: kind, Scope: scope, Source: "project override", Version: pm.Version, License: pm.License, SHA256: digest}
				} else if digest != e.SHA256 {
					return errors.New("PACKAGE_INTEGRITY_FAILED: export changed")
				}
				for _, data := range files {
					total += len(data)
				}
				if total > MaxStoreBytes {
					return errors.New("package export size limit exceeded")
				}
				effective[e.ID] = Exported{Entry: e, Files: files}
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}
	}
	result := make([]Exported, 0, len(effective))
	for _, e := range effective {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Entry.ID < result[j].Entry.ID })
	return result, nil
}

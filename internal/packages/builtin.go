package packages

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AntonioIliev19/conmesh/agents"
	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/security"
)

// BuiltinPrefix marks a predefined package shipped inside the ConMesh binary,
// for example "builtin:angular-agent". The source stays the same across
// releases, so `package update` picks up the copy in the running binary.
const BuiltinPrefix = "builtin:"

// builtinFS is the embedded catalog; tests replace it.
var builtinFS fs.FS = agents.FS

// CatalogEntry describes one predefined package.
type CatalogEntry struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Version     string `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// Catalog lists the predefined packages of one kind, sorted by ID. An empty
// kind lists every kind.
func Catalog(kind string) ([]CatalogEntry, error) {
	if kind != "" && !validKind(kind) {
		return nil, security.ErrDenied
	}
	dirs, err := fs.ReadDir(builtinFS, ".")
	if err != nil {
		return nil, errors.New("predefined package catalog unavailable")
	}
	var out []CatalogEntry
	for _, d := range dirs {
		if !d.IsDir() || security.ValidateID(d.Name()) != nil {
			continue
		}
		data, err := fs.ReadFile(builtinFS, d.Name()+"/"+SourceManifest)
		if err != nil {
			continue
		}
		var m PackageManifest
		if config.Decode(data, &m) != nil || m.validate() != nil || m.ID != d.Name() {
			return nil, errors.New("invalid predefined package manifest")
		}
		if kind != "" && m.Type != kind {
			continue
		}
		e := CatalogEntry{ID: m.ID, Type: m.Type, Version: m.Version, Title: m.ID, Source: BuiltinPrefix + m.ID}
		var meta struct {
			Index struct {
				Title       string `json:"title"`
				Description string `json:"description"`
			} `json:"index"`
		}
		// Catalog metadata is optional, only used for display, and carries many
		// fields ConMesh does not read, so it is decoded leniently.
		if raw, err := fs.ReadFile(builtinFS, d.Name()+"/"+d.Name()+".meta.json"); err == nil && json.Unmarshal(raw, &meta) == nil {
			if t := displayText(meta.Index.Title, 64); t != "" {
				e.Title = t
			}
			e.Description = displayText(meta.Index.Description, 200)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// displayText keeps catalog strings to one printable line.
func displayText(s string, max int) string {
	s = strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }), " ")
	if r := []rune(strings.TrimSpace(s)); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return strings.TrimSpace(s)
}

// builtinID returns the package ID of a builtin source.
func builtinID(source string) (string, bool) {
	id, ok := strings.CutPrefix(source, BuiltinPrefix)
	if !ok {
		return "", false
	}
	return id, security.ValidateID(id) == nil
}

// loadBuiltin copies one embedded package into a private temporary directory
// and loads it through the same checks as a local directory, like a Git source.
func loadBuiltin(ctx context.Context, source, kind, version string) (snapshot, error) {
	id, ok := builtinID(source)
	if !ok {
		return snapshot{}, errors.New("unknown predefined package")
	}
	sub, err := fs.Sub(builtinFS, id)
	if err != nil {
		return snapshot{}, errors.New("unknown predefined package")
	}
	if _, err := fs.Stat(sub, SourceManifest); err != nil {
		return snapshot{}, errors.New("unknown predefined package")
	}
	tmp, err := os.MkdirTemp("", "conmesh-builtin-")
	if err != nil {
		return snapshot{}, errors.New("cannot stage predefined package")
	}
	defer os.RemoveAll(tmp)
	tmp, err = filepath.EvalSymlinks(tmp)
	if err != nil {
		return snapshot{}, errors.New("cannot stage predefined package")
	}
	count, total := 0, 0
	err = fs.WalkDir(sub, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if name == "." {
			return nil
		}
		if security.ValidateRelative(name) != nil || strings.Count(name, "/") >= MaxDepth {
			return security.ErrDenied
		}
		target := filepath.Join(tmp, filepath.FromSlash(name))
		if d.IsDir() {
			return os.Mkdir(target, 0700)
		}
		count++
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			return err
		}
		total += len(data)
		if count > MaxFiles || len(data) > MaxFileBytes || total > MaxPackageBytes {
			return errors.New("package size limit exceeded")
		}
		return os.WriteFile(target, data, 0600)
	})
	if err != nil {
		return snapshot{}, errors.New("cannot stage predefined package")
	}
	s, err := loadLocal(ctx, tmp, kind, version)
	if err != nil {
		return snapshot{}, err
	}
	if s.manifest.ID != id {
		return snapshot{}, errors.New("predefined package identity mismatch")
	}
	s.source = BuiltinPrefix + id
	return s, nil
}

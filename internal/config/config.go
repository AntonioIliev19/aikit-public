// Package config owns strict project configuration and scope locations.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/AntonioIliev19/conmesh/internal/embeddings"
	"github.com/AntonioIliev19/conmesh/internal/security"
)

const Schema = 1
const File = "config.yaml"

var ErrNoProject = errors.New("NO_PROJECT: run conmesh init in the project root")

type Indexing struct {
	Enabled  bool   `json:"enabled"`
	Profile  string `json:"profile"`
	Semantic bool   `json:"semantic"`
}

type Config struct {
	SchemaVersion int                  `json:"schema_version"`
	Harnesses     []string             `json:"harnesses"`
	Indexing      Indexing             `json:"indexing"`
	Semantic      *embeddings.Settings `json:"semantic,omitempty"`
}

func Default() Config {
	return Config{SchemaVersion: Schema, Harnesses: []string{}, Indexing: Indexing{Profile: "balanced"}}
}

func (c Config) Validate() error {
	if c.SchemaVersion != Schema {
		return errors.New("unsupported project config schema")
	}
	if c.Indexing.Profile != "eco" && c.Indexing.Profile != "balanced" && c.Indexing.Profile != "performance" {
		return errors.New("invalid indexing profile")
	}
	if c.Indexing.Semantic && !c.Indexing.Enabled {
		return errors.New("semantic indexing requires explicit indexing opt-in")
	}
	if c.Semantic != nil {
		if err := c.Semantic.Validate(); err != nil {
			return err
		}
		if !c.Indexing.Semantic {
			return errors.New("semantic settings require explicit semantic opt-in")
		}
	}
	seen := map[string]bool{}
	for _, h := range c.Harnesses {
		if (h != "claude" && h != "codex" && h != "cursor" && h != "copilot") || seen[h] {
			return errors.New("invalid or duplicate harness")
		}
		seen[h] = true
	}
	return nil
}

func Decode(data []byte, value any) error {
	if security.CheckJSON(data) != nil {
		return errors.New("invalid configuration or manifest JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return errors.New("invalid configuration or manifest JSON")
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing configuration data")
	}
	return nil
}

func GlobalDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("user data directory unavailable")
	}
	return filepath.Join(base, "conmesh"), nil
}

var errLegacyProject = errors.New("legacy .aikit project detected; follow the ConMesh migration instructions")

func checkLegacyProject(root *os.Root) error {
	if _, err := root.Lstat(".aikit"); !errors.Is(err, fs.ErrNotExist) {
		return errLegacyProject
	}
	return nil
}

// Init creates a new project config exclusively and preserves an existing one.
func Init(project string, requested Config) (Config, bool, error) {
	if err := requested.Validate(); err != nil {
		return Config{}, false, err
	}
	root, err := security.OpenRoot(project)
	if err != nil {
		return Config{}, false, security.ErrDenied
	}
	defer root.Close()
	if _, err := root.Lstat(".conmesh/" + File); errors.Is(err, fs.ErrNotExist) {
		if err := checkLegacyProject(root); err != nil {
			return Config{}, false, err
		}
	}
	if err := security.CheckPath(filepath.Join(project, ".conmesh", File)); err != nil {
		return Config{}, false, err
	}
	if err := root.MkdirAll(".conmesh", 0700); err != nil {
		return Config{}, false, errors.New("cannot create project data directory")
	}
	dir, err := root.OpenRoot(".conmesh")
	if err != nil {
		return Config{}, false, security.ErrDenied
	}
	defer dir.Close()
	f, err := dir.OpenFile(File, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, fs.ErrExist) {
		c, err := Load(project)
		return c, false, err
	}
	if err != nil {
		return Config{}, false, errors.New("cannot create project config")
	}
	data, _ := json.MarshalIndent(requested, "", "  ")
	_, writeErr := f.Write(append(data, '\n'))
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		dir.Remove(File)
		return Config{}, false, errors.New("cannot persist project config")
	}
	return requested, true, nil
}

func Load(project string) (Config, error) {
	name := filepath.Join(project, ".conmesh", File)
	if err := security.CheckPath(name); err != nil {
		return Config{}, err
	}
	root, err := security.OpenRoot(project)
	if err != nil {
		return Config{}, security.ErrDenied
	}
	defer root.Close()
	info, err := root.Lstat(".conmesh/" + File)
	if errors.Is(err, fs.ErrNotExist) {
		if err := checkLegacyProject(root); err != nil {
			return Config{}, err
		}
		return Config{}, ErrNoProject
	}
	if err != nil || security.CheckEntry(info) != nil || info.IsDir() || info.Size() > 64<<10 {
		return Config{}, security.ErrDenied
	}
	data, err := security.ReadFileBounded(root, ".conmesh/"+File, 64<<10)
	if err != nil || len(data) > 64<<10 {
		return Config{}, security.ErrDenied
	}
	var c Config
	if err := Decode(data, &c); err != nil {
		return Config{}, err
	}
	return c, c.Validate()
}

func Find(start string) (string, Config, error) {
	root, err := filepath.Abs(start)
	if err != nil {
		return "", Config{}, ErrNoProject
	}
	for {
		c, err := Load(root)
		if err == nil {
			return root, c, nil
		}
		if !errors.Is(err, ErrNoProject) {
			return "", Config{}, err
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", Config{}, ErrNoProject
		}
		root = parent
	}
}

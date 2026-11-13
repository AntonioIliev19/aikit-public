// Package packages implements reviewed, scoped imports without executing content.
package packages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/security"
)

const Schema = 1

type Entry struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Scope   string `json:"scope"`
	Source  string `json:"source"`
	Ref     string `json:"ref,omitempty"`
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	SHA256  string `json:"sha256"`
	License string `json:"license"`
}

type Spec struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Source  string `json:"source"`
	Ref     string `json:"ref,omitempty"`
	Version string `json:"version"`
}

type manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Scope         string `json:"scope"`
	Packages      []Spec `json:"packages"`
}

type lockfile struct {
	SchemaVersion int     `json:"schema_version"`
	Scope         string  `json:"scope"`
	Packages      []Entry `json:"packages"`
}

type Item struct {
	Entry
	Path       string `json:"path"`
	Effective  bool   `json:"effective"`
	ShadowedBy string `json:"shadowed_by,omitempty"`
}

type Result struct {
	Entry     Entry
	Unchanged bool
	Omitted   int
}

// Manager keeps project and global roots explicit; an empty project permits
// global-only commands. Roots are resolved by trusted CLI context, never a lock.
type Manager struct {
	Project    string
	Global     string
	git        gitRunner
	beforeSwap func(string) error
	rename     func(*os.Root, string, string) error
	// storeLimit is an internal regression-test budget override; zero uses MaxStoreBytes.
	storeLimit int
}

func New(project, global string) *Manager {
	return &Manager{Project: project, Global: global, git: runGit}
}

func (m *Manager) base(scope string) (string, error) {
	switch scope {
	case "project":
		if m.Project == "" {
			return "", config.ErrNoProject
		}
		if _, err := config.Load(m.Project); err != nil {
			return "", err
		}
		return filepath.Join(m.Project, ".conmesh"), nil
	case "global":
		if m.Global == "" {
			return "", errors.New("global data directory unavailable")
		}
		return m.Global, nil
	default:
		return "", errors.New("scope must be project or global")
	}
}

func openBase(base string, create bool) (*os.Root, error) {
	if err := security.CheckPath(base); err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(base, 0700); err != nil {
			return nil, errors.New("cannot create package data directory")
		}
	}
	r, err := security.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func acquire(root *os.Root) (func(), error) {
	if _, err := root.Lstat(".packages-backup"); !errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("package recovery required: preserve .packages-backup before retrying")
	}
	f, err := root.OpenFile(".packages-mutation.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, errors.New("package store busy or interrupted; inspect mutation lock before retrying")
	}
	if _, err := f.WriteString("CONMESH package transaction\n"); err != nil {
		f.Close()
		root.Remove(".packages-mutation.lock")
		return nil, errors.New("cannot persist mutation lock")
	}
	f.Close()
	return func() { root.Remove(".packages-mutation.lock") }, nil
}

func packagePath(kind, id string) string { return kind + "s/" + id }
func key(kind, id string) string         { return kind + ":" + id }

func validateEntry(e Entry, scope string) error {
	if e.Scope != scope || !validKind(e.Type) || security.ValidateID(e.ID) != nil || !versionPattern.MatchString(e.Version) || !labelPattern.MatchString(e.License) || len(e.SHA256) != 64 || strings.Trim(e.SHA256, "0123456789abcdef") != "" {
		return errors.New("invalid package lock entry")
	}
	if e.Commit != "" {
		u, isGit, err := canonicalSource(e.Source)
		if err != nil || !isGit || u != e.Source || !validRef(e.Ref) || !commitPattern.MatchString(e.Commit) {
			return errors.New("invalid Git source in package lock")
		}
	} else if _, ok := builtinID(e.Source); ok {
		if e.Ref != "" {
			return errors.New("invalid predefined source in package lock")
		}
	} else if !filepath.IsAbs(e.Source) || security.CheckPath(e.Source) != nil {
		return errors.New("invalid local source in package lock")
	}
	return nil
}

// loadState validates metadata and every managed byte before any mutation.
func loadState(root *os.Root, scope string) ([]Entry, error) {
	info, err := root.Lstat("packages")
	if errors.Is(err, fs.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil || !info.IsDir() || security.CheckEntry(info) != nil {
		return nil, security.ErrDenied
	}
	store, err := root.OpenRoot("packages")
	if err != nil {
		return nil, security.ErrDenied
	}
	defer store.Close()
	var lock lockfile
	var specs manifest
	for _, target := range []struct {
		name  string
		value any
	}{{"lock.json", &lock}, {"manifest.json", &specs}} {
		data, err := security.ReadFileBounded(store, target.name, MaxFileBytes)
		if err != nil || config.Decode(data, target.value) != nil {
			return nil, errors.New("invalid package state metadata")
		}
	}
	if lock.SchemaVersion != Schema || specs.SchemaVersion != Schema || lock.Scope != scope || specs.Scope != scope || len(lock.Packages) != len(specs.Packages) || len(lock.Packages) > 128 {
		return nil, errors.New("unsupported or inconsistent package state")
	}
	seen := map[string]bool{}
	total := 0
	for i, e := range lock.Packages {
		if validateEntry(e, scope) != nil || seen[key(e.Type, e.ID)] {
			return nil, errors.New("invalid package lock entry")
		}
		seen[key(e.Type, e.ID)] = true
		want := Spec{ID: e.ID, Type: e.Type, Source: e.Source, Ref: e.Ref, Version: e.Version}
		if specs.Packages[i] != want {
			return nil, errors.New("package manifest/lock mismatch")
		}
		files, err := readTree(store, packagePath(e.Type, e.ID), MaxPackageBytes)
		if err != nil || digestFiles(files) != e.SHA256 {
			return nil, errors.New("PACKAGE_INTEGRITY_FAILED: managed package changed; preserve it before retrying")
		}
		for _, data := range files {
			total += len(data)
		}
		if total > MaxStoreBytes {
			return nil, errors.New("package store size limit exceeded")
		}
	}
	if err := verifyStoreLayout(store, seen); err != nil {
		return nil, err
	}
	return lock.Packages, nil
}

func verifyStoreLayout(root *os.Root, known map[string]bool) error {
	entries, err := security.ReadDirBounded(root, ".", 4)
	if err != nil {
		return security.ErrDenied
	}
	for _, e := range entries {
		if e.Name() == "manifest.json" || e.Name() == "lock.json" {
			continue
		}
		if (e.Name() != "agents" && e.Name() != "skills") || !e.IsDir() {
			return errors.New("unowned package store content; preserve it before retrying")
		}
		info, err := root.Lstat(e.Name())
		if err != nil || security.CheckEntry(info) != nil {
			return security.ErrDenied
		}
		packages, err := security.ReadDirBounded(root, e.Name(), 128)
		if err != nil {
			return security.ErrDenied
		}
		kind := strings.TrimSuffix(e.Name(), "s")
		for _, p := range packages {
			if !known[key(kind, p.Name())] {
				return errors.New("unowned package directory; preserve it before retrying")
			}
		}
	}
	return nil
}

func readTree(root *os.Root, dir string, max int) (map[string][]byte, error) {
	info, err := root.Lstat(dir)
	if err != nil || !info.IsDir() || security.CheckEntry(info) != nil {
		return nil, security.ErrDenied
	}
	r, err := root.OpenRoot(dir)
	if err != nil {
		return nil, security.ErrDenied
	}
	defer r.Close()
	files := map[string][]byte{}
	total, count, visited := 0, 0, 0
	seen := map[string]bool{}
	var walk func(string) error
	walk = func(dir string) error {
		if strings.Count(dir, "/") >= MaxDepth {
			return errors.New("package directory depth limit exceeded")
		}
		entries, err := security.ReadDirBounded(r, dir, MaxEntries)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			return errors.New("unowned empty package directory")
		}
		for _, entry := range entries {
			visited++
			if visited > MaxEntries {
				return errors.New("package directory entry limit exceeded")
			}
			name := entry.Name()
			if dir != "." {
				name = dir + "/" + name
			}
			if security.ValidateRelative(name) != nil {
				return security.ErrDenied
			}
			folded := strings.ToLower(name)
			if seen[folded] {
				return errors.New("case-insensitive package path collision")
			}
			seen[folded] = true
			info, err := r.Lstat(name)
			if err != nil || security.CheckEntry(info) != nil {
				return security.ErrDenied
			}
			if info.IsDir() {
				if err := walk(name); err != nil {
					return err
				}
				continue
			}
			count++
			if count > MaxFiles {
				return errors.New("package file limit exceeded")
			}
			data, err := security.ReadFileBounded(r, name, MaxFileBytes)
			if err != nil {
				return err
			}
			total += len(data)
			if total > max {
				return errors.New("package size limit exceeded")
			}
			files[name] = data
		}
		return nil
	}
	err = walk(".")
	return files, err
}

func writeTree(root *os.Root, dir string, files map[string][]byte) error {
	if err := root.MkdirAll(dir, 0700); err != nil {
		return errors.New("cannot stage package directory")
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if security.ValidateRelative(name) != nil {
			return security.ErrDenied
		}
		target := dir + "/" + name
		if err := root.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return errors.New("cannot stage package directory")
		}
		f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("cannot stage package file")
		}
		_, writeErr := f.Write(files[name])
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return errors.New("cannot persist staged package file")
		}
	}
	return nil
}

func writeJSON(root *os.Root, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot stage package metadata")
	}
	_, writeErr := f.Write(append(data, '\n'))
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("cannot persist package metadata")
	}
	return nil
}

func (m *Manager) commit(root *os.Root, scope string, entries []Entry, replace *snapshot, remove string) error {
	stage, err := os.MkdirTemp(root.Name(), ".packages-stage-")
	if err != nil {
		return errors.New("cannot create package transaction")
	}
	stageName := filepath.Base(stage)
	defer root.RemoveAll(stageName)
	r, err := root.OpenRoot(stageName)
	if err != nil {
		return security.ErrDenied
	}
	defer r.Close()
	next := make([]Entry, 0, len(entries)+1)
	totalBytes := 0
	for _, e := range entries {
		if key(e.Type, e.ID) == remove || (replace != nil && e.Type == replace.manifest.Type && e.ID == replace.manifest.ID) {
			continue
		}
		files, err := readTree(root, "packages/"+packagePath(e.Type, e.ID), MaxPackageBytes)
		if err != nil {
			return err
		}
		if digestFiles(files) != e.SHA256 {
			return errors.New("PACKAGE_INTEGRITY_FAILED: package changed during transaction")
		}
		for _, data := range files {
			totalBytes += len(data)
		}
		if err := writeTree(r, packagePath(e.Type, e.ID), files); err != nil {
			return err
		}
		next = append(next, e)
	}
	if replace != nil {
		for _, data := range replace.files {
			totalBytes += len(data)
		}
		e := entryFromSnapshot(*replace, scope)
		if err := writeTree(r, packagePath(e.Type, e.ID), replace.files); err != nil {
			return err
		}
		next = append(next, e)
	}
	limit := m.storeLimit
	if limit == 0 {
		limit = MaxStoreBytes
	}
	if totalBytes > limit {
		return errors.New("package store size limit exceeded")
	}
	sort.Slice(next, func(i, j int) bool { return key(next[i].Type, next[i].ID) < key(next[j].Type, next[j].ID) })
	if len(next) > 128 {
		return errors.New("package count limit exceeded")
	}
	var specs []Spec
	for _, e := range next {
		specs = append(specs, Spec{ID: e.ID, Type: e.Type, Source: e.Source, Ref: e.Ref, Version: e.Version})
	}
	if err := writeJSON(r, "manifest.json", manifest{SchemaVersion: Schema, Scope: scope, Packages: specs}); err != nil {
		return err
	}
	if err := writeJSON(r, "lock.json", lockfile{SchemaVersion: Schema, Scope: scope, Packages: next}); err != nil {
		return err
	}
	if m.beforeSwap != nil {
		if err := m.beforeSwap(stage); err != nil {
			return err
		}
	}
	current, err := loadState(root, scope)
	if err != nil {
		return err
	}
	if len(current) != len(entries) {
		return errors.New("package state changed during transaction")
	}
	for i := range current {
		if current[i] != entries[i] {
			return errors.New("package state changed during transaction")
		}
	}
	// Close directory handles before rename for Windows compatibility.
	r.Close()
	rename := m.rename
	if rename == nil {
		rename = func(r *os.Root, a, b string) error { return r.Rename(a, b) }
	}
	_, err = root.Lstat("packages")
	hadOld := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return security.ErrDenied
	}
	if hadOld {
		if err := rename(root, "packages", ".packages-backup"); err != nil {
			return errors.New("cannot stage previous package state")
		}
	}
	if err := rename(root, stageName, "packages"); err != nil {
		if hadOld {
			if restoreErr := root.Rename(".packages-backup", "packages"); restoreErr != nil {
				return errors.New("package recovery required: previous state retained in .packages-backup")
			}
		}
		return errors.New("package promotion failed; previous state preserved")
	}
	if hadOld {
		if err := root.RemoveAll(".packages-backup"); err != nil {
			return errors.New("package installed; backup cleanup required before next mutation")
		}
	}
	return nil
}

func entryFromSnapshot(s snapshot, scope string) Entry {
	return Entry{ID: s.manifest.ID, Type: s.manifest.Type, Scope: scope, Source: s.source, Ref: s.ref, Version: s.manifest.Version, Commit: s.commit, SHA256: s.digest, License: s.manifest.License}
}

func (m *Manager) Add(ctx context.Context, kind, source, ref, scope string, trusted bool) (Result, error) {
	return m.install(ctx, kind, "", source, ref, scope, trusted, false)
}

func (m *Manager) install(ctx context.Context, kind, id, source, ref, scope string, trusted, update bool) (Result, error) {
	if !trusted {
		return Result{}, errors.New("package instructions and license require review; pass --trust after review")
	}
	if !validKind(kind) || (id != "" && security.ValidateID(id) != nil) {
		return Result{}, security.ErrDenied
	}
	base, err := m.base(scope)
	if err != nil {
		return Result{}, err
	}
	r, err := openBase(base, true)
	if err != nil {
		return Result{}, security.ErrDenied
	}
	defer r.Close()
	release, err := acquire(r)
	if err != nil {
		return Result{}, err
	}
	defer release()
	entries, err := loadState(r, scope)
	if err != nil {
		return Result{}, err
	}
	var previous *Entry
	if update {
		for _, e := range entries {
			if e.ID == id && e.Type == kind {
				copy := e
				previous = &copy
				break
			}
		}
		if previous == nil {
			return Result{}, errors.New("package not installed in requested scope")
		}
		source = previous.Source
		if ref == "" {
			ref = previous.Ref
		}
	}
	var s snapshot
	if _, ok := builtinID(source); ok {
		// Predefined packages carry their own version; a ref is only a check.
		s, err = loadBuiltin(ctx, source, kind, ref)
		s.ref = ""
	} else {
		canonical, isGit, cerr := canonicalSource(source)
		if cerr != nil {
			return Result{}, cerr
		}
		if isGit {
			s, err = loadGit(ctx, canonical, kind, ref, m.git)
		} else {
			s, err = loadLocal(ctx, canonical, kind, ref)
		}
	}
	if err != nil {
		return Result{}, err
	}
	if update && s.manifest.ID != id {
		return Result{}, errors.New("updated package identity changed")
	}
	next := entryFromSnapshot(s, scope)
	for _, e := range entries {
		if e.Type != kind || e.ID != next.ID {
			continue
		}
		if e == next {
			return Result{Entry: e, Unchanged: true, Omitted: s.omitted}, nil
		}
		if !update {
			return Result{}, errors.New("package ID collision; use explicit package update for the same source")
		}
		if e.Source != next.Source {
			return Result{}, errors.New("package source identity changed")
		}
	}
	if err := m.commit(r, scope, entries, &s, ""); err != nil {
		return Result{}, err
	}
	return Result{Entry: next, Omitted: s.omitted}, nil
}

func (m *Manager) Update(ctx context.Context, kind, id, ref, scope string, trusted bool) (Result, error) {
	return m.install(ctx, kind, id, "", ref, scope, trusted, true)
}

func (m *Manager) Remove(ctx context.Context, kind, id, scope string) (bool, error) {
	if !validKind(kind) || security.ValidateID(id) != nil {
		return false, security.ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	base, err := m.base(scope)
	if err != nil {
		return false, err
	}
	r, err := openBase(base, false)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, security.ErrDenied
	}
	defer r.Close()
	release, err := acquire(r)
	if err != nil {
		return false, err
	}
	defer release()
	entries, err := loadState(r, scope)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.ID == id && e.Type == kind {
			err := m.commit(r, scope, entries, nil, key(kind, id))
			return err == nil, err
		}
	}
	return false, nil
}

func (m *Manager) List(ctx context.Context, kind, selectedScope string) ([]Item, error) {
	if !validKind(kind) {
		return nil, security.ErrDenied
	}
	if selectedScope != "" && selectedScope != "project" && selectedScope != "global" {
		return nil, errors.New("invalid package list scope")
	}
	var items []Item
	for _, scope := range []string{"project", "global"} {
		if selectedScope != "" && selectedScope != scope {
			continue
		}
		if scope == "project" && m.Project == "" {
			continue
		}
		base, err := m.base(scope)
		if err != nil {
			return nil, err
		}
		r, err := openBase(base, false)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, security.ErrDenied
		}
		release, err := acquire(r)
		if err != nil {
			r.Close()
			return nil, err
		}
		entries, err := loadState(r, scope)
		release()
		r.Close()
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.Type == kind {
				items = append(items, Item{Entry: e, Path: filepath.Join(base, "packages", kind+"s", e.ID)})
			}
		}
	}
	// Overrides are user-owned project directories, never modified by this manager.
	if m.Project != "" && selectedScope != "global" {
		base := filepath.Join(m.Project, ".conmesh", "overrides", kind+"s")
		if err := security.CheckPath(base); err != nil {
			return nil, err
		}
		r, err := security.OpenRoot(base)
		if err == nil {
			entries, err := security.ReadDirBounded(r, ".", 128)
			if err != nil {
				r.Close()
				return nil, security.ErrDenied
			}
			for _, e := range entries {
				if security.ValidateID(e.Name()) != nil {
					r.Close()
					return nil, security.ErrDenied
				}
				files, err := readTree(r, e.Name(), MaxPackageBytes)
				if err != nil {
					r.Close()
					return nil, err
				}
				var pm PackageManifest
				if config.Decode(files[SourceManifest], &pm) != nil || pm.validate() != nil || pm.ID != e.Name() || pm.Type != kind {
					r.Close()
					return nil, errors.New("invalid project override manifest")
				}
				items = append(items, Item{Entry: Entry{ID: pm.ID, Type: kind, Scope: "override", Source: "project override", Version: pm.Version, License: pm.License, SHA256: digestFiles(files)}, Path: filepath.Join(base, e.Name())})
			}
			r.Close()
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, security.ErrDenied
		}
	}
	priority := map[string]int{"override": 0, "project": 1, "global": 2}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ID != items[j].ID {
			return items[i].ID < items[j].ID
		}
		return priority[items[i].Scope] < priority[items[j].Scope]
	})
	effective := map[string]string{}
	for i := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if scope, ok := effective[items[i].ID]; ok {
			items[i].ShadowedBy = scope
		} else {
			items[i].Effective = true
			effective[items[i].ID] = items[i].Scope
		}
	}
	return items, nil
}

func (e Entry) String() string { return fmt.Sprintf("%s %s %s", e.Type, e.ID, e.Scope) }

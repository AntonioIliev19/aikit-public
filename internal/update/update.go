// Package update verifies native releases before replacing the installed CLI.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/security"
)

const Repository = "AntonioIliev19/conmesh"
const MaxArtifactBytes = 128 << 20

// ReleasePublicKey is populated at reviewed build time with a base64 Ed25519 key.
// An empty key deliberately disables remote updates; repository config cannot set it.
var ReleasePublicKey string
var ErrTrust = errors.New("UPDATE_TRUST: no approved release signing key or invalid signed release")
var ErrInstall = errors.New("UPDATE_INSTALL: cannot safely replace executable; previous executable preserved or backup retained")
var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type Manifest struct {
	Schema     int    `json:"schema"`
	Repository string `json:"repository"`
	Version    string `json:"version"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Artifact   string `json:"artifact"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
}

type Client struct {
	key           ed25519.PublicKey
	base          string
	latestURL     string
	http          *http.Client
	beforePromote func() // deterministic failure/race injection in package tests only
}

func New() (*Client, error) {
	key, err := base64.StdEncoding.DecodeString(ReleasePublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, ErrTrust
	}
	return &Client{key: key, base: "https://github.com/" + Repository + "/releases/download/", latestURL: "https://api.github.com/repos/" + Repository + "/releases/latest", http: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// GitHub release assets redirect to its fixed asset host; never follow HTTP.
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Hostname() != "release-assets.githubusercontent.com" && req.URL.Hostname() != "github.com") {
			return ErrTrust
		}
		return nil
	}}}, nil
}
func (c *Client) get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ErrTrust
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("release download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > max {
		return nil, ErrTrust
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil || int64(len(data)) > max {
		return nil, ErrTrust
	}
	return data, nil
}
func Artifact(version, goos, arch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return "conmesh_" + version + "_" + goos + "_" + arch + ext
}
func (c *Client) Check(ctx context.Context, version string) (Manifest, error) {
	var m Manifest
	if !versionPattern.MatchString(version) || len(c.key) != ed25519.PublicKeySize {
		return m, ErrTrust
	}
	name := Artifact(version, runtime.GOOS, runtime.GOARCH) + ".json"
	data, err := c.get(ctx, c.base+version+"/"+name, 16384)
	if err != nil {
		return m, err
	}
	sig, err := c.get(ctx, c.base+version+"/"+name+".sig", ed25519.SignatureSize)
	if err != nil || !ed25519.Verify(c.key, data, sig) {
		return m, ErrTrust
	}
	if config.Decode(data, &m) != nil || m.Schema != 1 || m.Repository != Repository || m.Version != version || m.OS != runtime.GOOS || m.Arch != runtime.GOARCH || m.Artifact != Artifact(version, m.OS, m.Arch) || m.Size <= 0 || m.Size > MaxArtifactBytes {
		return Manifest{}, ErrTrust
	}
	hash, err := hex.DecodeString(m.SHA256)
	if err != nil || len(hash) != sha256.Size || hex.EncodeToString(hash) != m.SHA256 {
		return Manifest{}, ErrTrust
	}
	return m, nil
}

type receipt struct {
	Previous  string `json:"previous_sha256"`
	Installed string `json:"installed_sha256"`
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func installRoot(executable string) (*os.Root, string, error) {
	if !filepath.IsAbs(executable) || security.CheckPath(executable) != nil {
		return nil, "", ErrInstall
	}
	r, err := security.OpenRoot(filepath.Dir(executable))
	if err != nil {
		return nil, "", ErrInstall
	}
	name := filepath.Base(executable)
	info, err := r.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || security.CheckEntry(info) != nil {
		r.Close()
		return nil, "", ErrInstall
	}
	if err = r.Mkdir(".conmesh-update-lock", 0700); err != nil {
		r.Close()
		return nil, "", errors.New("update lock exists or install directory is not writable")
	}
	return r, name, nil
}
func unlock(r *os.Root) { r.Remove(".conmesh-update-lock"); r.Close() }
func read(r *os.Root, name string) ([]byte, error) {
	return security.ReadFileBounded(r, name, MaxArtifactBytes)
}
func write(r *os.Root, name string, data []byte, mode os.FileMode) error {
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// Apply verifies again even when called after Check. Cancellation before promotion
// leaves the current executable intact. A backup is retained for explicit rollback.
func (c *Client) Apply(ctx context.Context, version, executable string) error {
	m, err := c.Check(ctx, version)
	if err != nil {
		return err
	}
	data, err := c.get(ctx, c.base+version+"/"+m.Artifact, MaxArtifactBytes)
	if err != nil {
		return err
	}
	if int64(len(data)) != m.Size || digest(data) != m.SHA256 {
		return ErrTrust
	}
	r, name, err := installRoot(executable)
	if err != nil {
		return err
	}
	defer unlock(r)
	sweepDiscarded(r, name)
	backup, stage, meta := name+".previous", name+".staged", name+".update.json"
	old, err := read(r, name)
	if err != nil {
		return ErrInstall
	}
	if digest(old) == digest(data) {
		return ErrCurrent
	}
	if err := clearFinishedUpdate(r, name, digest(old)); err != nil {
		return err
	}
	originalInfo, err := r.Lstat(name)
	if err != nil {
		return ErrInstall
	}
	record, _ := json.Marshal(receipt{digest(old), digest(data)})
	if err = write(r, stage, data, 0755); err != nil {
		r.Remove(stage)
		return ErrInstall
	}
	defer r.Remove(stage)
	if err = write(r, meta, record, 0600); err != nil {
		r.Remove(meta)
		return ErrInstall
	}
	if err = ctx.Err(); err != nil {
		r.Remove(meta)
		return err
	}
	if c.beforePromote != nil {
		c.beforePromote()
	}
	currentInfo, statErr := r.Lstat(name)
	current, readErr := read(r, name)
	if statErr != nil || readErr != nil || !os.SameFile(originalInfo, currentInfo) || digest(current) != digest(old) {
		r.Remove(meta)
		return ErrInstall
	}
	if err = ctx.Err(); err != nil {
		r.Remove(meta)
		return err
	}
	if err = r.Rename(name, backup); err != nil {
		r.Remove(meta)
		return ErrInstall
	}
	if err = r.Rename(stage, name); err != nil {
		if r.Rename(backup, name) == nil {
			r.Remove(meta)
		}
		return ErrInstall
	}
	return nil
}

// ErrCurrent reports that the verified release is the executable already installed.
var ErrCurrent = errors.New("UPDATE_CURRENT: this release is already installed")

// discardedSuffix marks old executables moved aside because they were in use.
const discardedSuffix = ".old-"

// discard removes an old executable. Windows refuses to delete an executable
// that a running program uses (another open ConMesh menu, an MCP server, or
// this process itself) but allows renaming it, so such a file is moved aside to
// a unique name and deleted by a later update or rollback once it is free.
func discard(r *os.Root, name, file string) error {
	if err := r.Remove(file); err == nil || os.IsNotExist(err) {
		return nil
	}
	var id [6]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	return r.Rename(file, name+discardedSuffix+hex.EncodeToString(id[:]))
}

// sweepDiscarded deletes executables moved aside earlier that are no longer in
// use. Files still in use stay for a later run.
func sweepDiscarded(r *os.Root, name string) {
	entries, err := security.ReadDirBounded(r, ".", 4096)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), name+discardedSuffix) {
			continue
		}
		if info, err := r.Lstat(e.Name()); err == nil && info.Mode().IsRegular() {
			r.Remove(e.Name())
		}
	}
}

// clearFinishedUpdate removes what an earlier update or rollback left behind,
// so each update keeps exactly one rollback step. It acts only when the receipt
// proves the earlier operation finished:
//   - after an update, the receipt names the executable now installed and, if
//     the rollback copy is still there, that copy;
//   - after a rollback that could not delete the replaced executable, the
//     receipt names the executable now installed as the previous one and the
//     leftover staged file as the one that was installed.
//
// Any other leftover state is kept and reported for review.
func clearFinishedUpdate(r *os.Root, name, current string) error {
	backup, stage, meta := name+".previous", name+".staged", name+".update.json"
	var present []string
	exists := func(n string) bool {
		_, err := r.Lstat(n)
		if !os.IsNotExist(err) {
			present = append(present, n)
			return true
		}
		return false
	}
	hasBackup, hasStage, hasMeta := exists(backup), exists(stage), exists(meta)
	if len(present) == 0 {
		return nil
	}
	review := fmt.Errorf("UPDATE_STATE: update files next to %s do not match this installation (%s). Close other ConMesh windows and tools that run ConMesh, such as an MCP server, then run conmesh update --rollback or remove those files", name, strings.Join(present, ", "))
	if !hasMeta {
		return review
	}
	raw, err := security.ReadFileBounded(r, meta, 1024)
	var record receipt
	if err != nil || config.Decode(raw, &record) != nil {
		return review
	}
	matches := func(file, want string) bool {
		data, err := read(r, file)
		return err == nil && digest(data) == want
	}
	switch {
	case !hasStage && record.Installed == current:
		if hasBackup && (!matches(backup, record.Previous) || discard(r, name, backup) != nil) {
			return review
		}
	case hasStage && !hasBackup && record.Previous == current:
		if !matches(stage, record.Installed) || discard(r, name, stage) != nil {
			return review
		}
	default:
		return review
	}
	if r.Remove(meta) != nil {
		return review
	}
	return nil
}

// Rollback restores only the locally recorded previous executable. Installation
// directory write access is the trust boundary, just as for the executable itself.
func Rollback(ctx context.Context, executable string) error {
	r, name, err := installRoot(executable)
	if err != nil {
		return err
	}
	defer unlock(r)
	sweepDiscarded(r, name)
	backup, stage, meta := name+".previous", name+".staged", name+".update.json"
	raw, err := security.ReadFileBounded(r, meta, 1024)
	var record receipt
	if err != nil || config.Decode(raw, &record) != nil {
		return ErrInstall
	}
	old, err := read(r, backup)
	if err != nil || digest(old) != record.Previous {
		return ErrTrust
	}
	current, err := read(r, name)
	if err != nil || digest(current) != record.Installed {
		return ErrTrust
	}
	if _, err = r.Lstat(stage); !os.IsNotExist(err) {
		return ErrInstall
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = r.Rename(name, stage); err != nil {
		return ErrInstall
	}
	if err = r.Rename(backup, name); err != nil {
		r.Rename(stage, name)
		return ErrInstall
	}
	// The replaced executable is usually this running process; on Windows it is
	// moved aside and deleted by a later run. Cleanup failure never touches the
	// restored executable.
	if err = discard(r, name, stage); err != nil {
		return errors.New("UPDATE_CLEANUP: rollback restored executable; remove retained staged image after exit")
	}
	if r.Remove(meta) != nil {
		return errors.New("UPDATE_CLEANUP: rollback restored executable; receipt cleanup required")
	}
	return nil
}

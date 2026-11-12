package packages

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AntonioIliev19/aikit-public/internal/config"
	"github.com/AntonioIliev19/aikit-public/internal/security"
)

const (
	MaxFiles        = 512
	MaxFileBytes    = 1 << 20
	MaxPackageBytes = 16 << 20
	MaxStoreBytes   = 128 << 20
	MaxEntries      = 4096
	MaxDepth        = 32
	SourceManifest  = "aikit-package.json"
)

type PackageManifest struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	Type          string `json:"type"`
	Version       string `json:"version"`
	License       string `json:"license"`
}

func (p PackageManifest) validate() error {
	if p.SchemaVersion != 1 || security.ValidateID(p.ID) != nil || !validKind(p.Type) || !versionPattern.MatchString(p.Version) || !labelPattern.MatchString(p.License) {
		return errors.New("invalid package manifest")
	}
	return nil
}

type snapshot struct {
	manifest PackageManifest
	files    map[string][]byte
	digest   string
	source   string
	ref      string
	commit   string
	omitted  int
}

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,95}$`)
var labelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 .()+-]{0,127}$`)
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
var refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)

func validKind(kind string) bool { return kind == "agent" || kind == "skill" }

func canonicalSource(source string) (string, bool, error) {
	if strings.Contains(source, "://") {
		u, err := url.Parse(source)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Path == "" || strings.ContainsAny(u.Path, "\r\n\\") {
			return "", false, errors.New("package Git source must be a credential-free HTTPS URL")
		}
		u.Host = strings.ToLower(u.Host)
		return u.String(), true, nil
	}
	abs, err := filepath.Abs(source)
	if err != nil || strings.TrimSpace(source) == "" {
		return "", false, errors.New("invalid local package source")
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || security.CheckPath(abs) != nil {
		return "", false, errors.New("package source must be a safe directory; archives are not supported")
	}
	return filepath.Clean(abs), false, nil
}

func loadLocal(ctx context.Context, source, kind, version string) (snapshot, error) {
	r, err := security.OpenRoot(source)
	if err != nil {
		return snapshot{}, security.ErrDenied
	}
	defer r.Close()
	s := snapshot{files: map[string][]byte{}, source: source, ref: version}
	var policy security.Policy
	count, total, visited := 0, 0, 0
	seenPaths := map[string]bool{}
	var walk func(string) error
	walk = func(dir string) error {
		if strings.Count(dir, "/") >= MaxDepth {
			return errors.New("package directory depth limit exceeded")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := policy.LoadDirectory(r, dir); err != nil {
			return err
		}
		entries, err := security.ReadDirBounded(r, dir, MaxEntries)
		if err != nil {
			return errors.New("cannot list package directory")
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			visited++
			if visited > MaxEntries {
				return errors.New("package directory entry limit exceeded")
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			if dir != "." {
				name = dir + "/" + name
			}
			if security.ValidateRelative(name) != nil {
				return security.ErrDenied
			}
			folded := strings.ToLower(name)
			if seenPaths[folded] {
				return errors.New("case-insensitive package path collision")
			}
			seenPaths[folded] = true
			info, err := r.Lstat(name)
			if err != nil || security.CheckEntry(info) != nil {
				return security.ErrDenied
			}
			if policy.Denied(name, info.IsDir()) {
				s.omitted++
				continue
			}
			if info.IsDir() {
				if err := walk(name); err != nil {
					return err
				}
				continue
			}
			count++
			if count > MaxFiles || info.Size() > MaxFileBytes {
				return errors.New("package file limit exceeded")
			}
			data, err := readBounded(r, name, MaxFileBytes)
			if err != nil {
				return err
			}
			total += len(data)
			if total > MaxPackageBytes {
				return errors.New("package size limit exceeded")
			}
			if security.SensitiveContent(data) {
				s.omitted++
				continue
			}
			s.files[name] = data
		}
		return nil
	}
	if err := walk("."); err != nil {
		return snapshot{}, err
	}
	data, exists := s.files[SourceManifest]
	if !exists || config.Decode(data, &s.manifest) != nil || s.manifest.validate() != nil || s.manifest.Type != kind {
		return snapshot{}, errors.New("valid matching aikit-package.json is required")
	}
	if version != "" && s.manifest.Version != version {
		return snapshot{}, errors.New("package version does not match requested version")
	}
	if _, ok := s.files["LICENSE"]; !ok {
		if _, ok := s.files["LICENSE.md"]; !ok {
			return snapshot{}, errors.New("package must include its reviewed LICENSE or LICENSE.md")
		}
	}
	s.digest = digestFiles(s.files)
	return s, nil
}

func readBounded(root *os.Root, name string, max int64) ([]byte, error) {
	return security.ReadFileBounded(root, name, max)
}

func digestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		fmt.Fprintf(h, "%d:%s:%d:", len(name), name, len(files[name]))
		h.Write(files[name])
	}
	return hex.EncodeToString(h.Sum(nil))
}

type gitRunner func(context.Context, string, ...string) ([]byte, error)

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("Git output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	commandArgs := []string{"-c", "core.hooksPath=" + filepath.ToSlash(filepath.Join(dir, "no-hooks")), "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "credential.helper=", "-c", "http.followRedirects=false"}
	cmd := exec.CommandContext(ctx, "git", append(commandArgs, args...)...)
	cmd.Dir = dir
	for _, env := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(env, "=", 2)[0])
		if strings.HasPrefix(key, "GIT_") || key == "SSH_ASKPASS" {
			continue
		}
		cmd.Env = append(cmd.Env, env)
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	var stdout limitedBuffer
	stdout.max = 4 << 20
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 2 * time.Second
	if err := runContained(ctx, cmd); err != nil {
		return nil, errors.New("Git operation failed; check source/ref, connectivity and Git availability")
	}
	return stdout.Bytes(), nil
}

func validRef(ref string) bool {
	return refPattern.MatchString(ref) && !strings.Contains(ref, "..") && !strings.HasSuffix(ref, "/") && !strings.Contains(ref, "//") && !strings.HasSuffix(ref, ".lock")
}

func loadGit(ctx context.Context, source, kind, ref string, git gitRunner) (snapshot, error) {
	if !validRef(ref) {
		return snapshot{}, errors.New("Git packages require an explicit exact --version ref")
	}
	tmp, err := os.MkdirTemp("", "aikit-git-")
	if err != nil {
		return snapshot{}, errors.New("cannot stage Git package")
	}
	defer os.RemoveAll(tmp)
	// Resolve only our newly created OS temp root (macOS /var may be an alias).
	tmp, err = filepath.EvalSymlinks(tmp)
	if err != nil {
		return snapshot{}, errors.New("cannot resolve Git staging directory")
	}
	if _, err = git(ctx, tmp, "init", "--bare", "."); err != nil {
		return snapshot{}, err
	}
	if _, err = boundedFetch(ctx, tmp, git, "fetch", "--depth=1", "--no-tags", "--", source, ref); err != nil {
		return snapshot{}, err
	}
	resolved, err := git(ctx, tmp, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	commit := strings.TrimSpace(string(resolved))
	if err != nil || !commitPattern.MatchString(commit) {
		return snapshot{}, errors.New("Git ref did not resolve to a commit")
	}
	tree, err := git(ctx, tmp, "ls-tree", "-r", "-z", commit)
	if err != nil {
		return snapshot{}, err
	}
	materialized, err := os.MkdirTemp("", "aikit-source-")
	if err != nil {
		return snapshot{}, errors.New("cannot stage package files")
	}
	defer os.RemoveAll(materialized)
	materialized, err = filepath.EvalSymlinks(materialized)
	if err != nil {
		return snapshot{}, errors.New("cannot resolve package staging directory")
	}
	r, err := security.OpenRoot(materialized)
	if err != nil {
		return snapshot{}, err
	}
	defer r.Close()
	count, total := 0, 0
	seenPaths := map[string]string{}
	for _, raw := range bytes.Split(tree, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		header, name, ok := strings.Cut(string(raw), "\t")
		fields := strings.Fields(header)
		if !ok || len(fields) != 3 || security.ValidateRelative(name) != nil || name == ".gitmodules" || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return snapshot{}, security.ErrDenied
		}
		parts := strings.Split(name, "/")
		for i := 1; i <= len(parts); i++ {
			prefix := strings.Join(parts[:i], "/")
			folded := strings.ToLower(prefix)
			if previous, exists := seenPaths[folded]; exists && previous != prefix {
				return snapshot{}, errors.New("case-insensitive Git path collision")
			}
			seenPaths[folded] = prefix
		}
		if security.HardDenied(name, false) {
			continue
		}
		count++
		if count > MaxFiles {
			return snapshot{}, errors.New("package file limit exceeded")
		}
		sizeOutput, err := git(ctx, tmp, "cat-file", "-s", fields[2])
		size, parseErr := strconv.Atoi(strings.TrimSpace(string(sizeOutput)))
		if err != nil || parseErr != nil || size < 0 || size > MaxFileBytes || total+size > MaxPackageBytes {
			return snapshot{}, errors.New("Git package size limit exceeded")
		}
		data, err := git(ctx, tmp, "cat-file", "blob", fields[2])
		if err != nil || len(data) != size {
			return snapshot{}, errors.New("cannot read Git package object")
		}
		total += len(data)
		if err := r.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return snapshot{}, errors.New("cannot stage package directory")
		}
		f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return snapshot{}, errors.New("cannot stage package file")
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			return snapshot{}, errors.New("cannot stage package file")
		}
	}
	s, err := loadLocal(ctx, materialized, kind, "")
	if err != nil {
		return snapshot{}, err
	}
	s.source, s.ref, s.commit = source, ref, commit
	return s, nil
}

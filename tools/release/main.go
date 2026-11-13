// Command release prepares private native artifacts. It never publishes anything.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/AntonioIliev19/conmesh/internal/security"
	"github.com/AntonioIliev19/conmesh/internal/update"
)

type module struct {
	Path, Version, Sum, Dir string
	Main                    bool
	Replace                 *module
}

func command(args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	data, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Go release command failed: %s", args[0])
	}
	return data, nil
}
func put(dir, name string, data []byte) error {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("release output exists or is unwritable")
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func windowsInstaller(out, source, artifact string) error {
	for _, name := range []string{"install.ps1", "install.cmd"} {
		data, err := os.ReadFile(filepath.Join(source, "scripts", name))
		if err != nil {
			return fmt.Errorf("Windows installer source unavailable: %s", name)
		}
		if name == "install.cmd" {
			data = bytes.ReplaceAll(data, []byte("%~dp0conmesh.exe"), []byte("%~dp0"+artifact))
		}
		if err = put(out, name, data); err != nil {
			return err
		}
	}
	return put(out, "INSTALL.txt", []byte("Private Windows artifact; no public release or authenticity guarantee is implied.\r\nAfter verifying the artifact's source, double-click install.cmd or run:\r\n  install.cmd "+artifact+"\r\nThen restart the terminal application. No administrator or manual PATH setup is needed.\r\nPowerShell current-session install: & .\\install.ps1 -BinaryPath .\\"+artifact+"\r\nA local execution policy may require process-scoped Bypass; enterprise policy still applies.\r\n"))
}

func nativeTarget(env map[string]string) error {
	if env["GOOS"] != runtime.GOOS || env["GOARCH"] != runtime.GOARCH || env["CGO_ENABLED"] != "1" {
		return fmt.Errorf("release preparation requires matching native GOOS/GOARCH, CGO_ENABLED=1 and supported compiler")
	}
	return nil
}

// Arguments are canonical absolute paths validated before this containment check.
func outsideRepository(repo, path string) bool {
	if !strings.EqualFold(filepath.VolumeName(repo), filepath.VolumeName(path)) {
		return true
	}
	rel, err := filepath.Rel(repo, path)
	return err == nil && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
func readSigningKey(path, repo string) (ed25519.PrivateKey, error) {
	abs, err := filepath.Abs(path)
	if err != nil || security.CheckPath(abs) != nil {
		return nil, fmt.Errorf("unsafe owner signing key path")
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve owner signing key")
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve source root")
	}
	if !outsideRepository(repo, abs) {
		return nil, fmt.Errorf("signing key must be outside source repository")
	}
	root, err := security.OpenRoot(filepath.Dir(abs))
	if err != nil {
		return nil, fmt.Errorf("unsafe owner signing key directory")
	}
	defer root.Close()
	name := filepath.Base(abs)
	info, err := root.Lstat(name)
	if err != nil || security.CheckEntry(info) != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("owner signing key must be a private regular file")
	}
	data, err := security.ReadFileBounded(root, name, 4096)
	if err != nil {
		return nil, fmt.Errorf("cannot safely read owner signing key")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid owner Ed25519 key")
	}
	key := ed25519.PrivateKey(decoded)
	if !bytes.Equal(ed25519.NewKeyFromSeed(key.Seed()), key) {
		return nil, fmt.Errorf("inconsistent owner key")
	}
	return key, nil
}
func licenseFiles(dir string) ([]string, error) {
	files := []string{}
	visited := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		visited++
		if visited > 200000 {
			return fmt.Errorf("license walk entry limit")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("license source contains symlink")
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToUpper(d.Name())
		if strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "LICENCE") || strings.HasPrefix(name, "COPYING") || strings.HasPrefix(name, "NOTICE") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}
func inventory(out string) error {
	if _, err := command("mod", "download", "all"); err != nil {
		return err
	}
	raw, err := command("list", "-m", "-json", "all")
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	modules := []module{}
	for {
		var m module
		if err = dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if m.Replace != nil {
			return fmt.Errorf("release cannot use replaced modules")
		}
		if !m.Main {
			modules = append(modules, m)
		}
	}
	var notices strings.Builder
	notices.WriteString("# Third-party license texts\n\nGenerated from the exact downloaded module graph; includes nested native licenses.\nThis file does not license ConMesh itself. Review before redistribution.\n")
	components := []map[string]any{}
	for _, m := range modules {
		if m.Dir == "" {
			return fmt.Errorf("dependency license source unavailable")
		}
		files, err := licenseFiles(m.Dir)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("no license found for module %s", m.Path)
		}
		relative := []string{}
		for _, file := range files {
			info, err := os.Stat(file)
			if err != nil || info.Size() > 2<<20 {
				return fmt.Errorf("invalid dependency license size")
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(m.Dir, file)
			relative = append(relative, filepath.ToSlash(rel))
			fmt.Fprintf(&notices, "\n## %s %s — %s\n\n```text\n%s\n```\n", m.Path, m.Version, filepath.ToSlash(rel), data)
		}
		components = append(components, map[string]any{"type": "library", "bom-ref": m.Path + "@" + m.Version, "name": m.Path, "version": m.Version, "purl": "pkg:golang/" + m.Path + "@" + m.Version, "properties": []map[string]string{{"name": "go:module:h1", "value": m.Sum}, {"name": "conmesh:license-files", "value": strings.Join(relative, ",")}}})
	}
	goRootRaw, err := command("env", "GOROOT")
	if err != nil {
		return err
	}
	goRoot := strings.TrimSpace(string(goRootRaw))
	standard := []string{filepath.Join(goRoot, "LICENSE"), filepath.Join(goRoot, "PATENTS")}
	vendor, err := licenseFiles(filepath.Join(goRoot, "src", "vendor"))
	if err != nil {
		return err
	}
	standard = append(standard, vendor...)
	for _, file := range standard {
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(goRoot, file)
		fmt.Fprintf(&notices, "\n## Go toolchain %s — %s\n\n```text\n%s\n```\n", runtime.Version(), filepath.ToSlash(rel), data)
	}
	sbom := map[string]any{"bomFormat": "CycloneDX", "specVersion": "1.6", "version": 1, "metadata": map[string]any{"component": map[string]string{"type": "application", "name": "ConMesh"}, "properties": []map[string]string{{"name": "conmesh:scope", "value": "Complete Go module graph (includes build/test-only modules); native license texts accompany inventory; Go toolchain separately identified"}, {"name": "conmesh:go-version", "value": runtime.Version()}}}, "components": components}
	data, _ := json.MarshalIndent(sbom, "", "  ")
	if err = put(out, "sbom.cdx.json", append(data, '\n')); err != nil {
		return err
	}
	if err = put(out, "THIRD_PARTY_LICENSE_TEXTS.md", []byte(notices.String())); err != nil {
		return err
	}
	data, _ = json.MarshalIndent(modules, "", "  ")
	for i := range modules {
		modules[i].Dir = ""
	}
	data, _ = json.MarshalIndent(modules, "", "  ")
	if err = put(out, "dependencies.json", append(data, '\n')); err != nil {
		return err
	}
	graph, err := command("mod", "graph")
	if err != nil {
		return err
	}
	return put(out, "go-module-graph.txt", graph)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "release preparation:", err)
		os.Exit(1)
	}
}
func run() error {
	var out, version, keyFile string
	var inventoryOnly bool
	flag.StringVar(&out, "out", "", "new private staging directory")
	flag.StringVar(&version, "version", "", "vMAJOR.MINOR.PATCH")
	flag.StringVar(&keyFile, "signing-key", "", "owner-supplied base64 Ed25519 private key outside the repository; no key generation")
	flag.BoolVar(&inventoryOnly, "inventory-only", false, "generate dependency inventory/notices/SBOM without building")
	flag.Parse()
	if out == "" || flag.NArg() != 0 {
		return fmt.Errorf("require --out and no positional arguments")
	}
	if !inventoryOnly && !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		return fmt.Errorf("explicit release version required")
	}
	if err := os.Mkdir(out, 0700); err != nil {
		return fmt.Errorf("output directory must be new")
	}
	if err := inventory(out); err != nil {
		return err
	}
	if inventoryOnly {
		return nil
	}
	var key ed25519.PrivateKey
	publicKey := ""
	if keyFile != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		key, err = readSigningKey(keyFile, cwd)
		if err != nil {
			return err
		}
		publicKey = base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	}
	artifact := update.Artifact(version, runtime.GOOS, runtime.GOARCH)
	target := filepath.Join(out, artifact)
	flags := "-X main.version=" + version
	if publicKey != "" {
		flags += " -X github.com/AntonioIliev19/conmesh/internal/update.ReleasePublicKey=" + publicKey
	}
	envRaw, err := command("env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "CC", "GOVERSION")
	if err != nil {
		return err
	}
	var env map[string]string
	if json.Unmarshal(envRaw, &env) != nil {
		return fmt.Errorf("cannot read build target settings")
	}
	if err = nativeTarget(env); err != nil {
		return err
	}
	if _, err = command("build", "-trimpath", "-buildvcs=true", "-ldflags", flags, "-o", target, "./cmd/conmesh"); err != nil {
		return err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	m := update.Manifest{Schema: 1, Repository: update.Repository, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, Artifact: artifact, SHA256: hash(data), Size: int64(len(data))}
	manifest, _ := json.MarshalIndent(m, "", "  ")
	manifest = append(manifest, '\n')
	if err = put(out, artifact+".json", manifest); err != nil {
		return err
	}
	if key != nil {
		if err = put(out, artifact+".json.sig", ed25519.Sign(key, manifest)); err != nil {
			return err
		}
	}
	buildInfo, err := command("version", "-m", target)
	if err != nil {
		return err
	}
	if err = put(out, "build-info.txt", buildInfo); err != nil {
		return err
	}
	if err = put(out, "build-environment.json", envRaw); err != nil {
		return err
	}
	provenance := map[string]any{"schema": 1, "repository": update.Repository, "artifact": artifact, "sha256": m.SHA256, "go": runtime.Version(), "signing_public_key": publicKey, "publication": "BLOCKED: private preparation only; owner product LICENSE, approved signing trust and native/security gates required", "reproducibility": "Pinned modules, trimpath, recorded build/VCS/environment metadata; native compiler reproducibility not claimed"}
	p, _ := json.MarshalIndent(provenance, "", "  ")
	if err = put(out, "provenance.json", append(p, '\n')); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		if err = windowsInstaller(out, ".", artifact); err != nil {
			return err
		}
	}
	files, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	var checksums strings.Builder
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(out, f.Name()))
		if err != nil {
			return err
		}
		fmt.Fprintf(&checksums, "%s  %s\n", hash(data), f.Name())
	}
	if err = put(out, "SHA256SUMS", []byte(checksums.String())); err != nil {
		return err
	}
	if key != nil {
		return put(out, "SHA256SUMS.sig", ed25519.Sign(key, []byte(checksums.String())))
	}
	return nil
}

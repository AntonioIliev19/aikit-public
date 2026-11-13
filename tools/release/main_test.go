package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsInstallerIncludedWithoutOverwrite(t *testing.T) {
	out := t.TempDir()
	if err := windowsInstaller(out, filepath.Join("..", ".."), "conmesh_v0.0.0_windows_amd64.exe"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install.ps1", "install.cmd"} {
		want, err := os.ReadFile(filepath.Join("..", "..", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "install.cmd" {
			want = bytes.ReplaceAll(want, []byte("%~dp0conmesh.exe"), []byte("%~dp0conmesh_v0.0.0_windows_amd64.exe"))
		}
		got, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("installer %s missing or altered", name)
		}
	}
	guide, err := os.ReadFile(filepath.Join(out, "INSTALL.txt"))
	if err != nil || !strings.Contains(string(guide), "install.cmd conmesh_v0.0.0_windows_amd64.exe") {
		t.Fatal("installer guide does not identify the native artifact")
	}
	if err := windowsInstaller(out, filepath.Join("..", ".."), "another.exe"); err == nil {
		t.Fatal("existing installer overwritten")
	}
	if err := windowsInstaller(t.TempDir(), t.TempDir(), "conmesh.exe"); err == nil {
		t.Fatal("missing installer sources accepted")
	}
}

func TestPrivateOutputNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := put(dir, "SHA256SUMS", []byte("reviewed")); err != nil {
		t.Fatal(err)
	}
	if put(dir, "SHA256SUMS", []byte("replacement")) == nil {
		t.Fatal("overwrote reviewed artifact")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if string(data) != "reviewed" {
		t.Fatal("original lost")
	}
}

func TestNativeTargetAndSigningKeyBoundary(t *testing.T) {
	env := map[string]string{"GOOS": runtime.GOOS, "GOARCH": runtime.GOARCH, "CGO_ENABLED": "1"}
	if nativeTarget(env) != nil {
		t.Fatal("native target refused")
	}
	env["GOOS"] = "wrong"
	if nativeTarget(env) == nil {
		t.Fatal("mislabeled target accepted")
	}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	external, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	data := []byte(base64.StdEncoding.EncodeToString(key))
	path := filepath.Join(external, "owner-signing-fixture")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readSigningKey(path, repo); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(repo, "repo-controlled")
	os.WriteFile(inside, data, 0600)
	if _, err = readSigningKey(inside, repo); err == nil {
		t.Fatal("repo-controlled signing key accepted")
	}
	link := filepath.Join(external, "linked")
	if os.Symlink(inside, link) == nil {
		if _, err = readSigningKey(link, repo); err == nil {
			t.Fatal("linked repo key accepted")
		}
	}
	os.WriteFile(path, bytes.Repeat([]byte{'x'}, 4097), 0600)
	if _, err = readSigningKey(path, repo); err == nil {
		t.Fatal("unbounded signing key accepted")
	}
	if runtime.GOOS != "windows" {
		os.WriteFile(path, data, 0600)
		os.Chmod(path, 0644)
		if _, err = readSigningKey(path, repo); err == nil {
			t.Fatal("publicly readable owner key accepted")
		}
	}
}
func TestLicenseInventoryIncludesNativeNestedTexts(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "native"), 0700)
	for _, name := range []string{"LICENSE", "LICENSE-3RD-PARTY.md", "native/COPYING"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("original test license"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := licenseFiles(dir)
	if err != nil || len(files) != 3 {
		t.Fatalf("missing transitive/native license texts: %v", err)
	}
}

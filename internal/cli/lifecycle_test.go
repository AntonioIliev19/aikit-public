package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/packages"
)

func physicalTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func cliSource(t *testing.T) string {
	t.Helper()
	dir := physicalTemp(t)
	data, _ := json.Marshal(packages.PackageManifest{SchemaVersion: 1, ID: "example", Type: "skill", Version: "1", License: "Test-License"})
	for name, data := range map[string][]byte{packages.SourceManifest: data, "SKILL.md": []byte("Use project tests."), "LICENSE": []byte("Test-authored fixture.")} {
		os.WriteFile(filepath.Join(dir, name), data, 0600)
	}
	return dir
}

func TestCLIProjectLifecycle(t *testing.T) {
	dir, global, source := physicalTemp(t), filepath.Join(physicalTemp(t), "global"), cliSource(t)
	for _, tt := range []struct {
		args     []string
		contains string
		code     int
	}{
		{[]string{"init", "--harness", "claude,codex"}, "Indexing enabled: false", 0},
		{[]string{"init", "--index"}, "existing configuration preserved", 0},
		{[]string{"status"}, `"enabled": false`, 0},
		{[]string{"skill", "add", source}, "", 1},
		{[]string{"skill", "add", source, "--trust"}, "Installed skill example", 0},
		{[]string{"skill", "add", source, "--trust", "--global"}, "global scope", 0},
		{[]string{"skill", "list"}, "shadowed by project", 0},
		{[]string{"skill", "list", "--project"}, source, 0},
		{[]string{"package", "update", "example", "--type", "skill", "--trust"}, "Unchanged", 0},
		{[]string{"skill", "remove", "example"}, "removed", 0},
		{[]string{"skill", "list", "--global"}, "effective", 0},
		{[]string{"skill", "remove", "example", "--global"}, "removed", 0},
		{[]string{"skill", "list", "--all"}, "No packages", 0},
		{[]string{"skill", "list", "--global", "--project"}, "", 2},
		{[]string{"init", "--index", "--no-index"}, "", 2},
		{[]string{"skill", "add", source, "--unknown", "sensitive-input"}, "", 2},
	} {
		var out, errOut bytes.Buffer
		code := run(tt.args, &out, &errOut, "test", dir, global)
		if code != tt.code {
			t.Fatalf("command %s exit %d want %d; stderr %q", tt.args[0], code, tt.code, errOut.String())
		}
		if !strings.Contains(out.String(), tt.contains) {
			t.Fatalf("command %s output missing expected text", tt.args[0])
		}
		if strings.Contains(errOut.String(), "sensitive-input") {
			t.Fatal("diagnostic echoed untrusted argument")
		}
	}
}

func TestGlobalCommandsSurviveMalformedProject(t *testing.T) {
	dir, global, source := physicalTemp(t), filepath.Join(physicalTemp(t), "global"), cliSource(t)
	os.Mkdir(filepath.Join(dir, ".conmesh"), 0700)
	os.WriteFile(filepath.Join(dir, ".conmesh", "config.yaml"), []byte("malformed"), 0600)
	for _, args := range [][]string{{"skill", "add", source, "--global", "--trust"}, {"skill", "list", "--global"}, {"package", "update", "example", "--type", "skill", "--global", "--trust"}, {"skill", "remove", "example", "--global"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut, "test", dir, global); code != 0 {
			t.Fatalf("global command blocked by unrelated project: %q", errOut.String())
		}
	}
}

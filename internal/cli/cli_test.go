package cli

import (
	"bytes"
	"errors"
	"github.com/AntonioIliev19/aikit-public/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
		code int
	}{
		{"no args", nil, help, 0},
		{"help flag", []string{"--help"}, help, 0},
		{"short help", []string{"-h"}, help, 0},
		{"help command", []string{"help"}, help, 0},
		{"version", []string{"--version"}, "conmesh test-version\n", 0},
		{"doctor", []string{"doctor"}, doctor("test-version"), 0},
		{"unknown", []string{"sensitive-input"}, "", 2},
		{"extra", []string{"doctor", "sensitive-input"}, "", 2},
		{"extra version", []string{"--version", "sensitive-input"}, "", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := Run(tt.args, &out, &errOut, "test-version"); code != tt.code {
				t.Fatalf("exit code = %d, want %d", code, tt.code)
			}
			if out.String() != tt.want {
				t.Fatalf("stdout = %q, want %q", out.String(), tt.want)
			}
			if (errOut.Len() != 0) != (tt.code != 0) {
				t.Fatalf("unexpected stderr: %q", errOut.String())
			}
			if strings.Contains(errOut.String(), "sensitive-input") {
				t.Fatal("diagnostic echoed untrusted input")
			}
		})
	}
}

func TestHarnessDiscoveryAndUpdateGate(t *testing.T) {
	found := discoverHarnesses(func(name string) (string, error) {
		if name == "codex" {
			return "detected", nil
		}
		return "", errors.New("absent")
	})
	if len(found) != 1 || found[0] != "codex" {
		t.Fatal("incorrect discovery")
	}
	for _, args := range [][]string{{"update"}, {"update", "--check"}, {"update", "--rollback", "--version", "v1.0.0"}} {
		var out, errs bytes.Buffer
		if run(args, &out, &errs, "test", "", "") != 2 {
			t.Fatal("invalid update usage accepted")
		}
	}
	var out, errs bytes.Buffer
	if run([]string{"update", "--version", "v1.0.0", "--check"}, &out, &errs, "test", "", "") != 1 || !strings.Contains(errs.String(), "UPDATE_TRUST") {
		t.Fatal("missing signing trust accepted")
	}
	if !strings.Contains(doctor("test"), "Detection does not verify authentication") {
		t.Fatal("doctor overstated detection")
	}
}

func TestInitDiscoversAvailableHarnessWithoutRunningIt(t *testing.T) {
	bin := physicalTemp(t)
	name := "codex"
	if filepath.Separator == '\\' {
		name += ".exe"
		t.Setenv("PATHEXT", ".EXE")
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("not executable program bytes; discovery must never invoke"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dir := physicalTemp(t)
	var out, errs bytes.Buffer
	if code := run([]string{"init"}, &out, &errs, "test", dir, ""); code != 0 {
		t.Fatal(errs.String())
	}
	cfg, err := config.Load(dir)
	if err != nil || len(cfg.Harnesses) != 1 || cfg.Harnesses[0] != "codex" || cfg.Indexing.Enabled {
		t.Fatal("init discovery/config incorrect")
	}
	out.Reset()
	errs.Reset()
	if run([]string{"init", "--harness", "claude"}, &out, &errs, "test", dir, "") != 0 {
		t.Fatal(errs.String())
	}
	cfg, err = config.Load(dir)
	if err != nil || cfg.Harnesses[0] != "codex" {
		t.Fatal("init overwrote existing harness selection")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestOutputFailure(t *testing.T) {
	var errOut bytes.Buffer
	if code := Run([]string{"--version"}, brokenWriter{}, &errOut, "test"); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

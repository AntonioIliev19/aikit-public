package cli

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/AntonioIliev19/conmesh/internal/parser"
	"github.com/AntonioIliev19/conmesh/internal/update"
)

func discoverHarnesses(lookup func(string) (string, error)) []string {
	found := []string{}
	for _, name := range []string{"claude", "codex"} {
		if _, err := lookup(name); err == nil {
			found = append(found, name)
		}
	}
	return found
}

// doctorReport is the machine-readable form of `conmesh doctor`. Fields hold
// only what the text report states; nothing is probed beyond it.
type doctorReport struct {
	Version               string   `json:"version"`
	GoVersion             string   `json:"go_version"`
	OS                    string   `json:"os"`
	Arch                  string   `json:"arch"`
	GitDetected           bool     `json:"git_detected"`
	NativeParser          bool     `json:"native_parser"`
	HarnessesOnPath       []string `json:"harnesses_on_path"`
	ReleaseSigningTrust   bool     `json:"release_signing_trust"`
	IndexingRequiresOptIn bool     `json:"indexing_requires_opt_in"`
}

func doctorStatus(version string) doctorReport {
	_, gitErr := exec.LookPath("git")
	_, trustErr := update.New()
	return doctorReport{
		Version:               version,
		GoVersion:             runtime.Version(),
		OS:                    runtime.GOOS,
		Arch:                  runtime.GOARCH,
		GitDetected:           gitErr == nil,
		NativeParser:          parser.Available(),
		HarnessesOnPath:       discoverHarnesses(exec.LookPath),
		ReleaseSigningTrust:   trustErr == nil,
		IndexingRequiresOptIn: true,
	}
}

func doctor(version string) string {
	d := doctorStatus(version)
	return fmt.Sprintf("ConMesh doctor\nVersion: %s\nRuntime: %s %s/%s\nLocal packages: available\nHTTPS Git imports: Git executable detected: %t\nIndexing: explicit project opt-in required\nSQLite/FTS5: integrated\nTree-sitter native parser available: %t\nMCP: read-only stdio; project harness sync available\nHarness executables detected on PATH: %s\nDetection does not verify authentication or run a session.\nOptional semantics: requires opted-in installed local CPU model; inference not probed\nCLI release signing trust configured: %t\n", d.Version, d.GoVersion, d.OS, d.Arch, d.GitDetected, d.NativeParser, strings.Join(d.HarnessesOnPath, ","), d.ReleaseSigningTrust)
}

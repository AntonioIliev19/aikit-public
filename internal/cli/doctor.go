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
func doctor(version string) string {
	_, gitErr := exec.LookPath("git")
	_, trustErr := update.New()
	return fmt.Sprintf("ConMesh doctor\nVersion: %s\nRuntime: %s %s/%s\nLocal packages: available\nHTTPS Git imports: Git executable detected: %t\nIndexing: explicit project opt-in required\nSQLite/FTS5: integrated\nTree-sitter native parser available: %t\nMCP: read-only stdio; project harness sync available\nHarness executables detected on PATH: %s\nDetection does not verify authentication or run a session.\nOptional semantics: requires opted-in installed local CPU model; inference not probed\nCLI release signing trust configured: %t\n", version, runtime.Version(), runtime.GOOS, runtime.GOARCH, gitErr == nil, parser.Available(), strings.Join(discoverHarnesses(exec.LookPath), ","), trustErr == nil)
}

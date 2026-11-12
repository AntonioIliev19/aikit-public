package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AntonioIliev19/aikit-public/internal/parser"
)

func TestCLIIndexFlow(t *testing.T) {
	dir := physicalTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "code.ts"), []byte("export function CLIExample() { return 'orchidfixtureword'; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check := func(args []string, code int, contains string) {
		t.Helper()
		var out, stderr bytes.Buffer
		if got := run(args, &out, &stderr, "test", dir, ""); got != code {
			t.Fatalf("command %s exit %d: %s", args[0], got, stderr.String())
		}
		if !strings.Contains(out.String()+stderr.String(), contains) {
			t.Fatalf("command %s missing expected outcome", args[0])
		}
	}
	check([]string{"init", "--index"}, 0, "Indexing enabled: true")
	check([]string{"status"}, 0, `"present": false`)
	check([]string{"search", "orchidfixtureword"}, 1, "INDEX_ABSENT")
	if !parser.Available() {
		check([]string{"index"}, 1, "INDEX_UNAVAILABLE")
		return
	}
	check([]string{"index"}, 0, `"indexed"`)
	check([]string{"search", "orchidfixtureword", "--scope", "code.ts", "--limit", "1"}, 0, `"path": "code.ts"`)
	check([]string{"symbol", "CLIExample"}, 0, `"mechanism": "symbol"`)
	check([]string{"status"}, 0, `"present": true`)
	check([]string{"search", "orchidfixtureword", "--limit", "1000"}, 2, "invalid command")
}

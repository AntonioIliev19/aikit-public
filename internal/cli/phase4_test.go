package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/parser"
)

func TestCLIResearchKnowledgeAndOptionalSemantic(t *testing.T) {
	dir := physicalTemp(t)
	check := func(args []string, code int, contains string) {
		t.Helper()
		var out, diag bytes.Buffer
		if got := run(args, &out, &diag, "test", dir, ""); got != code || !strings.Contains(out.String()+diag.String(), contains) {
			t.Fatal("Phase 4 CLI outcome mismatch", args[0], got, diag.String())
		}
	}
	check([]string{"research-skill"}, 0, "name: conmesh-repository-research")
	check([]string{"init", "--index"}, 0, "Indexing enabled: true")
	check([]string{"knowledge", "scaffold"}, 0, "Created empty")
	check([]string{"knowledge", "scaffold"}, 1, "KNOWLEDGE_INVALID")
	check([]string{"knowledge", "list", "--scopes", "frontend/file.ts"}, 0, "[]")
	check([]string{"knowledge", "approve", "entry"}, 2, "invalid command")
	check([]string{"knowledge", "approve", "entry", "--reviewed-digest", "wrong"}, 1, "KNOWLEDGE_REVIEW")
	check([]string{"search", "example", "--semantic"}, 1, "SEMANTIC_UNAVAILABLE")
	check([]string{"index", "--semantic"}, 1, "SEMANTIC_UNAVAILABLE")
	check([]string{"search", "example", "--semantic", "--fallback"}, 2, "invalid command")
	if parser.Available() {
		if err := os.WriteFile(filepath.Join(dir, "code.ts"), []byte("export function Example(){return 'phasefourfixtureword';}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		check([]string{"index"}, 0, "indexed")
		check([]string{"references", "Example"}, 0, "lexical_reference_candidate_no_lsp")
		check([]string{"search", "phasefourfixtureword"}, 0, "fts5")
	}
}

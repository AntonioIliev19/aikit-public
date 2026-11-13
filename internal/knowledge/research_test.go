package knowledge

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedResearchManifestExample(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	trust, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := Store{Project: root, trustDir: filepath.Join(trust, "approvals")}
	if err := store.Scaffold(); err != nil {
		t.Fatal(err)
	}
	manifest, err := store.load(t.Context())
	if err != nil || manifest.Schema != 1 || len(manifest.Entries) != 0 {
		t.Fatalf("invalid research scaffold: %+v, %v", manifest, err)
	}

	// The sample files specify LF even when Git checked out the guide as CRLF.
	guide := strings.ReplaceAll(ResearchSkill, "\r\n", "\n")
	codeBlock := func(language string) []byte {
		t.Helper()
		_, remainder, found := strings.Cut(guide, "```"+language+"\n")
		if !found {
			t.Fatalf("embedded research instructions have no %s example", language)
		}
		body, _, closed := strings.Cut(remainder, "```")
		if !closed {
			t.Fatalf("embedded research instructions have an unclosed %s example", language)
		}
		return []byte(body)
	}
	write(t, root, ".conmesh/knowledge/frontend/architecture.md", codeBlock("markdown"))
	write(t, root, "src/app.ts", codeBlock("typescript"))
	write(t, root, ManifestPath, codeBlock("json"))

	const id = "frontend.architecture"
	doc, err := store.Read(t.Context(), id)
	if err != nil {
		t.Fatalf("embedded example failed live knowledge validation: %v", err)
	}
	if doc.Entry.Status != "proposed" || doc.EffectiveStatus != "proposed" || !DigestOK(doc.ReviewDigest) {
		t.Fatalf("example does not resolve to a reviewable proposal: %+v", doc)
	}
	summaries, err := store.List(t.Context(), Query{Scopes: []string{"src/app.ts"}})
	if err != nil || len(summaries) != 1 || summaries[0].ID != id {
		t.Fatalf("example is not discoverable by scope: %+v, %v", summaries, err)
	}
}

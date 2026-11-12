package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AntonioIliev19/aikit-public/internal/knowledge"
	"github.com/AntonioIliev19/aikit-public/internal/repository"
)

func TestSDKKnowledgeRoutingReadOnlyAndOutputBudget(t *testing.T) {
	_, dir := fixture(t, false)
	source := []byte("export function Example() {}\n")
	body := []byte("Observed proposed architecture.\n")
	for name, data := range map[string][]byte{"frontend/code.ts": source, ".aikit/knowledge/frontend.md": body} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if os.MkdirAll(filepath.Dir(full), 0700) != nil || os.WriteFile(full, data, 0600) != nil {
			t.Fatal("fixture write failed")
		}
	}
	entry := knowledge.Entry{ID: "frontend.architecture", File: "frontend.md", Digest: repository.Digest(body), Summary: "Frontend architecture", Kind: "architecture", Category: "normative", Status: "approved", Scopes: []string{"frontend/**"}, Tags: []string{"component"}, Sources: []knowledge.Source{{Path: "frontend/code.ts", StartLine: 1, EndLine: 1, Digest: repository.Digest(source)}}}
	save := func() {
		data, _ := json.Marshal(knowledge.Manifest{Schema: 1, Entries: []knowledge.Entry{entry}})
		if os.WriteFile(filepath.Join(dir, knowledge.ManifestPath), data, 0600) != nil {
			t.Fatal("manifest write failed")
		}
	}
	save()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session := client(t, ctx, dir)
	list, err := call(ctx, session, "aikit_get_architecture", map[string]any{"scopes": []string{"frontend/code.ts"}})
	if err != nil || len(list.Knowledge) != 1 || list.Freshness != "current_path_policy_checked_body_and_citation_digests_unchecked" {
		t.Fatal("SDK routing failed", err)
	}
	doc, err := call(ctx, session, "aikit_get_knowledge", map[string]any{"id": entry.ID})
	if err != nil || doc.Document == nil || doc.Document.EffectiveStatus != "proposed" {
		t.Fatal("manifest granted SDK authority", err)
	}
	for _, args := range []any{map[string]any{"id": entry.ID, "scopes": []string{"frontend"}}, map[string]any{"id": nil}, map[string]any{"id": "../outside"}} {
		v, err := call(ctx, session, "aikit_get_knowledge", args)
		if err == nil && v.Error == "" {
			t.Fatal("invalid knowledge arguments accepted")
		}
	}
	for i := 0; i < 15; i++ {
		entry.Scopes = append(entry.Scopes, strings.Repeat("a", 2100)+string(rune('a'+i))+"/**")
	}
	save()
	oversized, err := call(ctx, session, "aikit_get_knowledge", map[string]any{"id": entry.ID})
	if err != nil || oversized.Error != "OUTPUT_LIMIT" {
		t.Fatal("provenance output budget exceeded", err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if strings.Contains(tool.Name, "approve") || strings.Contains(tool.Name, "fallback") || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatal("mutable MCP tool exposed")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".aikit/state")); !os.IsNotExist(err) {
		t.Fatal("read-only knowledge created project state")
	}
}

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/knowledge"
)

func sha(data string) string { h := sha256.Sum256([]byte(data)); return hex.EncodeToString(h[:]) }

// isolateApprovals keeps knowledge approval receipts in a test directory.
func isolateApprovals(t *testing.T) {
	// Resolve links (macOS temp folders live behind /var -> /private/var);
	// ConMesh rightly refuses an approvals folder reached through a link.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
}

// writeKnowledge registers two entries; only the first is approved.
func writeKnowledge(t *testing.T, m *Manager) {
	t.Helper()
	source := "export function main() {}\n"
	write(t, m, "src/app.ts", source)
	docs := map[string]string{"frontend/architecture.md": "# Frontend\n\nStart new screens from `src/app.ts`.\n", "frontend/draft.md": "# Draft\n\nUnreviewed.\n"}
	var entries []knowledge.Entry
	for _, item := range []struct{ id, file, summary string }{{"frontend.architecture", "frontend/architecture.md", "Frontend application entry point"}, {"frontend.draft", "frontend/draft.md", "Draft guidance"}} {
		write(t, m, ".conmesh/knowledge/"+item.file, docs[item.file])
		entries = append(entries, knowledge.Entry{ID: item.id, File: item.file, Digest: sha(docs[item.file]), Summary: item.summary, Kind: "architecture", Category: "observed", Status: "proposed", Scopes: []string{"src/**"}, Tags: []string{}, Sources: []knowledge.Source{{Path: "src/app.ts", StartLine: 1, EndLine: 1, Digest: sha(source)}}})
	}
	manifest, _ := json.Marshal(knowledge.Manifest{Schema: 1, Entries: entries})
	write(t, m, knowledge.ManifestPath, string(manifest))
	store := knowledge.Store{Project: m.Project}
	doc, err := store.Read(context.Background(), "frontend.architecture")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(context.Background(), "frontend.architecture", doc.ReviewDigest); err != nil {
		t.Fatal(err)
	}
}

func TestApprovedKnowledgeIsPublishedForSessionsWithoutConMesh(t *testing.T) {
	isolateApprovals(t)
	m := fixture(t)
	const userRules = "# Team rules\n\nUse tabs.\n"
	write(t, m, "CLAUDE.md", userRules)
	writeKnowledge(t, m)
	plan, err := m.Sync(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	skill := string(bytesAt(t, m, ".claude/skills/conmesh-kb-frontend-architecture/SKILL.md"))
	for _, want := range []string{"name: conmesh-kb-frontend-architecture", "description: Frontend application entry point. Use when reading, changing or reviewing code in src/**.", "Start new screens from `src/app.ts`.", "- `src/app.ts` lines 1-1"} {
		if !strings.Contains(skill, want) {
			t.Fatalf("Claude skill missing %q:\n%s", want, skill)
		}
	}
	if _, err := os.Stat(filepath.Join(m.Project, ".agents", "skills", "conmesh-kb-frontend-architecture", "SKILL.md")); err != nil {
		t.Fatal("portable skill missing", err)
	}
	if _, err := os.Stat(filepath.Join(m.Project, ".claude", "skills", "conmesh-kb-frontend-draft")); !os.IsNotExist(err) {
		t.Fatal("unapproved knowledge was published")
	}
	claude := string(bytesAt(t, m, "CLAUDE.md"))
	if !strings.HasPrefix(claude, userRules) || !strings.Contains(claude, knowledgeBegin) || !strings.Contains(claude, "`.claude/skills/conmesh-kb-frontend-architecture/SKILL.md`") {
		t.Fatalf("CLAUDE.md block:\n%s", claude)
	}
	if agents := string(bytesAt(t, m, "AGENTS.md")); !strings.Contains(agents, "`.agents/skills/conmesh-kb-frontend-architecture/SKILL.md`") {
		t.Fatalf("AGENTS.md index:\n%s", agents)
	}
	if !strings.Contains(strings.Join(plan.Skipped, "\n"), "knowledge frontend.draft: not approved yet") {
		t.Fatal("unapproved entry not reported", plan.Skipped)
	}
	// Re-syncing is a no-op.
	if again, err := m.Sync(context.Background(), true, false); err != nil || len(again.Changes) != 0 {
		t.Fatal("second plan not empty", again.Changes, err)
	}
	// When the cited code changes, the guidance is withdrawn until re-approved,
	// and the user's CLAUDE.md is restored byte for byte.
	write(t, m, "src/app.ts", "export function main() { start() }\n")
	plan, err = m.Sync(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(bytesAt(t, m, "CLAUDE.md")); got != userRules {
		t.Fatalf("CLAUDE.md not restored:\n%q", got)
	}
	if _, err := os.Stat(filepath.Join(m.Project, ".claude", "skills", "conmesh-kb-frontend-architecture")); !os.IsNotExist(err) {
		t.Fatal("stale knowledge still published")
	}
	if !strings.Contains(strings.Join(plan.Skipped, "\n"), "frontend.architecture: document or cited source changed") {
		t.Fatal("stale entry not reported", plan.Skipped)
	}
}

func TestEditedKnowledgeBlockIsAConflict(t *testing.T) {
	isolateApprovals(t)
	m := fixture(t)
	write(t, m, "CLAUDE.md", "# Team rules\n")
	writeKnowledge(t, m)
	if _, err := m.Sync(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(bytesAt(t, m, "CLAUDE.md")), "Project knowledge", "Project notes", 1)
	write(t, m, "CLAUDE.md", edited)
	if _, err := m.Sync(context.Background(), true, false); !errors.Is(err, ErrConflict) {
		t.Fatal("edited managed block must not be overwritten", err)
	}
	// A block ConMesh never wrote is not adopted either.
	other := fixture(t)
	write(t, other, "CLAUDE.md", "# Rules\n\n"+knowledgeBegin+"forged\n"+knowledgeEnd)
	if _, err := other.Sync(context.Background(), true, false); !errors.Is(err, ErrConflict) {
		t.Fatal("unowned block adopted", err)
	}
}

func fixtureWith(t *testing.T, harnesses ...string) *Manager {
	t.Helper()
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Harnesses = harnesses
	if _, _, err := config.Init(project, cfg); err != nil {
		t.Fatal(err)
	}
	return &Manager{Project: project, Global: filepath.Join(project, "global"), Executable: filepath.Join(project, "conmesh test.exe")}
}

// addAgentEntry registers and approves a project agent built by research.
func addAgentEntry(t *testing.T, m *Manager) {
	t.Helper()
	store := knowledge.Store{Project: m.Project}
	raw, err := os.ReadFile(filepath.Join(m.Project, filepath.FromSlash(knowledge.ManifestPath)))
	if err != nil {
		t.Fatal(err)
	}
	var manifest knowledge.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	body := "# Frontend developer\n\nYou build features in this repository's frontend.\n\n1. Read the project guidance for the files you change.\n2. Run `npm test` before you finish.\n"
	write(t, m, ".conmesh/knowledge/agents/frontend-developer.md", body)
	manifest.Entries = append(manifest.Entries, knowledge.Entry{ID: "frontend-developer", File: "agents/frontend-developer.md", Digest: sha(body), Summary: "Builds and changes frontend features following this repository's conventions", Kind: "agent", Category: "normative", Status: "proposed", Scopes: []string{"src/**"}, Tags: []string{}, Sources: manifest.Entries[0].Sources})
	data, _ := json.Marshal(manifest)
	write(t, m, knowledge.ManifestPath, string(data))
	doc, err := store.Read(context.Background(), "frontend-developer")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(context.Background(), "frontend-developer", doc.ReviewDigest); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeForCursorAndCopilotAndProjectAgents(t *testing.T) {
	isolateApprovals(t)
	m := fixtureWith(t, "claude", "codex", "cursor", "copilot")
	writeKnowledge(t, m)
	addAgentEntry(t, m)
	plan, err := m.Sync(context.Background(), false, true)
	if err != nil {
		t.Fatal(err)
	}
	cursor := string(bytesAt(t, m, ".cursor/rules/conmesh-kb-frontend-architecture.mdc"))
	if !strings.HasPrefix(cursor, "---\ndescription: Frontend application entry point. Use when reading, changing or reviewing code in src/**.\nglobs: src/**\nalwaysApply: false\n---\n") || !strings.Contains(cursor, "Start new screens from `src/app.ts`.") {
		t.Fatalf("Cursor rule:\n%s", cursor)
	}
	copilot := string(bytesAt(t, m, ".github/instructions/conmesh-kb-frontend-architecture.instructions.md"))
	if !strings.HasPrefix(copilot, "---\napplyTo: src/**\ndescription: Frontend application entry point.") || !strings.Contains(copilot, "Start new screens") {
		t.Fatalf("Copilot instructions:\n%s", copilot)
	}
	for _, unexpected := range []string{".cursor/rules/conmesh-kb-frontend-developer.mdc", ".github/instructions/conmesh-kb-frontend-developer.instructions.md", ".claude/skills/conmesh-kb-frontend-developer"} {
		if _, err := os.Stat(filepath.Join(m.Project, filepath.FromSlash(unexpected))); !os.IsNotExist(err) {
			t.Fatalf("agent entry published as guidance: %s", unexpected)
		}
	}
	agent := string(bytesAt(t, m, ".claude/agents/conmesh-agent-frontend-developer.md"))
	for _, want := range []string{"name: conmesh-agent-frontend-developer", "Run `npm test` before you finish.", "## Project guidance to follow", "`.claude/skills/conmesh-kb-frontend-architecture/SKILL.md`"} {
		if !strings.Contains(agent, want) {
			t.Fatalf("Claude agent missing %q:\n%s", want, agent)
		}
	}
	codex := string(bytesAt(t, m, ".codex/agents/conmesh-agent-frontend-developer.toml"))
	if !strings.Contains(codex, "conmesh-agent-frontend-developer") || !strings.Contains(codex, ".agents/skills/conmesh-kb-frontend-architecture/SKILL.md") {
		t.Fatalf("Codex agent:\n%s", codex)
	}
	if claude := string(bytesAt(t, m, "CLAUDE.md")); !strings.Contains(claude, "- `conmesh-agent-frontend-developer`: Builds and changes frontend features") {
		t.Fatalf("CLAUDE.md does not list the agent:\n%s", claude)
	}
	joined := strings.Join(plan.Skipped, "\n")
	if !strings.Contains(joined, "cursor: project agents are published for Claude Code and Codex only") {
		t.Fatal("agent limitation not reported", plan.Skipped)
	}
	// Dropping Cursor from the configuration removes its rules.
	cfg, err := config.Load(m.Project)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Harnesses = []string{"claude", "codex", "copilot"}
	data, _ := json.Marshal(cfg)
	write(t, m, ".conmesh/config.yaml", string(data))
	if _, err := m.Sync(context.Background(), false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Project, ".cursor", "rules", "conmesh-kb-frontend-architecture.mdc")); !os.IsNotExist(err) {
		t.Fatal("Cursor rule left behind")
	}
	if again, err := m.Sync(context.Background(), true, false); err != nil || len(again.Changes) != 0 {
		t.Fatal("plan not stable", again.Changes, err)
	}
}

func TestScopesOverlap(t *testing.T) {
	for _, tc := range []struct {
		a, b []string
		want bool
	}{
		{[]string{"src/**"}, []string{"src/api/**"}, true},
		{[]string{"src/api/**"}, []string{"src/**"}, true},
		{[]string{"src/api/**"}, []string{"src/web/**"}, false},
		{[]string{"src/api/**"}, []string{"src/api-old/**"}, false},
		{[]string{"src/app.ts"}, []string{"src/**"}, true},
		{[]string{"src/app.ts"}, []string{"src/app.tsx"}, false},
	} {
		if got := scopesOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("scopesOverlap(%v, %v) = %v", tc.a, tc.b, got)
		}
	}
}

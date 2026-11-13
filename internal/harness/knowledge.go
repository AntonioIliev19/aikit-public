package harness

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/AntonioIliev19/conmesh/internal/knowledge"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// Approved ConMesh knowledge is published in each harness's own format: skills
// for Claude Code and Codex, scoped rules for Cursor, path-specific instructions
// for GitHub Copilot, plus an index in CLAUDE.md / AGENTS.md. Approved agent
// entries become custom agents for Claude Code and Codex. Sessions that know
// nothing about ConMesh (no MCP server, any model) then load this the way they
// load any other project guidance.

const knowledgeBegin = "<!-- BEGIN CONMESH PROJECT KNOWLEDGE -->\n"
const knowledgeEnd = "<!-- END CONMESH PROJECT KNOWLEDGE -->\n"

// knowledgeSkillName is the published name for a guidance entry ID.
func knowledgeSkillName(id string) string {
	name := "conmesh-kb-" + strings.NewReplacer(".", "-", "_", "-").Replace(id)
	if len(name) <= 64 {
		return name
	}
	return portableName("kb-" + id) // shortened with a digest suffix
}

// projectAgentName is the published agent name for an agent entry ID.
func projectAgentName(id string) string {
	name := "conmesh-agent-" + strings.NewReplacer(".", "-", "_", "-").Replace(id)
	if len(name) <= 64 {
		return name
	}
	return portableName("agent-" + id)
}

// skillsBase is where a harness reads project skills.
func skillsBase(h string) string {
	if h == "claude" {
		return ".claude/skills/"
	}
	return ".agents/skills/"
}

// knowledgePath is where harness h reads one published guidance entry.
func knowledgePath(h, id string) string {
	name := knowledgeSkillName(id)
	switch h {
	case "cursor":
		return ".cursor/rules/" + name + ".mdc"
	case "copilot":
		return ".github/instructions/" + name + ".instructions.md"
	}
	return skillsBase(h) + name + "/SKILL.md"
}

func isAgent(doc knowledge.Document) bool { return doc.Entry.Kind == "agent" }

func hasAgents(docs []knowledge.Document) bool {
	for _, doc := range docs {
		if isAgent(doc) {
			return true
		}
	}
	return false
}

func knowledgeDescription(doc knowledge.Document) string {
	description := strings.TrimSpace(doc.Entry.Summary)
	if !strings.HasSuffix(description, ".") {
		description += "."
	}
	description += " Use when reading, changing or reviewing code in " + strings.Join(doc.Entry.Scopes, ", ") + "."
	if len(description) > 1024 {
		description = description[:1021] + "..."
	}
	return description
}

// yamlScalar renders one safely quoted YAML value.
func yamlScalar(s string) (string, error) {
	out, err := yaml.Marshal(s)
	if err != nil {
		return "", ErrPackage
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// knowledgeBody is the published text of an entry: its document, where it
// applies, and the evidence it was approved against.
func knowledgeBody(doc knowledge.Document) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Source: .conmesh/knowledge/%s (ConMesh knowledge %q, approved). Edit that file and run `conmesh sync --trust`; changes here are overwritten. -->\n\n", doc.Entry.File, doc.Entry.ID)
	b.WriteString(strings.TrimSpace(strings.ReplaceAll(doc.Body, "\r\n", "\n")))
	b.WriteString("\n\n## Applies to\n\n")
	for _, scope := range doc.Entry.Scopes {
		fmt.Fprintf(&b, "- `%s`\n", scope)
	}
	b.WriteString("\n## Evidence\n\nThis guidance was checked against these files when it was approved. If they have changed a lot, verify before relying on it.\n\n")
	for _, source := range doc.Entry.Sources {
		fmt.Fprintf(&b, "- `%s` lines %d-%d\n", source.Path, source.StartLine, source.EndLine)
	}
	return b.String()
}

// renderKnowledge publishes each approved guidance entry in harness h's
// format. Agent entries are published by renderProjectAgents.
func renderKnowledge(h string, docs []knowledge.Document, files map[string][]byte) error {
	for _, doc := range docs {
		if isAgent(doc) {
			continue
		}
		path := knowledgePath(h, doc.Entry.ID)
		if _, exists := files[path]; exists {
			return ErrConflict
		}
		description, err := yamlScalar(knowledgeDescription(doc))
		if err != nil {
			return err
		}
		var front string
		switch h {
		case "cursor":
			// Cursor reads comma-separated, unquoted globs; scopes are validated paths.
			front = "description: " + description + "\nglobs: " + strings.Join(doc.Entry.Scopes, ", ") + "\nalwaysApply: false\n"
		case "copilot":
			applyTo, err := yamlScalar(strings.Join(doc.Entry.Scopes, ","))
			if err != nil {
				return err
			}
			front = "applyTo: " + applyTo + "\ndescription: " + description + "\n"
		default:
			front = "name: " + knowledgeSkillName(doc.Entry.ID) + "\ndescription: " + description + "\n"
		}
		files[path] = []byte("---\n" + front + "---\n" + header + knowledgeBody(doc))
	}
	return nil
}

// scopesOverlap reports whether two scope lists can match the same file.
func scopesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			px, py := strings.TrimSuffix(x, "/**"), strings.TrimSuffix(y, "/**")
			if px == py || strings.HasSuffix(y, "/**") && strings.HasPrefix(px, py+"/") || strings.HasSuffix(x, "/**") && strings.HasPrefix(py, px+"/") {
				return true
			}
		}
	}
	return false
}

// renderProjectAgents publishes approved agent entries as custom agents for
// Claude Code and Codex. Each agent is pointed at the published guidance that
// covers its part of the codebase, so it works from the reviewed knowledge.
func renderProjectAgents(h string, docs []knowledge.Document, files map[string][]byte) error {
	for _, doc := range docs {
		if !isAgent(doc) {
			continue
		}
		name := projectAgentName(doc.Entry.ID)
		body := knowledgeBody(doc)
		var related []string
		for _, other := range docs {
			if !isAgent(other) && scopesOverlap(doc.Entry.Scopes, other.Entry.Scopes) {
				related = append(related, fmt.Sprintf("- %s: `%s`", strings.TrimSpace(other.Entry.Summary), knowledgePath(h, other.Entry.ID)))
			}
		}
		if len(related) > 0 {
			body += "\n## Project guidance to follow\n\nBefore you plan, read the files that cover the code you are about to change, and follow them:\n\n" + strings.Join(related, "\n") + "\n"
		}
		description := knowledgeDescription(doc)
		if h == "claude" {
			path := ".claude/agents/" + name + ".md"
			if _, exists := files[path]; exists {
				return ErrConflict
			}
			meta, err := yaml.Marshal(map[string]string{"name": name, "description": description})
			if err != nil {
				return ErrPackage
			}
			files[path] = []byte("---\n" + string(meta) + "---\n" + header + body)
			continue
		}
		path := ".codex/agents/" + name + ".toml"
		if _, exists := files[path]; exists {
			return ErrConflict
		}
		data, err := toml.Marshal(map[string]string{"name": name, "description": description, "developer_instructions": strings.TrimSpace(header + body)})
		if err != nil {
			return ErrPackage
		}
		files[path] = append([]byte("# Generated by CONMESH; managed project integration.\n"), data...)
	}
	return nil
}

// knowledgeIndex is the instruction-file section that points any session to
// the published guidance and agents. It is empty when nothing is approved.
func knowledgeIndex(h string, docs []knowledge.Document) string {
	var guidance, agents []string
	for _, doc := range docs {
		scopes := "`" + strings.Join(doc.Entry.Scopes, "`, `") + "`"
		if isAgent(doc) {
			agents = append(agents, fmt.Sprintf("- `%s`: %s (%s)", projectAgentName(doc.Entry.ID), strings.TrimSpace(doc.Entry.Summary), scopes))
			continue
		}
		guidance = append(guidance, fmt.Sprintf("- %s (%s): `%s`", strings.TrimSpace(doc.Entry.Summary), scopes, knowledgePath(h, doc.Entry.ID)))
	}
	if len(guidance) == 0 && len(agents) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Project knowledge\n\n")
	if len(guidance) > 0 {
		b.WriteString("Reviewed, source-cited guidance about this repository's architecture, patterns and conventions. Before planning or changing code in a listed scope, read the matching file and follow it; it takes precedence over general habits. If the code now contradicts it, say so instead of silently diverging.\n\n")
		b.WriteString(strings.Join(guidance, "\n") + "\n")
	}
	if len(agents) > 0 {
		if len(guidance) > 0 {
			b.WriteString("\n")
		}
		b.WriteString("Project agents built from this guidance. Delegate matching work to them when your tool supports custom agents:\n\n")
		b.WriteString(strings.Join(agents, "\n") + "\n")
	}
	return b.String()
}

// reconcileKnowledgeBlock adds, replaces or removes the managed knowledge block
// in a user-owned instruction file. owned is the hash of the block ConMesh last
// wrote; any other block, or an edited one, is a conflict. It returns the new
// file content and the new block hash ("" when no block remains).
func reconcileKnowledgeBlock(data []byte, owned, index string) ([]byte, string, error) {
	start := bytes.Index(data, []byte("\n"+knowledgeBegin))
	beginCount, endCount := bytes.Count(data, []byte(knowledgeBegin)), bytes.Count(data, []byte(knowledgeEnd))
	out := data
	if owned != "" {
		finish := bytes.Index(data, []byte(knowledgeEnd))
		if start < 0 || finish < start || beginCount != 1 || endCount != 1 {
			return nil, "", ErrConflict
		}
		finish += len(knowledgeEnd)
		if digest(data[start:finish]) != owned {
			return nil, "", ErrConflict
		}
		out = append(append([]byte{}, data[:start]...), data[finish:]...)
	} else if beginCount != 0 || endCount != 0 {
		return nil, "", ErrConflict
	}
	if index == "" {
		return out, "", nil
	}
	block := []byte("\n" + knowledgeBegin + header + index + knowledgeEnd)
	return append(out, block...), digest(block), nil
}

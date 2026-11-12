package cli

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/AntonioIliev19/aikit-public/internal/knowledge"
)

func researchSkill() string { return knowledge.ResearchSkill }
func knowledgeCommand(ctx context.Context, project string, args []string) (string, error) {
	if len(args) == 0 {
		return "", errUsage
	}
	store := knowledge.Store{Project: project}
	var value any
	switch args[0] {
	case "scaffold":
		if len(args) != 1 {
			return "", errUsage
		}
		if err := store.Scaffold(); err != nil {
			return "", err
		}
		return "Created empty proposed-knowledge manifest; existing files are preserved.\n", nil
	case "list":
		opts, err := parseOptions(args[1:], map[string]bool{"scopes": true, "tags": true, "kind": true, "limit": true})
		if err != nil || len(opts.positionals) != 0 {
			return "", errUsage
		}
		q := knowledge.Query{Kind: opts.values["kind"]}
		if opts.has("scopes") {
			q.Scopes = strings.Split(opts.values["scopes"], ",")
		}
		if opts.has("tags") {
			q.Tags = strings.Split(opts.values["tags"], ",")
		}
		if opts.has("limit") {
			q.Limit, err = strconv.Atoi(opts.values["limit"])
			if err != nil || q.Limit < 1 || q.Limit > 50 {
				return "", errUsage
			}
		}
		value, err = store.List(ctx, q)
		if err != nil {
			return "", err
		}
	case "show":
		if len(args) != 2 {
			return "", errUsage
		}
		doc, err := store.Read(ctx, args[1])
		if err != nil {
			return "", err
		}
		value = doc
	case "approve":
		opts, err := parseOptions(args[1:], map[string]bool{"reviewed-digest": true})
		if err != nil || len(opts.positionals) != 1 || !opts.has("reviewed-digest") {
			return "", errUsage
		}
		if err := store.Approve(ctx, opts.positionals[0], opts.values["reviewed-digest"]); err != nil {
			return "", err
		}
		return "Approved reviewed entry/document/citation digests for this user and project.\n", nil
	default:
		return "", errUsage
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data) + "\n", nil
}

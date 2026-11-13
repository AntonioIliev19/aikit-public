package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/AntonioIliev19/conmesh/internal/config"
	"github.com/AntonioIliev19/conmesh/internal/knowledge"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type knowledgeArgs struct {
	ID     string   `json:"id,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Limit  int      `json:"limit,omitempty"`
}

func addKnowledgeTools(server *sdk.Server, project string) {
	for _, name := range []string{"conmesh_get_architecture", "conmesh_get_patterns", "conmesh_get_examples", "conmesh_get_knowledge"} {
		properties := map[string]any{"scopes": map[string]any{"type": "array", "maxItems": 16, "items": map[string]any{"type": "string", "maxLength": 4096}}, "tags": map[string]any{"type": "array", "maxItems": 16, "items": map[string]any{"type": "string", "maxLength": 64}}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}}
		input := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
		description := "List scoped proposed knowledge summaries lazily; document/citation freshness and effective approval are checked by conmesh_get_knowledge."
		if name == "conmesh_get_knowledge" {
			input = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id"}, "properties": map[string]any{"id": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}}}
			description = "Read one bounded authorized knowledge document with current source citations, review digest and user-local effective approval status."
		}
		server.AddTool(&sdk.Tool{Name: name, Description: description, InputSchema: input, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			raw := req.Params.Arguments
			if len(raw) == 0 {
				raw = json.RawMessage(`{}`)
			}
			var args knowledgeArgs
			var fields map[string]json.RawMessage
			if len(raw) > 8192 || config.Decode(raw, &args) != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
				return result(response{Error: "INVALID_ARGUMENTS"}), nil
			}
			for _, v := range fields {
				if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
					return result(response{Error: "INVALID_ARGUMENTS"}), nil
				}
			}
			store := knowledge.Store{Project: project}
			if name == "conmesh_get_knowledge" {
				if len(fields) != 1 || args.ID == "" {
					return result(response{Error: "INVALID_ARGUMENTS"}), nil
				}
				doc, err := store.Read(ctx, args.ID)
				if err != nil {
					return result(response{Error: errorCode(err)}), nil
				}
				return result(response{Document: &doc, Freshness: "document_and_all_source_digests_checked"}), nil
			}
			if _, present := fields["id"]; present {
				return result(response{Error: "INVALID_ARGUMENTS"}), nil
			}
			if _, present := fields["limit"]; present && (args.Limit < 1 || args.Limit > 50) {
				return result(response{Error: "INVALID_ARGUMENTS"}), nil
			}
			kind := "architecture"
			if name == "conmesh_get_patterns" {
				kind = "pattern"
			}
			if name == "conmesh_get_examples" {
				kind = "example"
			}
			items, err := store.List(ctx, knowledge.Query{Scopes: args.Scopes, Tags: args.Tags, Limit: args.Limit, Kind: kind})
			if err != nil {
				return result(response{Error: errorCode(err)}), nil
			}
			v := response{Knowledge: items, Freshness: "current_path_policy_checked_body_and_citation_digests_unchecked"}
			for {
				data, _ := json.Marshal(v)
				if len(data) <= MaxOutputBytes || len(v.Knowledge) == 0 {
					break
				}
				v.Knowledge = v.Knowledge[:len(v.Knowledge)-1]
				v.Truncated = true
			}
			return result(v), nil
		})
	}
}

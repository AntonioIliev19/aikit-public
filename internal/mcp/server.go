// Package mcp is a thin read-only protocol adapter over project-bound Core.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/AntonioIliev19/aikit-public/internal/config"
	"github.com/AntonioIliev19/aikit-public/internal/embeddings"
	"github.com/AntonioIliev19/aikit-public/internal/index"
	"github.com/AntonioIliev19/aikit-public/internal/knowledge"
	"github.com/AntonioIliev19/aikit-public/internal/repository"
	"github.com/AntonioIliev19/aikit-public/internal/security"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const MaxOutputBytes = 24 << 10

type queryArgs struct {
	Query       string `json:"query"`
	Scope       string `json:"scope,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Semantic    bool   `json:"semantic,omitempty"`
	hasSemantic bool
}
type response struct {
	Knowledge []knowledge.Summary `json:"knowledge,omitempty"`
	Document  *knowledge.Document `json:"document,omitempty"`
	Hybrid    *index.HybridResult `json:"hybrid,omitempty"`
	Evidence  []index.Evidence    `json:"evidence,omitempty"`
	Status    *index.Status       `json:"index,omitempty"`
	Freshness string              `json:"freshness,omitempty"`
	Truncated bool                `json:"truncated,omitempty"`
	Error     string              `json:"error,omitempty"`
}

func decodeArgs(raw []byte, status bool) (queryArgs, error) {
	var args queryArgs
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return args, index.ErrQuery
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return args, index.ErrQuery
		}
		name, ok := key.(string)
		if !ok || seen[name] || status {
			return args, index.ErrQuery
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return args, index.ErrQuery
		}
		switch name {
		case "query":
			if json.Unmarshal(value, &args.Query) != nil {
				return args, index.ErrQuery
			}
		case "scope":
			if json.Unmarshal(value, &args.Scope) != nil {
				return args, index.ErrQuery
			}
		case "limit":
			if json.Unmarshal(value, &args.Limit) != nil || args.Limit < 1 || args.Limit > index.MaxResults {
				return args, index.ErrQuery
			}
		case "semantic":
			args.hasSemantic = true
			if json.Unmarshal(value, &args.Semantic) != nil {
				return args, index.ErrQuery
			}
		default:
			return args, index.ErrQuery
		}
	}
	if _, err := decoder.Token(); err != nil {
		return args, index.ErrQuery
	}
	if _, err := decoder.Token(); err != io.EOF {
		return args, index.ErrQuery
	}
	if !status && !seen["query"] {
		return args, index.ErrQuery
	}
	return args, nil
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, embeddings.ErrUnavailable):
		return "SEMANTIC_UNAVAILABLE"
	case errors.Is(err, embeddings.ErrIdentity):
		return "SEMANTIC_IDENTITY"
	case errors.Is(err, knowledge.ErrManifest):
		return "INVALID_ARGUMENTS"
	case errors.Is(err, knowledge.ErrAbsent):
		return "KNOWLEDGE_ABSENT"
	case errors.Is(err, knowledge.ErrReview):
		return "KNOWLEDGE_REVIEW"
	case errors.Is(err, index.ErrQuery):
		return "INVALID_ARGUMENTS"
	case errors.Is(err, index.ErrDisabled):
		return "INDEX_DISABLED"
	case errors.Is(err, index.ErrAbsent):
		return "INDEX_ABSENT"
	case errors.Is(err, repository.ErrStale):
		return "INDEX_STALE"
	case errors.Is(err, security.ErrDenied):
		return "SECURITY_DENIED"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "CANCELED"
	default:
		return "INDEX_UNAVAILABLE"
	}
}

func result(v response) *sdk.CallToolResult {
	data, _ := json.Marshal(v)
	if len(data) > MaxOutputBytes {
		v = response{Error: "OUTPUT_LIMIT"}
		data, _ = json.Marshal(v)
	}
	return &sdk.CallToolResult{IsError: v.Error != "", Content: []sdk.Content{&sdk.TextContent{Text: string(data)}}, StructuredContent: v}
}

// New never enables indexing or opens a database. Core request arguments cannot
// change the root selected by the trusted launcher.
func New(project, version string) (*sdk.Server, error) {
	if _, err := config.Load(project); err != nil {
		return nil, errors.New("MCP project configuration unavailable")
	}
	core, err := index.New(project)
	if err != nil {
		return nil, errors.New("MCP project root unavailable")
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "aikit", Version: version}, nil)
	addKnowledgeTools(server, project)
	schema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"}, "properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "scope": map[string]any{"type": "string", "maxLength": 4096}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}}}
	for _, name := range []string{"aikit_search_codebase", "aikit_find_symbol", "aikit_find_reference_candidates", "aikit_index_status"} {
		input := any(schema)
		if name == "aikit_search_codebase" {
			input = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"query"}, "properties": map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 256}, "scope": map[string]any{"type": "string", "maxLength": 4096}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}, "semantic": map[string]any{"type": "boolean"}}}
		}
		description := "Search authorized indexed code; returned files are digest-checked; additions elsewhere may require reindexing."
		if name == "aikit_find_symbol" {
			description = "Find exact indexed symbol names with authorized source evidence."
		}
		if name == "aikit_find_reference_candidates" {
			description = "Find approximate lexical reference candidates, including possible declarations; no LSP, type resolution or completeness guarantee."
		}
		if name == "aikit_index_status" {
			input = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}}
			description = "Read last committed index counts and timestamp; does not scan for repository freshness."
		}
		server.AddTool(&sdk.Tool{Name: name, Description: description, InputSchema: input, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true}}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if len(req.Params.Arguments) > 8192 {
				return result(response{Error: "INVALID_ARGUMENTS"}), nil
			}
			args, argErr := decodeArgs(req.Params.Arguments, name == "aikit_index_status")
			if argErr != nil || args.hasSemantic && name != "aikit_search_codebase" {
				return result(response{Error: "INVALID_ARGUMENTS"}), nil
			}
			if name == "aikit_index_status" {
				status, err := core.Status(ctx)
				if err != nil {
					return result(response{Error: errorCode(err)}), nil
				}
				return result(response{Status: &status, Freshness: "last_committed_counts_not_repository_scan"}), nil
			}
			query := index.Query{Text: args.Query, Scope: args.Scope, Limit: args.Limit}
			var evidence []index.Evidence
			var err error
			if name == "aikit_search_codebase" {
				if args.Semantic {
					v, err := core.Hybrid(ctx, query)
					if err != nil {
						return result(response{Error: errorCode(err)}), nil
					}
					return result(response{Hybrid: &v, Freshness: "authorized_returned_source_digests_checked_bounded_candidates"}), nil
				}
				evidence, err = core.Search(ctx, query)
			} else if name == "aikit_find_reference_candidates" {
				evidence, err = core.FindReferenceCandidates(ctx, query)
			} else {
				evidence, err = core.FindSymbol(ctx, query)
			}
			if err != nil {
				return result(response{Error: errorCode(err)}), nil
			}
			v := response{Evidence: evidence, Freshness: "returned_source_digests_checked_not_repository_scan"}
			for {
				data, _ := json.Marshal(v)
				if len(data) <= MaxOutputBytes {
					break
				}
				v.Truncated = true
				v.Evidence = v.Evidence[:len(v.Evidence)-1]
			}
			return result(v), nil
		})
	}
	return server, nil
}

func Serve(ctx context.Context, project, version string, transport sdk.Transport) error {
	server, err := New(project, version)
	if err != nil {
		return err
	}
	if err := server.Run(ctx, transport); err != nil {
		return errors.New("MCP transport stopped")
	}
	return nil
}

package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AntonioIliev19/conmesh/internal/index"
)

func bulkyEvidence(n int) []index.Evidence {
	out := make([]index.Evidence, n)
	for i := range out {
		out[i] = index.Evidence{Path: "src/file.ts", StartLine: i + 1, EndLine: i + 1, Snippet: strings.Repeat("x", index.MaxSnippetBytes), Mechanism: "hybrid_rrf", Digest: strings.Repeat("a", 64)}
	}
	return out
}

func TestHybridOutputIsTrimmedNotRejected(t *testing.T) {
	hybrid := &index.HybridResult{Evidence: bulkyEvidence(10), Candidates: 10}
	v := fit(response{Hybrid: hybrid, Freshness: "f"})
	data, _ := json.Marshal(v)
	if len(data) > MaxOutputBytes || !v.Truncated || v.Error != "" || v.Hybrid == nil || len(v.Hybrid.Evidence) == 0 || len(v.Hybrid.Evidence) >= 10 {
		t.Fatal("hybrid output not trimmed", len(data), v.Truncated, v.Error)
	}
	if len(hybrid.Evidence) != 10 || v.Hybrid.Candidates != 10 {
		t.Fatal("caller result mutated or metadata lost")
	}
	r := result(v)
	if r.IsError {
		t.Fatal("trimmed hybrid result reported as error")
	}
	if got := r.Content[0]; got == nil {
		t.Fatal("missing content")
	}
}

func TestLexicalOutputTrimAndSmallResultsUntouched(t *testing.T) {
	v := fit(response{Evidence: bulkyEvidence(12)})
	data, _ := json.Marshal(v)
	if len(data) > MaxOutputBytes || !v.Truncated || len(v.Evidence) == 0 {
		t.Fatal("lexical output not trimmed", len(data), v.Truncated)
	}
	small := fit(response{Evidence: bulkyEvidence(1), Hybrid: &index.HybridResult{Evidence: bulkyEvidence(1)}})
	if small.Truncated || len(small.Evidence) != 1 || len(small.Hybrid.Evidence) != 1 {
		t.Fatal("fitting response changed", small.Truncated)
	}
	// Nothing left to trim: result() still turns it into OUTPUT_LIMIT.
	huge := fit(response{Freshness: strings.Repeat("y", MaxOutputBytes)})
	if r := result(huge); !r.IsError {
		t.Fatal("oversized response without evidence must be OUTPUT_LIMIT")
	}
}

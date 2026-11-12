package parser

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTextBoundariesAndCancellation(t *testing.T) {
	source := strings.Repeat("small line\n", 4000)
	r, err := Parse(context.Background(), "text", []byte(source))
	if err != nil || len(r.Chunks) < 2 {
		t.Fatal("bounded text chunks", err)
	}
	var joined strings.Builder
	last := 0
	for _, chunk := range r.Chunks {
		if len(chunk.Text) > MaxChunkBytes || chunk.StartLine != last+1 || chunk.EndLine < chunk.StartLine {
			t.Fatal("bad chunk evidence", chunk.StartLine, chunk.EndLine)
		}
		joined.WriteString(chunk.Text)
		last = chunk.EndLine
	}
	if joined.String() != source {
		t.Fatal("chunking lost source")
	}
	if _, err := Parse(context.Background(), "text", []byte(strings.Repeat("x", MaxChunkBytes+1))); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded line accepted", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, "text", []byte("hello")); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled parsing", err)
	}
}

func TestNativeSymbolsAndASTChunks(t *testing.T) {
	if !Available() {
		t.Skip("native CGO parser unavailable")
	}
	for _, tt := range []struct{ language, source, name, kind string }{
		{"typescript", "export function greet() { return 1; }\nexport class Greeter {}\n", "greet", "function_declaration"},
		{"tsx", "export function View() { return <div>Hello</div>; }\n", "View", "function_declaration"},
		{"csharp", "public class Greeter { public string Greet() { return \"hello\"; } }\n", "Greet", "method_declaration"},
	} {
		r, err := Parse(context.Background(), tt.language, []byte(tt.source))
		if err != nil {
			t.Fatal(tt.language, err)
		}
		found := false
		for _, s := range r.Symbols {
			if s.Name == tt.name && s.Kind == tt.kind {
				found = true
			}
		}
		if !found || len(r.Chunks) == 0 || r.Chunks[0].StartLine != 1 {
			t.Fatal("native evidence missing", tt.language, r)
		}
		for _, chunk := range r.Chunks {
			if len(chunk.Text) > MaxChunkBytes || !strings.Contains(tt.source, chunk.Text) {
				t.Fatal("invalid AST chunk")
			}
		}
	}
	if _, err := Parse(context.Background(), "typescript", []byte("function incomplete( {")); !errors.Is(err, ErrMalformed) {
		t.Fatal("malformed syntax accepted", err)
	}
	data := []byte("export function deep() { return " + strings.Repeat("(", 300) + "1" + strings.Repeat(")", 300) + "; }")
	if _, err := Parse(context.Background(), "typescript", data); !errors.Is(err, ErrLimit) {
		t.Fatal("AST depth limit missing", err)
	}
}

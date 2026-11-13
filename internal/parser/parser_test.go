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
	if _, err := parseNative(context.Background(), "typescript", []byte("function incomplete( {")); !errors.Is(err, ErrMalformed) {
		t.Fatal("malformed syntax accepted", err)
	}
	if r, err := Parse(context.Background(), "typescript", []byte("function incomplete( {")); err != nil || r.Fallback != FallbackMalformed || len(r.Symbols) != 0 || len(r.Chunks) != 1 {
		t.Fatal("malformed syntax not kept as plain text", r, err)
	}
	data := []byte("export function deep() { return " + strings.Repeat("(", 300) + "1" + strings.Repeat(")", 300) + "; }")
	if _, err := parseNative(context.Background(), "typescript", data); !errors.Is(err, ErrLimit) {
		t.Fatal("AST depth limit missing", err)
	}
	if r, err := Parse(context.Background(), "typescript", data); err != nil || r.Fallback != FallbackLimit || len(r.Symbols) != 0 {
		t.Fatal("over-limit source not kept as plain text", r, err)
	}
}

func TestNativeSkipsFunctionLocalVariables(t *testing.T) {
	if !Available() {
		t.Skip("native CGO parser unavailable")
	}
	for _, tt := range []struct {
		language, source string
		want, unwanted   []string
	}{
		{"typescript", "export const handler = () => { const localNoise = 1; return localNoise; };\n" +
			"export function run() { let counter = 0; return counter; }\n" +
			"namespace Space { export const spaced = 1; }\n" +
			"class Box { method() { const inner = 2; return inner; } }\n" +
			"export const topLevel = 3;\n",
			[]string{"handler", "run", "spaced", "Box", "topLevel"}, []string{"localNoise", "counter", "inner"}},
		{"csharp", "public class Store { private int count = 0; public int Total() { var tmp = count; return tmp; } public Store() { int ctorLocal = 1; } }\n",
			[]string{"Store", "Total"}, []string{"tmp", "ctorLocal"}},
	} {
		r, err := Parse(context.Background(), tt.language, []byte(tt.source))
		if err != nil || r.Fallback != "" {
			t.Fatal(tt.language, err, r.Fallback)
		}
		names := map[string]bool{}
		for _, s := range r.Symbols {
			names[s.Name] = true
		}
		for _, name := range tt.want {
			if !names[name] {
				t.Error(tt.language, "missing symbol", name, r.Symbols)
			}
		}
		for _, name := range tt.unwanted {
			if names[name] {
				t.Error(tt.language, "function-local variable indexed", name)
			}
		}
	}
}

func TestPlainTextFallbackWithoutNative(t *testing.T) {
	previous := nativeParse
	t.Cleanup(func() { nativeParse = previous })
	source := []byte("line one\nline two\n")
	for _, tc := range []struct {
		err      error
		fallback string
	}{{ErrMalformed, FallbackMalformed}, {ErrLimit, FallbackLimit}} {
		nativeParse = func(context.Context, string, []byte) (Result, error) { return Result{}, tc.err }
		r, err := Parse(context.Background(), "typescript", source)
		if err != nil || r.Fallback != tc.fallback || len(r.Symbols) != 0 || len(r.Chunks) != 1 || r.Chunks[0].Text != string(source) || r.Chunks[0].StartLine != 1 || r.Chunks[0].EndLine != 2 {
			t.Fatal("plain-text fallback", tc.fallback, r, err)
		}
		// Plain text has its own limits; a file that fails both is still rejected.
		if _, err := Parse(context.Background(), "typescript", []byte(strings.Repeat("x", MaxChunkBytes+1))); !errors.Is(err, ErrLimit) {
			t.Fatal("unbounded fallback accepted", err)
		}
	}
	for _, failure := range []error{ErrUnavailable, context.Canceled} {
		nativeParse = func(context.Context, string, []byte) (Result, error) { return Result{}, failure }
		if _, err := Parse(context.Background(), "csharp", source); !errors.Is(err, failure) {
			t.Fatal("non-recoverable error swallowed", failure, err)
		}
	}
	nativeParse = func(context.Context, string, []byte) (Result, error) {
		return Result{Symbols: []Symbol{{Name: "A", Kind: "class_declaration", StartLine: 1, EndLine: 1}}}, nil
	}
	if r, err := Parse(context.Background(), "csharp", source); err != nil || r.Fallback != "" || len(r.Symbols) != 1 {
		t.Fatal("successful native result altered", r, err)
	}
}

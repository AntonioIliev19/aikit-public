// Package parser extracts bounded source evidence without type-resolution claims.
package parser

import (
	"context"
	"errors"
	"strings"
)

const MaxChunkBytes = 16 << 10
const MaxChunks = 1024
const MaxSymbols = 4096

var ErrUnavailable = errors.New("INDEX_UNAVAILABLE: native Tree-sitter requires a CGO-enabled build")
var ErrMalformed = errors.New("malformed source omitted")
var ErrLimit = errors.New("parser resource limit exceeded")

type Symbol struct {
	Name, Kind         string
	StartLine, EndLine int
}
type Chunk struct {
	Text               string
	StartLine, EndLine int
}
type Result struct {
	Symbols []Symbol
	Chunks  []Chunk
}

func SymbolKind(kind string) bool {
	switch kind {
	case "function_declaration", "class_declaration", "interface_declaration", "method_declaration", "namespace_declaration", "enum_declaration", "struct_declaration", "record_declaration", "type_alias_declaration", "variable_declarator", "property_declaration", "constructor_declaration":
		return true
	}
	return false
}

// textChunks splits at line boundaries; AST adapters call it on individual nodes.
func textChunks(data []byte, start int) ([]Chunk, error) {
	var out []Chunk
	lines := strings.SplitAfter(string(data), "\n")
	var b strings.Builder
	first, line := start, start
	flush := func() {
		if b.Len() > 0 {
			out = append(out, Chunk{b.String(), first, line - 1})
			b.Reset()
			first = line
		}
	}
	for _, text := range lines {
		if len(text) == 0 {
			continue
		}
		if len(text) > MaxChunkBytes {
			return nil, ErrLimit
		}
		if b.Len()+len(text) > MaxChunkBytes {
			flush()
		}
		b.WriteString(text)
		line++
	}
	flush()
	if len(out) > MaxChunks {
		return nil, ErrLimit
	}
	return out, nil
}

func Parse(ctx context.Context, language string, data []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if language != "text" {
		return parseNative(ctx, language, data)
	}
	chunks, err := textChunks(data, 1)
	return Result{Chunks: chunks}, err
}

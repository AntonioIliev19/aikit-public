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
	// Fallback is FallbackMalformed or FallbackLimit when native parsing failed
	// and the file was chunked as plain text without symbols instead.
	Fallback string
}

const (
	FallbackMalformed = "malformed"
	FallbackLimit     = "limit"
)

// Version identifies the extraction rules (symbol kinds, chunking, fallback).
// Bump it whenever Parse output for unchanged bytes would differ, so existing
// index entries are re-extracted instead of being kept as "unchanged".
const Version = "2"

// nativeParse is a seam so the plain-text fallback is testable without CGO.
var nativeParse = parseNative

// localScope reports function-like AST node kinds (TypeScript and C#) whose
// descendants are function-local, so variable declarators below them are not
// indexed as symbols. Module, namespace and class level declarators are kept,
// including `const handler = () => {...}`, whose declarator encloses the function.
func localScope(kind string) bool {
	switch kind {
	// TypeScript / TSX
	case "function_declaration", "generator_function_declaration", "function_expression", "function",
		"generator_function", "arrow_function", "method_definition", "class_static_block":
		return true
	// C#
	case "method_declaration", "constructor_declaration", "destructor_declaration", "operator_declaration",
		"conversion_operator_declaration", "accessor_declaration", "local_function_statement",
		"lambda_expression", "anonymous_method_expression", "global_statement":
		return true
	}
	return false
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
		result, err := nativeParse(ctx, language, data)
		var fallback string
		switch {
		case err == nil:
			return result, nil
		case errors.Is(err, ErrMalformed):
			fallback = FallbackMalformed
		case errors.Is(err, ErrLimit):
			fallback = FallbackLimit
		default:
			return Result{}, err
		}
		// Keep files with syntax errors or oversized ASTs searchable as plain text.
		chunks, textErr := textChunks(data, 1)
		if textErr != nil {
			return Result{}, textErr
		}
		return Result{Chunks: chunks, Fallback: fallback}, nil
	}
	chunks, err := textChunks(data, 1)
	return Result{Chunks: chunks}, err
}

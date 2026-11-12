//go:build cgo

package parser

import (
	"context"

	sitter "github.com/tree-sitter/go-tree-sitter"
	csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func Available() bool { return true }

func parseNative(ctx context.Context, language string, data []byte) (Result, error) {
	p := sitter.NewParser()
	defer p.Close()
	var lang *sitter.Language
	if language == "csharp" {
		lang = sitter.NewLanguage(csharp.Language())
	} else if language == "tsx" {
		lang = sitter.NewLanguage(typescript.LanguageTSX())
	} else {
		lang = sitter.NewLanguage(typescript.LanguageTypescript())
	}
	if err := p.SetLanguage(lang); err != nil {
		return Result{}, ErrUnavailable
	}
	p.SetTimeoutMicros(2_000_000)
	tree := p.ParseWith(func(offset int, _ sitter.Point) []byte {
		if ctx.Err() != nil || offset >= len(data) {
			return nil
		}
		end := offset + 4096
		if end > len(data) {
			end = len(data)
		}
		return data[offset:end]
	}, nil)
	if err := ctx.Err(); err != nil {
		if tree != nil {
			tree.Close()
		}
		return Result{}, err
	}
	if tree == nil {
		return Result{}, ErrLimit
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return Result{}, ErrMalformed
	}
	var result Result
	nodes := 0
	var symbols func(*sitter.Node, int) error
	symbols = func(n *sitter.Node, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes++
		if depth > 256 || nodes > 100000 {
			return ErrLimit
		}
		kind := n.Kind()
		if SymbolKind(kind) {
			if name := n.ChildByFieldName("name"); name != nil {
				if len(result.Symbols) >= MaxSymbols || name.EndByte()-name.StartByte() > 256 {
					return ErrLimit
				}
				result.Symbols = append(result.Symbols, Symbol{name.Utf8Text(data), kind, int(n.StartPosition().Row) + 1, int(n.EndPosition().Row) + 1})
			}
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			if err := symbols(n.NamedChild(i), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := symbols(root, 0); err != nil {
		return Result{}, err
	}
	// Split oversized declarations recursively at AST children; leaf lines remain bounded.
	var chunks func(*sitter.Node) error
	chunks = func(n *sitter.Node) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if n.EndByte()-n.StartByte() <= MaxChunkBytes || n.NamedChildCount() == 0 {
			parts, err := textChunks(data[n.StartByte():n.EndByte()], int(n.StartPosition().Row)+1)
			if err != nil {
				return err
			}
			result.Chunks = append(result.Chunks, parts...)
		} else {
			for i := uint(0); i < n.NamedChildCount(); i++ {
				if err := chunks(n.NamedChild(i)); err != nil {
					return err
				}
			}
		}
		if len(result.Chunks) > MaxChunks {
			return ErrLimit
		}
		return nil
	}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		if err := chunks(root.NamedChild(i)); err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

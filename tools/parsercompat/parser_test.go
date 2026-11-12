package parsercompat

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
	csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func TestNativeGrammars(t *testing.T) {
	for _, tt := range []struct {
		name     string
		language *sitter.Language
		source   string
		kind     string
		symbol   string
	}{
		{"typescript", sitter.NewLanguage(typescript.LanguageTypescript()), "export function greet(name: string): string { return name; }", "function_declaration", "greet"},
		{"csharp", sitter.NewLanguage(csharp.Language()), "public class Greeter { public string Greet(string name) { return name; } }", "method_declaration", "Greet"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if version := tt.language.Version(); version != 14 {
				t.Fatalf("grammar ABI = %d, want pinned ABI 14", version)
			}
			parser := sitter.NewParser()
			defer parser.Close()
			if err := parser.SetLanguage(tt.language); err != nil {
				t.Fatal(err)
			}
			source := []byte(tt.source)
			tree := parser.Parse(source, nil)
			if tree == nil {
				t.Fatal("nil parse tree")
			}
			defer tree.Close()
			root := tree.RootNode()
			if root.HasError() {
				t.Fatal("valid fixture parsed with errors")
			}
			if !findSymbol(root, source, tt.kind, tt.symbol) {
				t.Fatalf("missing %s %s", tt.kind, tt.symbol)
			}
		})
	}
}

func findSymbol(node *sitter.Node, source []byte, kind, name string) bool {
	if node.Kind() == kind {
		if symbol := node.ChildByFieldName("name"); symbol != nil && symbol.Utf8Text(source) == name {
			return true
		}
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		if findSymbol(node.NamedChild(i), source, kind, name) {
			return true
		}
	}
	return false
}

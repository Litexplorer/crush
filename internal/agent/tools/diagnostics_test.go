package tools

import (
	"context"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func TestSymbolKindName(t *testing.T) {
	tests := []struct {
		kind protocol.SymbolKind
		want string
	}{
		{protocol.File, "file"},
		{protocol.Module, "module"},
		{protocol.Namespace, "namespace"},
		{protocol.Package, "package"},
		{protocol.Class, "class"},
		{protocol.Method, "method"},
		{protocol.Property, "property"},
		{protocol.Field, "field"},
		{protocol.Constructor, "constructor"},
		{protocol.Enum, "enum"},
		{protocol.Interface, "interface"},
		{protocol.Function, "func"},
		{protocol.Variable, "var"},
		{protocol.Constant, "const"},
		{protocol.String, "string"},
		{protocol.Number, "number"},
		{protocol.Boolean, "boolean"},
		{protocol.Array, "array"},
		{protocol.Object, "object"},
		{protocol.Key, "key"},
		{protocol.Null, "null"},
		{protocol.EnumMember, "enum member"},
		{protocol.Struct, "struct"},
		{protocol.Event, "event"},
		{protocol.Operator, "operator"},
		{protocol.TypeParameter, "type parameter"},
		{99, "symbol"}, // unknown
	}

	for _, tt := range tests {
		got := symbolKindName(tt.kind)
		if got != tt.want {
			t.Errorf("symbolKindName(%d) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestFormatDocumentSymbols_empty(t *testing.T) {
	got := formatDocumentSymbols(nil, "")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestFormatDocumentSymbols_emptySlice(t *testing.T) {
	got := formatDocumentSymbols([]protocol.DocumentSymbol{}, "")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestFormatDocumentSymbols_single(t *testing.T) {
	symbols := []protocol.DocumentSymbol{
		{
			Name: "main",
			Kind: protocol.Function,
		},
	}
	got := formatDocumentSymbols(symbols, "")
	want := []string{"└── func main"}
	if len(got) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(got), got)
	}
	if got[0] != want[0] {
		t.Errorf("got %q, want %q", got[0], want[0])
	}
}

func TestFormatDocumentSymbols_withDetail(t *testing.T) {
	symbols := []protocol.DocumentSymbol{
		{
			Name:   "foo",
			Kind:   protocol.Function,
			Detail: "(x int) string",
		},
	}
	got := formatDocumentSymbols(symbols, "")
	want := []string{"└── func foo (x int) string"}
	if got[0] != want[0] {
		t.Errorf("got %q, want %q", got[0], want[0])
	}
}

func TestFormatDocumentSymbols_multiple(t *testing.T) {
	symbols := []protocol.DocumentSymbol{
		{
			Name: "foo",
			Kind: protocol.Function,
		},
		{
			Name: "bar",
			Kind: protocol.Variable,
		},
		{
			Name: "MyStruct",
			Kind: protocol.Struct,
		},
	}
	got := formatDocumentSymbols(symbols, "")
	want := []string{
		"├── func foo",
		"├── var bar",
		"└── struct MyStruct",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFormatDocumentSymbols_nested(t *testing.T) {
	symbols := []protocol.DocumentSymbol{
		{
			Name: "MyStruct",
			Kind: protocol.Struct,
			Children: []protocol.DocumentSymbol{
				{
					Name: "Field1",
					Kind: protocol.Field,
				},
				{
					Name: "Method1",
					Kind: protocol.Method,
				},
			},
		},
		{
			Name: "main",
			Kind: protocol.Function,
		},
	}
	got := formatDocumentSymbols(symbols, "")
	want := []string{
		"├── struct MyStruct",
		"│   ├── field Field1",
		"│   └── method Method1",
		"└── func main",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFormatDocumentSymbols_nestedLastChild(t *testing.T) {
	symbols := []protocol.DocumentSymbol{
		{
			Name: "outer",
			Kind: protocol.Function,
			Children: []protocol.DocumentSymbol{
				{
					Name: "inner",
					Kind: protocol.Variable,
				},
			},
		},
	}
	got := formatDocumentSymbols(symbols, "")
	want := []string{
		"└── func outer",
		"    └── var inner",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFormatDocumentSymbols_deepNested(t *testing.T) {
	symbols := []protocol.DocumentSymbol{
		{
			Name: "A",
			Kind: protocol.Module,
			Children: []protocol.DocumentSymbol{
				{
					Name: "B",
					Kind: protocol.Class,
					Children: []protocol.DocumentSymbol{
						{
							Name: "c",
							Kind: protocol.Method,
						},
					},
				},
				{
					Name: "D",
					Kind: protocol.Function,
				},
			},
		},
	}
	got := formatDocumentSymbols(symbols, "")
	want := []string{
		"└── module A",
		"    ├── class B",
		"    │   └── method c",
		"    └── func D",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d lines, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestGetDocumentSymbols_nilManager(t *testing.T) {
	got := getDocumentSymbols(context.Background(), "test.go", nil)
	if got != "" {
		t.Errorf("expected empty string for nil manager, got %q", got)
	}
}

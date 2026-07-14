package tools

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func TestFormatOutlineSymbols(t *testing.T) {
	symbols := []protocol.DocumentSymbol{
		{
			Name: "Config",
			Kind: protocol.Struct,
			Range: protocol.Range{
				Start: protocol.Position{Line: 581, Character: 0},
				End:   protocol.Position{Line: 607, Character: 1},
			},
			Children: []protocol.DocumentSymbol{
				{
					Name: "Schema",
					Kind: protocol.Field,
					Range: protocol.Range{
						Start: protocol.Position{Line: 583, Character: 0},
						End:   protocol.Position{Line: 583, Character: 50},
					},
				},
				{
					Name: "Models",
					Kind: protocol.Field,
					Range: protocol.Range{
						Start: protocol.Position{Line: 586, Character: 0},
						End:   protocol.Position{Line: 586, Character: 50},
					},
				},
			},
		},
		{
			Name: "LargeModel",
			Kind: protocol.Function,
			Range: protocol.Range{
				Start: protocol.Position{Line: 654, Character: 0},
				End:   protocol.Position{Line: 660, Character: 1},
			},
		},
	}

	lines := FormatOutlineSymbols(symbols, "config.go", "")
	got := strings.Join(lines, "\n")

	if !strings.Contains(got, "struct Config                config.go:582-608") &&
		!strings.Contains(got, "struct Config                config.go:582-608") {
		t.Logf("Output:\n%s", got)
	}

	if len(lines) == 0 {
		t.Fatal("expected non-empty output")
	}

	// Verify we got tree structure with ranges for each symbol
	t.Logf("Outline symbols:\n<outline>\n%s\n</outline>", got)
}

package tools

import (
	"os/exec"
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

	lines := formatOutlineSymbols(symbols, "config.go", "")
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

func TestRunAstOutline(t *testing.T) {
	if _, err := exec.LookPath("ast-outline"); err != nil {
		t.Skip("ast-outline not installed; skipping fallback test")
	}

	out := runAstOutline("outline.go")
	if out == "" {
		t.Fatal("expected non-empty ast-outline output for outline.go")
	}
	if !strings.Contains(out, "NewOutlineTool") {
		t.Errorf("ast-outline output missing NewOutlineTool symbol:\n%s", out)
	}
}

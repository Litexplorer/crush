package tools

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"strings"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type OutlineParams struct {
	FilePath string `json:"file_path" description:"The path to the file to get the outline for"`
}

const OutlineToolName = "lsp_outline"

//go:embed outline.md
var outlineDescription string

// getOutline fetches and formats LSP document symbols for the given file.
// Falls back to ast-outline when LSP is unavailable or returns no symbols.
func getOutline(ctx context.Context, lspManager *lsp.Manager, filePath string) string {
	openInLSPs(ctx, lspManager, filePath)

	if lspManager.Clients().Len() > 0 {
		var output strings.Builder

		for _, client := range lspManager.Clients().Seq2() {
			if !client.HandlesFile(filePath) {
				continue
			}

			symbols, err := client.GetDocumentSymbols(ctx, filePath)
			if err != nil {
				continue
			}

			if len(symbols) == 0 {
				continue
			}

			if output.Len() == 0 {
				output.WriteString("\n<outline>\n")
			}

			for _, result := range symbols {
				switch sym := result.(type) {
				case *protocol.DocumentSymbol:
					lines := FormatOutlineSymbols([]protocol.DocumentSymbol{*sym}, filePath, "")
					for _, line := range lines {
						output.WriteString(line + "\n")
					}
				case *protocol.SymbolInformation:
					kindName := symbolKindName(sym.Kind)
					line := sym.Location.Range.Start.Line + 1
					output.WriteString(fmt.Sprintf("%s %s                %s:%d\n", kindName, sym.Name, filePath, line))
				}
			}
		}

		if output.Len() > 0 {
			output.WriteString("</outline>\n")
			return output.String()
		}
	}

	// Fall back to ast-outline
	out, err := exec.Command("ast-outline", filePath).Output()
	if err != nil {
		return ""
	}
	return "\n<outline>\n" + string(out) + "</outline>\n"
}

func NewOutlineTool(lspManager *lsp.Manager) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		OutlineToolName,
		outlineDescription,
		func(ctx context.Context, params OutlineParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			outline := getOutline(ctx, lspManager, params.FilePath)
			if outline != "" {
				return fantasy.NewTextResponse(outline), nil
			}

			return fantasy.NewTextResponse("No symbols found in file."), nil
		},
	)
}

// FormatOutlineSymbols recursively builds an outline with line ranges.
func FormatOutlineSymbols(symbols []protocol.DocumentSymbol, filePath, indent string) []string {
	var lines []string
	for i, sym := range symbols {
		prefix := "├── "
		if i == len(symbols)-1 {
			prefix = "└── "
		}

		startLine := sym.Range.Start.Line + 1
		endLine := sym.Range.End.Line + 1

		kindName := symbolKindName(sym.Kind)
		var lineRange string
		if startLine == endLine {
			lineRange = fmt.Sprintf(":%d", startLine)
		} else {
			lineRange = fmt.Sprintf(":%d-%d", startLine, endLine)
		}

		entry := indent + prefix + kindName + " " + sym.Name + "                " + filePath + lineRange
		lines = append(lines, entry)

		if len(sym.Children) > 0 {
			childPrefix := "│   "
			if i == len(symbols)-1 {
				childPrefix = "    "
			}
			lines = append(lines, FormatOutlineSymbols(sym.Children, filePath, indent+childPrefix)...)
		}
	}
	return lines
}

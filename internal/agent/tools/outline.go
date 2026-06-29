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

func NewOutlineTool(lspManager *lsp.Manager) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		OutlineToolName,
		outlineDescription,
		func(ctx context.Context, params OutlineParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}

			openInLSPs(ctx, lspManager, params.FilePath)

			if lspManager.Clients().Len() == 0 {
				return tryAstOutline(params.FilePath)
			}

			var output strings.Builder

			for _, client := range lspManager.Clients().Seq2() {
				if !client.HandlesFile(params.FilePath) {
					continue
				}

				symbols, err := client.GetDocumentSymbols(ctx, params.FilePath)
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
						lines := formatOutlineSymbols([]protocol.DocumentSymbol{*sym}, params.FilePath, "")
						for _, line := range lines {
							output.WriteString(line + "\n")
						}
					case *protocol.SymbolInformation:
						kindName := symbolKindName(sym.Kind)
						line := sym.Location.Range.Start.Line + 1
						output.WriteString(fmt.Sprintf("%s %s                %s:%d\n", kindName, sym.Name, params.FilePath, line))
					}
				}
			}

			if output.Len() == 0 {
				return tryAstOutline(params.FilePath)
			}

			output.WriteString("</outline>\n")
			return fantasy.NewTextResponse(output.String()), nil
		},
	)
}

// tryAstOutline runs ast-outline as a fallback when LSP returns no symbols.
func tryAstOutline(filePath string) (fantasy.ToolResponse, error) {
	out, err := exec.Command("ast-outline", filePath).Output()
	if err != nil {
		return fantasy.NewTextResponse("No symbols found in file."), nil
	}
	return fantasy.NewTextResponse("\n<outline>\n" + string(out) + "</outline>\n"), nil
}

// formatOutlineSymbols recursively builds an outline with line ranges.
func formatOutlineSymbols(symbols []protocol.DocumentSymbol, filePath, indent string) []string {
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
			lines = append(lines, formatOutlineSymbols(sym.Children, filePath, indent+childPrefix)...)
		}
	}
	return lines
}

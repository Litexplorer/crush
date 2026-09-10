package tools

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const (
	codegraphTimeout   = 2 * time.Second
	maxTopLevelSymbols = 10
	maxDepsPerSymbol   = 5
)

type codegraphImpactResult struct {
	Symbol   string                    `json:"symbol"`
	Affected []codegraphAffectedSymbol `json:"affected"`
}

type codegraphAffectedSymbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	FilePath  string `json:"filePath"`
	StartLine int    `json:"startLine"`
}

func getCodegraphImpact(ctx context.Context, lspManager *lsp.Manager, filePath string) string {
	if _, err := exec.LookPath("codegraph"); err != nil {
		return ""
	}

	symbols := getTopLevelSymbols(ctx, lspManager, filePath)
	if len(symbols) == 0 {
		return ""
	}
	if len(symbols) > maxTopLevelSymbols {
		symbols = symbols[:maxTopLevelSymbols]
	}

	fileBase := filepath.Base(filePath)
	var output strings.Builder
	hasResults := false

	for _, sym := range symbols {
		name := sym.Name
		symCtx, cancel := context.WithTimeout(ctx, codegraphTimeout)

		cmd := exec.CommandContext(symCtx, "codegraph", "impact", "--depth", "1", "--json", name)
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}

		var result codegraphImpactResult
		if err := json.Unmarshal(out, &result); err != nil || len(result.Affected) <= 1 {
			continue
		}

		var deps []string
		for _, a := range result.Affected[1:] {
			if len(deps) >= maxDepsPerSymbol {
				deps = append(deps, "...")
				break
			}
			depBase := filepath.Base(a.FilePath)
			if depBase == fileBase {
				deps = append(deps, a.Name)
			} else {
				deps = append(deps, depBase+":"+a.Name)
			}
		}

		if len(deps) > 0 {
			if !hasResults {
				output.WriteString("\n<codegraph>\n")
				hasResults = true
			}
			output.WriteString(name + " → " + strings.Join(deps, ", ") + "\n")
		}
	}

	if hasResults {
		output.WriteString("</codegraph>\n")
		return output.String()
	}
	return ""
}

func getTopLevelSymbols(ctx context.Context, lspManager *lsp.Manager, filePath string) []protocol.DocumentSymbol {
	if lspManager.Clients().Len() == 0 {
		return nil
	}

	for _, client := range lspManager.Clients().Seq2() {
		if !client.HandlesFile(filePath) {
			continue
		}

		symbols, err := client.GetDocumentSymbols(ctx, filePath)
		if err != nil || len(symbols) == 0 {
			continue
		}

		var topLevel []protocol.DocumentSymbol
		for _, result := range symbols {
			if sym, ok := result.(*protocol.DocumentSymbol); ok {
				topLevel = append(topLevel, *sym)
			}
		}
		return topLevel
	}

	return nil
}

package tools

import (
	"context"
	_ "embed"
	"os/exec"
	"strconv"
	"strings"

	"charm.land/fantasy"
)

const defaultFileFinderLimit = 200

type FileFinderParams struct {
	Pattern string `json:"pattern" description:"The file name or glob pattern to search for (e.g., '*.go', 'config.yaml')"`
	Root    string `json:"root,omitempty" description:"Optional root directory to search in (defaults to working directory)"`
	Limit   int    `json:"limit,omitempty" description:"Maximum number of results to return (default: 200)"`
}

const FileFinderToolName = "file_finder"

//go:embed file_finder.md
var fileFinderDescription string

func NewFileFinderTool(workingDir string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		FileFinderToolName,
		fileFinderDescription,
		func(ctx context.Context, params FileFinderParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Pattern == "" {
				return fantasy.NewTextErrorResponse("pattern is required"), nil
			}

			root := params.Root

			limit := params.Limit
			if limit <= 0 {
				limit = defaultFileFinderLimit
			}

			result := findFiles(params.Pattern, root, workingDir, limit)
			if result == "" {
				return fantasy.NewTextResponse("No files found matching pattern: " + params.Pattern), nil
			}

			return fantasy.NewTextResponse("\n" + result + "\n"), nil
		},
	)
}

// findFiles tries fd, then find to search for files matching the pattern.
// If root is empty, it also tries locate first for system-wide search.
func findFiles(pattern, root, workingDir string, limit int) string {
	if root == "" {
		// No root constraint — try locate first (fastest, uses system index)
		if out, ok := tryLocate(pattern); ok {
			return formatResults(truncateLines(out, limit), "locate")
		}
		root = workingDir
	}

	// Try fd next (fast, respects .gitignore)
	if out, ok := tryFd(pattern, root); ok {
		return formatResults(truncateLines(out, limit), "fd")
	}

	// Fall back to find (always available)
	if out, ok := tryFind(pattern, root); ok {
		return formatResults(truncateLines(out, limit), "find")
	}

	return ""
}

func tryLocate(pattern string) (string, bool) {
	if _, err := exec.LookPath("locate"); err != nil {
		return "", false
	}

	cmd := exec.Command("locate", pattern)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return "", false
	}

	return strings.TrimSpace(string(out)), true
}

func tryFd(pattern, root string) (string, bool) {
	if _, err := exec.LookPath("fd"); err != nil {
		return "", false
	}

	cmd := exec.Command("fd", "--type", "f", pattern, root)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return "", false
	}

	return strings.TrimSpace(string(out)), true
}

func tryFind(pattern, root string) (string, bool) {
	cmd := exec.Command("find", root, "-name", pattern, "-type", "f")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return "", false
	}

	return strings.TrimSpace(string(out)), true
}

func formatResults(output, tool string) string {
	var b strings.Builder
	b.WriteString("<results tool=\"" + tool + "\">\n")
	b.WriteString(output)
	b.WriteString("\n</results>")
	return b.String()
}

func truncateLines(output string, limit int) string {
	lines := strings.Split(output, "\n")
	if len(lines) <= limit {
		return output
	}
	return strings.Join(lines[:limit], "\n") + "\n... (truncated, showing " + strconv.Itoa(limit) + " of " + strconv.Itoa(len(lines)) + " results)"
}

package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/diff"
	"github.com/charmbracelet/crush/internal/filepathext"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/history"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/permission"
)

const ApplyPatchToolName = "apply_patch"

//go:embed apply_patch.md
var applyPatchDescription string

type ApplyPatchParams struct {
	Patch string `json:"patch" description:"The unified diff patch to apply. Must include --- a/path and +++ b/path headers for each file."`
}

// ApplyPatchFileDiff represents the diff for a single file in the patch.
type ApplyPatchFileDiff struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
	Additions  int    `json:"additions"`
	Removals   int    `json:"removals"`
	Applied    bool   `json:"applied"`
	Error      string `json:"error,omitempty"`
}

// ApplyPatchPermissionsParams is the params sent to the permission system.
type ApplyPatchPermissionsParams struct {
	Files []ApplyPatchFileDiff `json:"files"`
}

// ApplyPatchResponseMetadata is the metadata returned in the tool response.
type ApplyPatchResponseMetadata struct {
	Files []ApplyPatchFileDiff `json:"files"`
}

// fileChange represents a single file's parsed patch with applied content.
type fileChange struct {
	filePath   string
	oldContent string
	newContent string
	additions  int
	removals   int
}

// NewApplyPatchTool creates a new apply_patch tool.
func NewApplyPatchTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ApplyPatchToolName,
		applyPatchDescription,
		func(ctx context.Context, params ApplyPatchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Patch == "" {
				return fantasy.NewTextErrorResponse("patch is required"), nil
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required")
			}

			// Phase 1: Parse patch into per-file sections
			sections := splitPatchIntoSections(params.Patch)

			if len(sections) == 0 {
				return fantasy.NewTextErrorResponse("no file changes found in patch"), nil
			}

			// Phase 2: Security — filetracker check + try-apply all hunks in memory
			var changes []fileChange
			for _, sec := range sections {
				// Resolve full file path
				fullPath := filepathext.SmartJoin(workingDir, sec.filePath)

				// Check if this is a new file (--- /dev/null)
				isNewFile := sec.fromDevNull

				// For existing files, verify filetracker and read content
				var oldContent string
				if !isNewFile {
					fileInfo, err := os.Stat(fullPath)
					if err != nil {
						if os.IsNotExist(err) {
							return fantasy.NewTextErrorResponse(fmt.Sprintf("file not found: %s. The patch may reference a path that doesn't exist in the workspace.", fullPath)), nil
						}
						return fantasy.ToolResponse{}, fmt.Errorf("failed to access file %s: %w", fullPath, err)
					}
					if fileInfo.IsDir() {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", fullPath)), nil
					}

					lastRead := filetracker.LastReadTime(ctx, sessionID, fullPath)
					if lastRead.IsZero() {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("you must read the file before editing it: %s. Use the View tool first.", fullPath)), nil
					}

					modTime := fileInfo.ModTime().Truncate(time.Second)
					if modTime.After(lastRead) {
						return fantasy.NewTextErrorResponse(
							fmt.Sprintf("file %s has been modified since it was last read (mod time: %s, last read: %s)",
								fullPath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339)),
						), nil
					}

					content, err := os.ReadFile(fullPath)
					if err != nil {
						return fantasy.ToolResponse{}, fmt.Errorf("failed to read file %s: %w", fullPath, err)
					}
					oldContent, _ = fsext.ToUnixLineEndings(string(content))
				}

				// Phase 3: Try-apply the patch in memory
				newContent, err := applyPatchToContent(sec.patchText, oldContent)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to apply hunk for %s: %v. The patch may not match the current file content.", sec.filePath, err)), nil
				}

				_, additions, removals := diff.GenerateDiff(oldContent, newContent, sec.filePath)

				changes = append(changes, fileChange{
					filePath:   fullPath,
					oldContent: oldContent,
					newContent: newContent,
					additions:  additions,
					removals:   removals,
				})
			}

			// Build file diffs for permission display
			var fileDiffs []ApplyPatchFileDiff
			for _, ch := range changes {
				fileDiffs = append(fileDiffs, ApplyPatchFileDiff{
					FilePath:   ch.filePath,
					OldContent: ch.oldContent,
					NewContent: ch.newContent,
					Additions:  ch.additions,
					Removals:   ch.removals,
				})
			}

			// Phase 4: Permission request
			desc := fmt.Sprintf("Apply patch to %d file(s)", len(changes))
			displayPath := fsext.PathOrPrefix(changes[0].filePath, workingDir)
			p, err := permissions.Request(ctx, permission.CreatePermissionRequest{
				SessionID:   sessionID,
				Path:        displayPath,
				ToolCallID:  call.ID,
				ToolName:    ApplyPatchToolName,
				Action:      "write",
				Description: desc,
				Params: ApplyPatchPermissionsParams{
					Files: fileDiffs,
				},
			})
			if err != nil {
				return fantasy.ToolResponse{}, err
			}
			if !p {
				resp := NewPermissionDeniedResponse()
				resp = fantasy.WithResponseMetadata(resp, ApplyPatchResponseMetadata{
					Files: fileDiffs,
				})
				return resp, nil
			}

			// Phase 5: Atomic write — all already verified, now write
			for i, ch := range changes {
				dir := filepath.Dir(ch.filePath)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					// Partial failure — try to clean up already-written files
					for j := 0; j < i; j++ {
						_ = os.WriteFile(changes[j].filePath, []byte(changes[j].oldContent), 0o644)
					}
					return fantasy.ToolResponse{}, fmt.Errorf("failed to create parent directories for %s: %w", ch.filePath, err)
				}

				if err := os.WriteFile(ch.filePath, []byte(ch.newContent), 0o644); err != nil {
					// Rollback already-written files
					for j := 0; j < i; j++ {
						_ = os.WriteFile(changes[j].filePath, []byte(changes[j].oldContent), 0o644)
					}
					return fantasy.ToolResponse{}, fmt.Errorf("failed to write file %s: %w", ch.filePath, err)
				}

				// Update filetracker
				filetracker.RecordRead(ctx, sessionID, ch.filePath)

				// Update history
				fileHist, err := files.GetByPathAndSession(ctx, ch.filePath, sessionID)
				if err != nil {
					_, err = files.Create(ctx, sessionID, ch.filePath, ch.oldContent)
					if err != nil {
						slog.Error("Error creating file history", "error", err, "file", ch.filePath)
					}
				} else if fileHist.Content != ch.oldContent {
					_, err = files.CreateVersion(ctx, sessionID, ch.filePath, ch.oldContent)
					if err != nil {
						slog.Error("Error creating file history version", "error", err, "file", ch.filePath)
					}
				}
				_, err = files.CreateVersion(ctx, sessionID, ch.filePath, ch.newContent)
				if err != nil {
					slog.Error("Error creating file history version", "error", err, "file", ch.filePath)
				}

				fileDiffs[i].Applied = true
			}

			// Phase 6: Notify LSPs and build response
			for _, ch := range changes {
				notifyLSPs(ctx, lspManager, ch.filePath)
			}

			totalAdditions := 0
			totalRemovals := 0
			for _, fd := range fileDiffs {
				totalAdditions += fd.Additions
				totalRemovals += fd.Removals
			}

			respText := fmt.Sprintf("Applied patch to %d file(s) (%d additions, %d removals)", len(changes), totalAdditions, totalRemovals)

			// Collect diagnostics for all changed files
			var diagText string
			for _, ch := range changes {
				diagText += getDiagnostics(ch.filePath, lspManager)
			}
			if diagText != "" {
				respText += "\n\n" + diagText
			}

			resp := fantasy.NewTextResponse(respText)
			resp = fantasy.WithResponseMetadata(resp, ApplyPatchResponseMetadata{
				Files: fileDiffs,
			})
			return resp, nil
		},
	)
}

// patchSection holds the parsed content for a single file in a multi-file patch.
type patchSection struct {
	filePath    string // the target file path (from +++ line, relative to repo root)
	patchText   string // the complete unified diff for just this file
	fromDevNull bool   // true if --- was /dev/null (new file)
}

// splitPatchIntoSections splits a multi-file unified diff patch into per-file sections.
// It handles both git-format patches (with diff --git headers) and plain unified diff.
func splitPatchIntoSections(patchStr string) []patchSection {
	var sections []patchSection
	lines := strings.Split(patchStr, "\n")

	type state int
	const (
		looking state = iota // looking for the start of a file section
		inHeader             // found --- line, looking for +++ line
		inHunks              // collecting hunk lines
	)

	st := looking
	var currentLines []string
	var currentPath string
	var currentFromDevNull bool

	flush := func() {
		if currentPath == "" || len(currentLines) == 0 {
			return
		}
		sections = append(sections, patchSection{
			filePath:    currentPath,
			patchText:   strings.Join(currentLines, "\n"),
			fromDevNull: currentFromDevNull,
		})
	}

	for _, line := range lines {
		switch st {
		case looking:
			if strings.HasPrefix(line, "--- ") {
				// Start of a new file section
				currentLines = []string{line}
				currentFromDevNull = strings.Contains(line, "/dev/null")
				st = inHeader
			}
		case inHeader:
			currentLines = append(currentLines, line)
			if strings.HasPrefix(line, "+++ ") {
				// Extract the file path from +++ line
				pathStr := strings.TrimPrefix(line, "+++ ")
				pathStr = strings.TrimPrefix(pathStr, "b/")
				if pathStr == "/dev/null" {
					// Deletion — keep the path from the --- line
					pathStr = ""
				}
				currentPath = pathStr
				st = inHunks
			} else if !strings.HasPrefix(line, "--- ") && !strings.HasPrefix(line, "diff --git ") {
				// Malformed: second line wasn't +++, treat as looking for next
				flush()
				st = looking
			}
		case inHunks:
			if strings.HasPrefix(line, "--- ") {
				// Start of next file section
				flush()
				currentLines = []string{line}
				currentFromDevNull = strings.Contains(line, "/dev/null")
				st = inHeader
			} else {
				currentLines = append(currentLines, line)
			}
		}
	}
	flush()

	return sections
}

// hunkRegex matches unified diff hunk headers like @@ -1,4 +1,5 @@
var hunkRegex = regexp.MustCompile(`@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// applyPatchToContent applies a unified diff patch section (single file) to the given content.
// This handles context lines (leading space), deletions (-), insertions (+),
// and skips file headers (---/+++/diff --git) and trailing markers (\ No newline at end of file).
func applyPatchToContent(patchText, before string) (string, error) {
	beforeLines := strings.Split(before, "\n")
	patchLines := strings.Split(patchText, "\n")

	var result []string
	pos := 0 // current position in beforeLines (0-based)

	for i := 0; i < len(patchLines); i++ {
		line := patchLines[i]

		// Skip headers and metadata lines
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "diff --git ") {
			continue
		}

		// Skip "\ No newline at end of file" markers
		if strings.HasPrefix(line, `\`) {
			continue
		}

		// Skip empty lines between hunks
		if line == "" {
			continue
		}

		// Check for hunk header
		if strings.HasPrefix(line, "@@") {
			matches := hunkRegex.FindStringSubmatch(line)
			if matches == nil {
				return "", fmt.Errorf("malformed hunk header: %s", line)
			}

			fromLine, _ := strconv.Atoi(matches[1])

			// Advance to the hunk position (1-based → 0-based)
			for pos < fromLine-1 {
				if pos >= len(beforeLines) {
					return "", fmt.Errorf("hunk start %d beyond file length %d", fromLine, len(beforeLines))
				}
				result = append(result, beforeLines[pos])
				pos++
			}

			// Process hunk body lines until next @@ or end
			i++
			for i < len(patchLines) {
				hl := patchLines[i]
				i++

				// Check for end of hunk markers
				if hl == "" || strings.HasPrefix(hl, "@@") || strings.HasPrefix(hl, "--- ") || strings.HasPrefix(hl, "+++ ") || strings.HasPrefix(hl, `\`) {
					// If we hit a hunk header, back up so outer loop processes it
					if strings.HasPrefix(hl, "@@") {
						i--
					}
					break
				}

				if len(hl) == 0 {
					continue
				}

				switch hl[0] {
				case ' ':
					// Context line — should match the original
					expected := hl[1:]
					if pos >= len(beforeLines) {
						return "", fmt.Errorf("unexpected end of file at context line: %s", expected)
					}
					if beforeLines[pos] != expected {
						return "", fmt.Errorf("context mismatch at line %d: expected %q, got %q", pos+1, expected, beforeLines[pos])
					}
					result = append(result, beforeLines[pos])
					pos++
				case '-':
					// Deletion — line should exist in original
					expected := hl[1:]
					if pos >= len(beforeLines) {
						return "", fmt.Errorf("unexpected end of file at deletion: %s", expected)
					}
					if beforeLines[pos] != expected {
						return "", fmt.Errorf("deletion mismatch at line %d: expected %q, got %q", pos+1, expected, beforeLines[pos])
					}
					pos++ // skip the line
				case '+':
					// Insertion — add to result, don't advance pos
					result = append(result, hl[1:])
				default:
					return "", fmt.Errorf("unexpected line in hunk: %q", hl)
				}
			}
			if i > 0 && strings.HasPrefix(patchLines[i-1], "@@") {
				i-- // reprocess the next hunk header
			}
			continue
		}

		// Skip any other unrecognized lines
	}

	// Add remaining lines
	for pos < len(beforeLines) {
		result = append(result, beforeLines[pos])
		pos++
	}

	return strings.Join(result, "\n"), nil
}

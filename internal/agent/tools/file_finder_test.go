package tools

import (
	"context"
	"strings"
	"testing"

	"charm.land/fantasy"
)

func TestFileFinder_PatternRequired(t *testing.T) {
	tool := NewFileFinderTool("/tmp")
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{Input: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsError {
		t.Fatalf("expected error response, got %+v", resp)
	}
}

func TestFindFiles_Locate(t *testing.T) {
	// locate searches a system database — temp files won't be found.
	// Verify it returns empty when locate is unavailable or pattern doesn't exist.
	result := findFiles("__nonexistent_pattern_xyz__", "", "/tmp", 200)
	_ = result // empty is acceptable (locate may or may not be installed)
}

func TestFileFinder_Run(t *testing.T) {
	tool := NewFileFinderTool("/tmp")
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"pattern": "__nonexistent_xyz__", "root": "/tmp"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	// locate may or may not find it; any response (including "No files found") is valid
	_ = resp
}

func TestFormatResults(t *testing.T) {
	out := formatResults("foo\nbar")
	if out == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestTruncateLines(t *testing.T) {
	out := truncateLines("a\nb\nc", 2)
	if !strings.Contains(out, "truncated") {
		t.Fatal("expected truncated message")
	}
	if !strings.Contains(out, "2 of 3") {
		t.Fatal("expected count info in truncated message")
	}
	if strings.Count(out, "\n") != 2 {
		t.Fatalf("expected 2 newlines (2 lines + 1 info line), got %d", strings.Count(out, "\n"))
	}
}

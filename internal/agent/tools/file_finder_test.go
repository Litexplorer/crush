package tools

import (
	"context"
	"os"
	"path/filepath"
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

func TestFindFiles_FindFallback(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "testfile_ff.go")
	if err := os.WriteFile(f, []byte("package test"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := findFiles("testfile_ff.go", dir, dir, 200)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestFileFinder_Run(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "findme_test.go")
	if err := os.WriteFile(f, []byte("package test"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewFileFinderTool(dir)
	resp, err := tool.Run(context.Background(), fantasy.ToolCall{
		Input: `{"pattern": "findme_test.go", "root": "` + dir + `"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.IsError {
		t.Fatalf("expected success, got error: %+v", resp)
	}
}

func TestFormatResults(t *testing.T) {
	out := formatResults("foo\nbar", "find")
	if out == "" {
		t.Fatal("expected non-empty result")
	}
}

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/charmbracelet/crush/internal/filetracker"
	"github.com/charmbracelet/crush/internal/permission"
	"github.com/charmbracelet/crush/internal/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockApplyPatchFileTracker struct {
	readTimes map[string]time.Time
}

func (m *mockApplyPatchFileTracker) RecordRead(ctx context.Context, sessionID, path string) {
	if m.readTimes == nil {
		m.readTimes = map[string]time.Time{}
	}
	m.readTimes[sessionID+":"+path] = time.Now()
}

func (m *mockApplyPatchFileTracker) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	if m.readTimes == nil {
		return time.Time{}
	}
	if t, ok := m.readTimes[sessionID+":"+path]; ok {
		return t
	}
	return time.Time{}
}

func (m *mockApplyPatchFileTracker) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return nil, nil
}

var _ filetracker.Service = (*mockApplyPatchFileTracker)(nil)

func TestSplitPatchIntoSections_SingleFile(t *testing.T) {
	t.Parallel()
	patch := `--- a/src/main.go
+++ b/src/main.go
@@ -1,3 +1,4 @@
 package main
 
-func foo() {
+func bar() {
 	fmt.Println("hello")
 }`

	sections := splitPatchIntoSections(patch)
	require.Len(t, sections, 1)
	assert.Equal(t, "src/main.go", sections[0].filePath)
	assert.Contains(t, sections[0].patchText, "func foo")
	assert.Contains(t, sections[0].patchText, "func bar")
	assert.False(t, sections[0].fromDevNull)
}

func TestSplitPatchIntoSections_MultiFile(t *testing.T) {
	t.Parallel()
	patch := `--- a/src/main.go
+++ b/src/main.go
@@ -1,3 +1,4 @@
 package main
 
-func foo() {
+func bar() {
 	fmt.Println("hello")
 }
--- a/src/utils.go
+++ b/src/utils.go
@@ -1,5 +1,6 @@
 package utils
 
-func Helper() string {
+func Helper() string {
+	// added comment
 	return "ok"
 }`

	sections := splitPatchIntoSections(patch)
	require.Len(t, sections, 2)
	assert.Equal(t, "src/main.go", sections[0].filePath)
	assert.Equal(t, "src/utils.go", sections[1].filePath)
	assert.Contains(t, sections[0].patchText, "--- a/src/main.go")
	assert.Contains(t, sections[1].patchText, "--- a/src/utils.go")
}

func TestSplitPatchIntoSections_NewFile(t *testing.T) {
	t.Parallel()
	patch := `--- /dev/null
+++ b/src/newfile.go
@@ -0,0 +1,3 @@
+package main
+
+func newFunc() {}`

	sections := splitPatchIntoSections(patch)
	require.Len(t, sections, 1)
	assert.Equal(t, "src/newfile.go", sections[0].filePath)
	assert.True(t, sections[0].fromDevNull)
}

func TestSplitPatchIntoSections_WithDiffGitHeader(t *testing.T) {
	t.Parallel()
	patch := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
 
-func foo() {
+func bar() {
 	fmt.Println("hello")
 }`

	sections := splitPatchIntoSections(patch)
	require.Len(t, sections, 1)
	assert.Equal(t, "main.go", sections[0].filePath)
}

func TestSplitPatchIntoSections_EmptyPatch(t *testing.T) {
	t.Parallel()
	sections := splitPatchIntoSections("")
	assert.Len(t, sections, 0)
}

func TestSplitPatchIntoSections_NoValidSections(t *testing.T) {
	t.Parallel()
	patch := "some random text\nthat is not a valid patch"
	sections := splitPatchIntoSections(patch)
	assert.Len(t, sections, 0)
}

func TestApplyPatch_ParseAndApplyInMemory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	origContent := `package main

func foo() {
	fmt.Println("hello")
}`
	err := os.WriteFile(filePath, []byte(origContent), 0o644)
	require.NoError(t, err)

	patch := `--- a/main.go
+++ b/main.go
@@ -1,4 +1,5 @@
 package main
 
-func foo() {
+func bar() {
 	fmt.Println("hello")
 }`

	sections := splitPatchIntoSections(patch)
	require.Len(t, sections, 1)

	// Resolve the path and apply
	resolvedPath := filepath.Join(dir, sections[0].filePath)
	content, err := os.ReadFile(resolvedPath)
	require.NoError(t, err)

	newContent, err := applyPatchToContent(sections[0].patchText, string(content))
	require.NoError(t, err)
	assert.Contains(t, newContent, "func bar()")
	assert.NotContains(t, newContent, "func foo()")
}

func runApplyPatchTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params ApplyPatchParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  ApplyPatchToolName,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}

func TestApplyPatch_MissingPermission(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "main.go")
	err := os.WriteFile(filePath, []byte("package main\n"), 0o644)
	require.NoError(t, err)

	// Filetracker hasn't read the file → should fail
	ft := &mockApplyPatchFileTracker{}

	mockPerms := &mockPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
	}
	mockHist := &mockHistoryService{}

	tool := NewApplyPatchTool(nil, mockPerms, mockHist, ft, dir)

	patch := `--- a/main.go
+++ b/main.go
@@ -1 +1,3 @@
-package main
+package main
+
+func main() {}`

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runApplyPatchTool(t, tool, ctx, ApplyPatchParams{Patch: patch})
	assert.True(t, resp.IsError)
	assert.Contains(t, resp.Content, "must read the file")
}

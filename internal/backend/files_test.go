package backend

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/crush/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestWithinRoot(t *testing.T) {
	root := "/tmp/ws"
	require.True(t, withinRoot(root, "/tmp/ws/a/b"))
	require.True(t, withinRoot(root, "/tmp/ws"))
	require.False(t, withinRoot(root, "/tmp/other"))
	require.False(t, withinRoot(root, "/tmp/wsx"))
}

// resolveAttachments fills content/mime for attachments that only carry a
// file_path (the `@`-mention flow). Verify it reads server-side content.
func TestResolveAttachments(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, content string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		return p
	}
	writeFile("a.txt", "hello world")

	// Attachment with only a file_path gets content resolved from disk.
	got, err := resolveAttachments(dir, []proto.Attachment{{FilePath: "a.txt"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "hello world", string(got[0].Content))
	require.Equal(t, "a.txt", got[0].FileName)
	require.Equal(t, "text/plain; charset=utf-8", got[0].MimeType)

	// Attachment that already carries content is left untouched.
	got, err = resolveAttachments(dir, []proto.Attachment{{FilePath: "b.txt", Content: []byte("x")}})
	require.NoError(t, err)
	require.Equal(t, "x", string(got[0].Content))

	// Path escaping the workspace is rejected.
	_, err = resolveAttachments(dir, []proto.Attachment{{FilePath: "../../etc/passwd"}})
	require.Error(t, err)
}

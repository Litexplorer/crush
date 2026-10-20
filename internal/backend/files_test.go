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

	// Directory attachments become a text tree listing the folder's entries.
	writeFile("sub/inner.txt", "nested")
	got, err = resolveAttachments(dir, []proto.Attachment{{FilePath: "sub"}})
	require.NoError(t, err)
	require.Equal(t, "sub/", got[0].FileName)
	require.Equal(t, "text/plain", got[0].MimeType)
	require.Contains(t, string(got[0].Content), "inner.txt")
	require.Contains(t, string(got[0].Content), "Directory: ")

	// Path escaping the workspace is rejected.
	_, err = resolveAttachments(dir, []proto.Attachment{{FilePath: "../../etc/passwd"}})
	require.Error(t, err)
}

// Attachments are typed by content, not by extension: extension-based types
// such as application/json or "" (for .tsx/.toml) would be dropped by the
// text-only prompt merge, so every UTF-8 file must arrive as text/plain.
func TestResolveAttachments_TextMimeByContent(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, data, 0o644))
	}
	write("config.json", []byte(`{"a":1}`))
	write("deploy.yaml", []byte("a: 1\n"))
	write("app.tsx", []byte("export const a = 1\n"))
	write("Cargo.toml", []byte("[package]\n"))
	write(".env", []byte("A=1\n"))
	write("blob.bin", []byte{0x00, 0x01, 0x02})
	write("image.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...))

	tests := map[string]string{
		"config.json": "text/plain; charset=utf-8",
		"deploy.yaml": "text/plain; charset=utf-8",
		"app.tsx":     "text/plain; charset=utf-8",
		"Cargo.toml":  "text/plain; charset=utf-8",
		".env":        "text/plain; charset=utf-8",
		"blob.bin":    "application/octet-stream",
		"image.png":   "image/png",
	}
	for name, want := range tests {
		got, err := resolveAttachments(dir, []proto.Attachment{{FilePath: name}})
		require.NoError(t, err)
		require.Equal(t, want, got[0].MimeType, name)
	}
}

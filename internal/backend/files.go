package backend

import (
	"bytes"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/crush/internal/fsext"
	"github.com/charmbracelet/crush/internal/proto"
)

// Directory attachments are rendered as a compact tree listing instead of
// their binary content: the agent can orient itself and then read individual
// files with its own tools.
const (
	maxDirectoryEntries = 500
	maxDirectoryDepth   = 4
)

// Recursive workspace listings are capped so a large monorepo cannot flood the
// browser with tens of thousands of entries in one response.
const (
	maxFileListEntries = 5000
	maxFileListDepth   = 8
)

// withinRoot reports whether target is inside root (both absolute paths).
func withinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// ListWorkspaceFiles lists files and directories under the workspace working
// directory. dir is a relative path ("" = root); it must stay within the
// working directory. depth 1 lists a single level (the original behavior);
// depth > 1 walks that many levels and returns a flat list so the `@` mention
// picker can search nested files. Powers the `@` mention file picker (US-31).
func (b *Backend) ListWorkspaceFiles(workspaceID, dir string, depth int) ([]proto.FileEntry, error) {
	root, err := b.GetWorkingDir(workspaceID)
	if err != nil {
		return nil, err
	}
	target := root
	if dir != "" {
		target = filepath.Join(root, filepath.Clean(dir))
	}
	if !withinRoot(root, target) {
		return nil, fmt.Errorf("path %q is outside the workspace", dir)
	}
	if depth > maxFileListDepth {
		depth = maxFileListDepth
	}
	if depth > 1 {
		return listWorkspaceFilesRecursive(root, target, depth)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	out := make([]proto.FileEntry, 0, len(entries))
	for _, e := range entries {
		rel, _ := filepath.Rel(root, filepath.Join(target, e.Name()))
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, proto.FileEntry{
			Name:  e.Name(),
			Path:  rel,
			IsDir: e.IsDir(),
			Size:  size,
		})
	}
	return out, nil
}

// listWorkspaceFilesRecursive walks target up to depth levels and returns a
// flat, workspace-relative listing. Directory entries are reported with
// IsDir; their Size stays zero because a recursive walk does not stat files.
func listWorkspaceFilesRecursive(root, target string, depth int) ([]proto.FileEntry, error) {
	paths, _, err := fsext.ListDirectory(target, nil, depth, maxFileListEntries)
	if err != nil {
		return nil, err
	}
	out := make([]proto.FileEntry, 0, len(paths))
	for _, path := range paths {
		isDir := strings.HasSuffix(path, string(filepath.Separator))
		cleanPath := strings.TrimSuffix(path, string(filepath.Separator))
		rel, relErr := filepath.Rel(root, cleanPath)
		if relErr != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		out = append(out, proto.FileEntry{
			Name:  filepath.Base(cleanPath),
			Path:  filepath.ToSlash(rel),
			IsDir: isDir,
		})
	}
	return out, nil
}

// resolveAttachments reads file content from disk for any attachment that
// carries only a file_path (the `@` mention flow), filling content, mime type
// and file name so the agent can actually consume the reference. The browser
// cannot read the server's filesystem, so the server does the read.
func resolveAttachments(root string, atts []proto.Attachment) ([]proto.Attachment, error) {
	if len(atts) == 0 {
		return atts, nil
	}
	out := make([]proto.Attachment, len(atts))
	for i, a := range atts {
		out[i] = a
		// 用长度而非 nil 判定：JSON 里缺省的 content 会被 base64 解码成空但非 nil 的切片，
		// 用 a.Content != nil 会把「只有路径」的引用当成已带内容而跳过读取
		if len(a.Content) > 0 || a.FilePath == "" {
			continue
		}
		full := filepath.Join(root, filepath.Clean(a.FilePath))
		if !withinRoot(root, full) {
			return nil, fmt.Errorf("attachment path %q is outside the workspace", a.FilePath)
		}
		info, statErr := os.Stat(full)
		if statErr != nil {
			return nil, statErr
		}
		if info.IsDir() {
			listing, err := directoryListing(full)
			if err != nil {
				return nil, err
			}
			out[i].Content = []byte(listing)
			out[i].MimeType = "text/plain"
			if out[i].FileName == "" {
				out[i].FileName = filepath.Base(full) + "/"
			}
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		out[i].Content = data
		if out[i].FileName == "" {
			out[i].FileName = filepath.Base(a.FilePath)
		}
		// 按内容而非扩展名判类型：.json/.yaml/.tsx 等扩展名推不出 text/*，
		// 而提示词合并只接受 text/*，一旦类型失配内容会被静默丢弃
		out[i].MimeType = attachmentMimeType(a.FilePath, data)
	}
	return out, nil
}

// attachmentMimeType picks the type the agent uses to consume an attachment.
// Images keep their image type so they stay image parts; anything that decodes
// as UTF-8 text is labelled text/plain so its content is folded into the
// prompt. Extension-based types such as application/json would otherwise be
// dropped by the text-only prompt merge.
func attachmentMimeType(path string, data []byte) string {
	kind := mime.TypeByExtension(filepath.Ext(path))
	if strings.HasPrefix(kind, "image/") {
		return kind
	}
	if isTextData(data) {
		return "text/plain; charset=utf-8"
	}
	if kind == "" {
		return "application/octet-stream"
	}
	return kind
}

// isTextData reports whether data can be treated as UTF-8 text: no NUL bytes
// and valid UTF-8. Empty files count as text so the agent still sees the path.
func isTextData(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	return utf8.Valid(data)
}

// directoryListing walks a directory and renders a bounded, indented tree of
// its entries. Unreadable children are skipped so one bad file does not make
// the whole attachment fail.
func directoryListing(root string) (string, error) {
	var b strings.Builder
	b.WriteString("Directory: " + root + "\n")
	entries := 0
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := strings.Count(rel, string(os.PathSeparator)) + 1
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if depth > maxDirectoryDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entries >= maxDirectoryEntries {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			name += "/"
		}
		b.WriteString(strings.Repeat("  ", depth-1) + name + "\n")
		entries++
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	if entries >= maxDirectoryEntries {
		b.WriteString("... (listing truncated)\n")
	}
	b.WriteString("Use your file tools to read the contents of any entry above.\n")
	return b.String(), nil
}

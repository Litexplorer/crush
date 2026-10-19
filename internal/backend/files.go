package backend

import (
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/crush/internal/proto"
)

// Directory attachments are rendered as a compact tree listing instead of
// their binary content: the agent can orient itself and then read individual
// files with its own tools.
const (
	maxDirectoryEntries = 500
	maxDirectoryDepth   = 4
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
// working directory. Powers the `@` mention file picker (US-31).
func (b *Backend) ListWorkspaceFiles(workspaceID, dir string) ([]proto.FileEntry, error) {
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
		if a.Content != nil || a.FilePath == "" {
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
		if out[i].MimeType == "" {
			out[i].MimeType = mime.TypeByExtension(filepath.Ext(a.FilePath))
		}
	}
	return out, nil
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

package server

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/charmbracelet/crush/internal/proto"
)

// maxFsEntries caps how many subdirectories one listing returns; large
// directories stay responsive without needing pagination.
const maxFsEntries = 500

// handleGetFsDirs lists the subdirectories of an absolute path so the web UI
// can offer live path completion when opening a project. Read-only: it never
// creates, changes or deletes anything.
func (c *controllerV1) handleGetFsDirs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "cannot resolve home directory")
			return
		}
		path = home
	}
	if !filepath.IsAbs(path) {
		jsonError(w, http.StatusBadRequest, "path must be absolute")
		return
	}

	clean := filepath.Clean(path)
	entries, err := os.ReadDir(clean)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	out := proto.FsDirList{
		Path:    clean,
		Parent:  filepath.Dir(clean),
		Entries: make([]proto.FsDirEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		full := filepath.Join(clean, entry.Name())
		// Follow symlinks so linked project directories are selectable.
		info, statErr := os.Stat(full)
		if statErr != nil || !info.IsDir() {
			continue
		}
		out.Entries = append(out.Entries, proto.FsDirEntry{
			Name:  entry.Name(),
			Path:  full,
			IsDir: true,
			Size:  info.Size(),
		})
		if len(out.Entries) >= maxFsEntries {
			break
		}
	}

	sort.Slice(out.Entries, func(i, j int) bool {
		return out.Entries[i].Name < out.Entries[j].Name
	})
	jsonEncode(w, out)
}

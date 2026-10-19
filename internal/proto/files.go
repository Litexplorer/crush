package proto

// FileEntry describes a single file or directory entry in a workspace's
// working directory. It powers the `@` mention file picker (US-31).
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // relative to the workspace working dir
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// FsDirEntry is a single directory entry returned by the filesystem browser
// used by the web UI's "Open project" path autocomplete.
type FsDirEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // absolute path
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// FsDirList is the response for GET /v1/fs/dirs: the resolved directory,
// its parent and the subdirectories it contains.
type FsDirList struct {
	Path    string       `json:"path"`
	Parent  string       `json:"parent"`
	Entries []FsDirEntry `json:"entries"`
}

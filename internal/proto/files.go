package proto

// FileEntry describes a single file or directory entry in a workspace's
// working directory. It powers the `@` mention file picker (US-31).
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // relative to the workspace working dir
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

package proto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A path-only attachment (the `@` mention wire format) must decode with nil
// content so the backend knows it has to read the file itself. An empty base64
// payload previously produced a non-nil empty slice and made the resolver skip
// the read, leaving the agent with a path and no content.
func TestAttachment_UnmarshalJSON(t *testing.T) {
	var pathOnly Attachment
	require.NoError(t, json.Unmarshal([]byte(`{"file_path":"a/b.go","file_name":"b.go"}`), &pathOnly))
	require.Equal(t, "a/b.go", pathOnly.FilePath)
	require.Equal(t, "b.go", pathOnly.FileName)
	require.Nil(t, pathOnly.Content)

	var withContent Attachment
	require.NoError(t, json.Unmarshal([]byte(`{"file_name":"p.png","mime_type":"image/png","content":"aGk="}`), &withContent))
	require.Equal(t, []byte("hi"), withContent.Content)

	// Round-trip keeps the base64 payload intact.
	encoded, err := json.Marshal(withContent)
	require.NoError(t, err)
	var again Attachment
	require.NoError(t, json.Unmarshal(encoded, &again))
	require.Equal(t, withContent.Content, again.Content)
}

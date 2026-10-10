package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateTodos(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		items   []TodoItem
		wantErr string
	}{
		{
			name: "valid flat list",
			items: []TodoItem{
				{Content: "a", Status: "pending"},
				{Content: "b", Status: "in_progress"},
			},
		},
		{
			name: "valid nested tree",
			items: []TodoItem{
				{Content: "root", Status: "pending"},
				{Content: "child", Status: "pending", Parent: "root"},
				{Content: "grandchild", Status: "pending", Parent: "child"},
			},
		},
		{
			name:    "invalid status",
			items:   []TodoItem{{Content: "a", Status: "doing"}},
			wantErr: "invalid status",
		},
		{
			name:    "empty content",
			items:   []TodoItem{{Content: "", Status: "pending"}},
			wantErr: "must not be empty",
		},
		{
			name: "duplicate content",
			items: []TodoItem{
				{Content: "a", Status: "pending"},
				{Content: "a", Status: "completed"},
			},
			wantErr: "duplicate todo content",
		},
		{
			name:    "missing parent",
			items:   []TodoItem{{Content: "a", Status: "pending", Parent: "ghost"}},
			wantErr: "references missing parent",
		},
		{
			name:    "self parent",
			items:   []TodoItem{{Content: "a", Status: "pending", Parent: "a"}},
			wantErr: "cannot be its own parent",
		},
		{
			name: "cycle",
			items: []TodoItem{
				{Content: "a", Status: "pending", Parent: "b"},
				{Content: "b", Status: "pending", Parent: "a"},
			},
			wantErr: "cyclic parent chain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateTodos(tt.items)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

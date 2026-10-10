package session

import (
	"testing"

	"github.com/charmbracelet/crush/internal/db"
	"github.com/stretchr/testify/require"
)

func TestEstimatedUsageStateSurvivesFetchModifySave(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.EstimatedUsage = true

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.True(t, saved.EstimatedUsage)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, fetched.EstimatedUsage)

	fetched.Todos = []Todo{{
		Content:    "Check estimate state",
		Status:     TodoStatusInProgress,
		ActiveForm: "Checking estimate state",
	}}

	updated, err := sessions.Save(t.Context(), fetched)
	require.NoError(t, err)
	require.True(t, updated.EstimatedUsage)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, refetched.EstimatedUsage)
}

func TestSessionChannelPersists(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "channel")
	require.NoError(t, err)
	updated, err := sessions.SetChannel(t.Context(), created.ID, "signal")
	require.NoError(t, err)
	require.Equal(t, "signal", updated.Channel)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, "signal", fetched.Channel)
}

func TestMCPServerDisabledRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	disabled, err := sessions.MCPDisabledServers(t.Context())
	require.NoError(t, err)
	require.Empty(t, disabled, "a new repository must default to the config")

	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "docker", true))
	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "serena", true))
	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "docker", true), "disabling twice must be idempotent")

	disabled, err = sessions.MCPDisabledServers(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"docker", "serena"}, disabled)

	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "docker", false))
	disabled, err = sessions.MCPDisabledServers(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"serena"}, disabled)

	// Enabling records an enabled override so a config-disabled server
	// stays enabled across restarts; disabling removes it again.
	enabled, err := sessions.MCPServersEnabled(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{"docker"}, enabled)

	require.NoError(t, sessions.SetMCPServerDisabled(t.Context(), "docker", true))
	enabled, err = sessions.MCPServersEnabled(t.Context())
	require.NoError(t, err)
	require.Empty(t, enabled)
}

func TestEstimatedUsageStateCanBeClearedByExplicitSave(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.EstimatedUsage = true

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.True(t, saved.EstimatedUsage)

	saved.EstimatedUsage = false
	updated, err := sessions.Save(t.Context(), saved)
	require.NoError(t, err)
	require.False(t, updated.EstimatedUsage)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.False(t, refetched.EstimatedUsage)
}

// TestTotalTokensPersistAcrossSaveAndGet covers the write -> read loop for the
// session lifetime token counter, including a fetch-modify-save cycle that
// only touches unrelated fields.
func TestTotalTokensPersistAcrossSaveAndGet(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	require.Zero(t, created.TotalTokens)

	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.TotalTokens = 150

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.Equal(t, int64(150), saved.TotalTokens)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, int64(150), fetched.TotalTokens)

	fetched.Todos = []Todo{{
		Content:    "Check the lifetime total",
		Status:     TodoStatusInProgress,
		ActiveForm: "Checking the lifetime total",
	}}
	fetched.TotalTokens += 250

	updated, err := sessions.Save(t.Context(), fetched)
	require.NoError(t, err)
	require.Equal(t, int64(400), updated.TotalTokens)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, int64(400), refetched.TotalTokens)
}

// TestTodoParentPersistsAcrossSaveAndGet covers the write → read closure for
// the tree extension: the parent reference must survive JSON round-tripping
// through the sessions.todos column.
func TestTodoParentPersistsAcrossSaveAndGet(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "tree")
	require.NoError(t, err)
	created.Todos = []Todo{
		{Content: "root", Status: TodoStatusInProgress, ActiveForm: "Doing root"},
		{Content: "child", Status: TodoStatusPending, Parent: "root"},
		{Content: "grandchild", Status: TodoStatusPending, Parent: "child"},
	}

	_, err = sessions.Save(t.Context(), created)
	require.NoError(t, err)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Len(t, fetched.Todos, 3)
	require.Empty(t, fetched.Todos[0].Parent)
	require.Equal(t, "root", fetched.Todos[1].Parent)
	require.Equal(t, "child", fetched.Todos[2].Parent)
}

// TestMarshalUnmarshalTodosLegacyData guards backward compatibility: rows
// written before the parent field existed must decode with empty parents.
func TestMarshalUnmarshalTodosLegacyData(t *testing.T) {
	t.Parallel()

	legacy := `[{"content":"old","status":"pending","active_form":"Doing old"}]`
	todos, err := unmarshalTodos(legacy)
	require.NoError(t, err)
	require.Len(t, todos, 1)
	require.Empty(t, todos[0].Parent)

	data, err := marshalTodos([]Todo{{Content: "a", Status: TodoStatusPending, Parent: "b"}})
	require.NoError(t, err)
	require.Contains(t, data, `"parent":"b"`)
}

func TestTodoDepths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		todos []Todo
		want  map[string]int
	}{
		{
			name:  "flat list stays at depth zero",
			todos: []Todo{{Content: "a"}, {Content: "b"}},
			want:  map[string]int{"a": 0, "b": 0},
		},
		{
			name: "nested chain",
			todos: []Todo{
				{Content: "root"},
				{Content: "child", Parent: "root"},
				{Content: "grandchild", Parent: "child"},
			},
			want: map[string]int{"root": 0, "child": 1, "grandchild": 2},
		},
		{
			name:  "missing parent degrades to root",
			todos: []Todo{{Content: "a", Parent: "missing"}},
			want:  map[string]int{"a": 0},
		},
		{
			name: "cycle stays bounded",
			todos: []Todo{
				{Content: "a", Parent: "b"},
				{Content: "b", Parent: "a"},
			},
			want: map[string]int{"a": 1, "b": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, TodoDepths(tt.todos))
		})
	}
}

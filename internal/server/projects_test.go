package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/charmbracelet/crush/internal/projects"
	"github.com/charmbracelet/crush/internal/proto"
	"github.com/stretchr/testify/require"
)

// TestProjectsEndpoints covers listing and forgetting projects. Neither
// handler touches the backend, so a bare controller is enough.
func TestProjectsEndpoints(t *testing.T) {
	t.Setenv("CRUSH_GLOBAL_DATA", t.TempDir())

	const projectPath = "/tmp/crush-projects-endpoint"
	require.NoError(t, projects.Register(projectPath, projectPath+"/.crush"))

	c := &controllerV1{}

	t.Run("list", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c.handleGetProjects(rec, httptest.NewRequest(http.MethodGet, "/v1/projects", nil))

		require.Equal(t, http.StatusOK, rec.Code)

		var got []proto.Project
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Len(t, got, 1)
		require.Equal(t, projectPath, got[0].Path)
		require.Equal(t, projectPath+"/.crush", got[0].DataDir)
	})

	t.Run("delete requires path", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c.handleDeleteProjects(rec, httptest.NewRequest(http.MethodDelete, "/v1/projects", nil))

		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("delete", func(t *testing.T) {
		rec := httptest.NewRecorder()
		target := "/v1/projects?path=" + url.QueryEscape(projectPath)
		c.handleDeleteProjects(rec, httptest.NewRequest(http.MethodDelete, target, nil))

		require.Equal(t, http.StatusNoContent, rec.Code)

		list, err := projects.List()
		require.NoError(t, err)
		require.Empty(t, list)
	})
}

package dialog

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// searchWorkspace is a workspace stub returning canned search results.
type searchWorkspace struct {
	workspace.Workspace
	results []message.SearchResult
}

func (w *searchWorkspace) Config() *config.Config { return nil }

func (w *searchWorkspace) SearchMessages(context.Context, string, int) ([]message.SearchResult, error) {
	return w.results, nil
}

func newTestSearch(t *testing.T) (*Search, *searchWorkspace) {
	t.Helper()
	ws := &searchWorkspace{results: []message.SearchResult{{
		Message: message.Message{
			ID:        "msg-1",
			SessionID: "sess-1",
			Role:      message.User,
			Parts:     []message.ContentPart{message.TextContent{Text: "the quick brown fox"}},
			CreatedAt: time.Now().Unix(),
		},
		SessionTitle: "session one",
	}}}
	s := NewSearch(common.DefaultCommon(ws))
	return s, ws
}

func typeIntoSearch(t *testing.T, s *Search, text string) {
	t.Helper()
	for _, r := range text {
		s.HandleMsg(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestSearch_EscapeReturnsActionClose(t *testing.T) {
	s, _ := newTestSearch(t)

	action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.IsType(t, ActionClose{}, action)
}

// TestSearch_EnterRunsSearchAndSelectsResult walks the dialog through
// typing a query, running the search, receiving results, and selecting
// the first result.
func TestSearch_EnterRunsSearchAndSelectsResult(t *testing.T) {
	s, _ := newTestSearch(t)

	typeIntoSearch(t, s, "quick")

	action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	cmdAction, ok := action.(ActionCmd)
	require.True(t, ok, "Enter should run the search command")
	require.NotNil(t, cmdAction.Cmd)

	msg := cmdAction.Cmd()
	resultMsg, ok := msg.(searchResultMsg)
	require.True(t, ok, "search command should return searchResultMsg")
	require.Len(t, resultMsg.results, 1)

	// Deliver results back to the dialog.
	require.Nil(t, s.HandleMsg(resultMsg))
	require.Equal(t, 1, s.list.Len())

	// Enter again with the same query selects the result.
	action = s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	selectAction, ok := action.(ActionSelectSearchResult)
	require.True(t, ok, "Enter on a result should select it")
	require.Equal(t, "sess-1", selectAction.SessionID)
	require.Equal(t, "msg-1", selectAction.MessageID)
}

func TestSearch_EmptyQueryEnterReturnsNoCmd(t *testing.T) {
	s, _ := newTestSearch(t)

	action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	cmdAction, ok := action.(ActionCmd)
	require.True(t, ok)
	require.Nil(t, cmdAction.Cmd, "empty query should not run a search")
}

func TestSearch_NoResultsShowsPlaceholderState(t *testing.T) {
	s, ws := newTestSearch(t)
	ws.results = nil

	typeIntoSearch(t, s, "zzz")
	action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	cmdAction := action.(ActionCmd)
	msg := cmdAction.Cmd().(searchResultMsg)
	require.Empty(t, msg.results)

	require.Nil(t, s.HandleMsg(msg))
	require.Zero(t, s.list.Len())
	require.Equal(t, "zzz", s.query)
}

func TestSearch_NewQueryRerunsInsteadOfSelecting(t *testing.T) {
	s, _ := newTestSearch(t)

	// First search for "quick".
	typeIntoSearch(t, s, "quick")
	action := s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	cmdAction := action.(ActionCmd)
	require.NotNil(t, cmdAction.Cmd)
	s.HandleMsg(cmdAction.Cmd().(searchResultMsg))
	require.Equal(t, 1, s.list.Len())

	// Change the query; Enter must re-search, not select.
	typeIntoSearch(t, s, " fox")
	action = s.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	cmdAction, ok := action.(ActionCmd)
	require.True(t, ok, "changed query should re-run the search")
	require.NotNil(t, cmdAction.Cmd)
}

// TestSearch_SelectedItemGetsFocused verifies the list's focused render
// callback marks the selected item focused so it renders highlighted.
func TestSearch_SelectedItemGetsFocused(t *testing.T) {
	s, ws := newTestSearch(t)
	ws.results = append(ws.results,
		message.SearchResult{Message: message.Message{ID: "msg-2", SessionID: "sess-1"}, SessionTitle: "session one"},
	)

	s.setResults(ws.results)
	require.Equal(t, 2, s.list.Len())

	// Match the real dialog draw path: sizeDialogList calls SetSize
	// before rendering, and only then do render callbacks fire.
	s.list.SetSize(60, 10)

	// Rendering with the list focused marks only the selected item.
	_ = s.list.Render()
	selected := s.list.SelectedItem().(*SearchResultItem)
	require.True(t, selected.focused, "selected item must be marked focused")

	// Move selection down; the newly selected item becomes focused.
	s.list.SelectNext()
	_ = s.list.Render()
	selected = s.list.SelectedItem().(*SearchResultItem)
	require.True(t, selected.focused, "newly selected item must be marked focused")
}

package model

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/session"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/dialog"
	"github.com/charmbracelet/crush/internal/workspace"
	"github.com/stretchr/testify/require"
)

// questionJumpWorkspace is a minimal stub whose only real method is
// ListUserMessages; everything else panics if touched. The QuestionIndex
// dialog and its select handler only read user messages, so this is enough.
type questionJumpWorkspace struct {
	workspace.Workspace
	msgs []message.Message
}

func (w *questionJumpWorkspace) Config() *config.Config { return nil }

func (w *questionJumpWorkspace) ListUserMessages(context.Context, string) ([]message.Message, error) {
	return w.msgs, nil
}

// TestSelectQuestionIndexSetsPendingScroll walks the dialog select path
// end-to-end: opening a question index dialog over the current session,
// pressing Enter, and asserting the UI stashes the message id for the chat
// to scroll to (the same jump path used by message search).
func TestSelectQuestionIndexSetsPendingScroll(t *testing.T) {
	ws := &questionJumpWorkspace{msgs: []message.Message{{
		ID:        "msg-9",
		SessionID: "s1",
		Role:      message.User,
		Parts:     []message.ContentPart{message.TextContent{Text: "hello"}},
		CreatedAt: time.Now().Unix(),
	}}}
	com := common.DefaultCommon(ws)
	m := &UI{
		com:     com,
		chat:    NewChat(com, config.ScrollbarDefault),
		session: &session.Session{ID: "s1"},
		dialog:  dialog.NewOverlay(),
		state:   uiChat,
		keyMap:  DefaultKeyMap(),
	}

	dlg, err := dialog.NewQuestionIndex(com, "s1")
	require.NoError(t, err)
	m.dialog.OpenDialog(dlg)

	cmd := m.handleDialogMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "selecting a question should schedule the session jump")
	require.Equal(t, "msg-9", m.pendingScrollToMessageID)
}

// TestQuestionIndexKeyOpensDialog verifies ctrl+q opens the conversation
// question index dialog, so it is reachable without the commands palette.
func TestQuestionIndexKeyOpensDialog(t *testing.T) {
	ws := &questionJumpWorkspace{}
	com := common.DefaultCommon(ws)
	m := &UI{
		com:     com,
		chat:    NewChat(com, config.ScrollbarDefault),
		session: &session.Session{ID: "s1"},
		dialog:  dialog.NewOverlay(),
		state:   uiChat,
		focus:   uiFocusMain,
		keyMap:  DefaultKeyMap(),
	}

	m.handleKeyPressMsg(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})

	require.True(t, m.dialog.ContainsDialog(dialog.QuestionIndexID),
		"ctrl+q must open the question index dialog")
}

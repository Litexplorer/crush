package dialog

import (
	"context"
	"image"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

// questionIndexWorkspace is a workspace stub returning canned user messages.
type questionIndexWorkspace struct {
	workspace.Workspace
	messages []message.Message
}

func (w *questionIndexWorkspace) Config() *config.Config { return nil }

func (w *questionIndexWorkspace) ListUserMessages(context.Context, string) ([]message.Message, error) {
	return w.messages, nil
}

func newTestQuestionIndex(t *testing.T, msgs []message.Message) *QuestionIndex {
	t.Helper()
	ws := &questionIndexWorkspace{messages: msgs}
	dlg, err := NewQuestionIndex(common.DefaultCommon(ws), "sess-1")
	require.NoError(t, err)
	return dlg
}

func testUserMsg(id, text string, created time.Time) message.Message {
	return message.Message{
		ID:        id,
		SessionID: "sess-1",
		Role:      message.User,
		Parts:     []message.ContentPart{message.TextContent{Text: text}},
		CreatedAt: created.Unix(),
	}
}

func TestQuestionIndex_EscapeReturnsActionClose(t *testing.T) {
	dlg := newTestQuestionIndex(t, []message.Message{testUserMsg("msg-1", "hello", time.Now())})

	action := dlg.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.IsType(t, ActionClose{}, action)
}

// TestQuestionIndex_SelectReturnsMessageID walks the dialog through a
// list of user messages and verifies Enter selects the first one.
func TestQuestionIndex_SelectReturnsMessageID(t *testing.T) {
	dlg := newTestQuestionIndex(t, []message.Message{
		testUserMsg("msg-1", "first question", time.Now()),
		testUserMsg("msg-2", "second question", time.Now()),
	})

	require.Equal(t, 2, dlg.list.Len())

	action := dlg.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	selectAction, ok := action.(ActionSelectQuestionIndex)
	require.True(t, ok, "Enter on a question should select it")
	require.Equal(t, "msg-1", selectAction.MessageID)
}

func TestQuestionIndex_EmptySessionShowsZeroItems(t *testing.T) {
	dlg := newTestQuestionIndex(t, nil)

	require.Zero(t, dlg.list.Len(), "empty session should produce no question items")
}

func TestQuestionIndex_ItemsBuiltFromUserMessages(t *testing.T) {
	msgs := []message.Message{
		testUserMsg("msg-1", "the quick brown fox", time.Unix(1000, 0)),
		testUserMsg("msg-2", "jumped over the lazy dog", time.Unix(2000, 0)),
	}
	dlg := newTestQuestionIndex(t, msgs)

	items := dlg.list.FilteredItems()
	require.Len(t, items, 2)

	first, ok := items[0].(*QuestionIndexItem)
	require.True(t, ok)
	require.Equal(t, "msg-1", first.ID())
	require.Equal(t, 1, first.ordinal)
	require.Equal(t, "the quick brown fox", first.Filter())

	second := items[1].(*QuestionIndexItem)
	require.Equal(t, "msg-2", second.ID())
	require.Equal(t, 2, second.ordinal)
}

// TestQuestionIndex_FirstFrameShowsItems draws the dialog once on a freshly
// built instance and asserts the session's questions are already visible.
// Regression guard for the "empty list until you type a character" bug.
func TestQuestionIndex_FirstFrameShowsItems(t *testing.T) {
	dlg := newTestQuestionIndex(t, []message.Message{
		testUserMsg("msg-1", "the quick brown fox", time.Unix(1000, 0)),
		testUserMsg("msg-2", "jumped over the lazy dog", time.Unix(2000, 0)),
	})
	require.Equal(t, 2, dlg.list.Len(), "items must be loaded before the first draw")

	scr := uv.NewScreenBuffer(80, 30)
	dlg.Draw(scr, image.Rect(0, 0, 80, 30))

	out := scr.String()
	require.Contains(t, out, "the quick brown fox", "first frame must already list the questions")
	require.Contains(t, out, "jumped over the lazy dog")
}

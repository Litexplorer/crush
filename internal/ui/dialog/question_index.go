package dialog

import (
	"context"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	uv "github.com/charmbracelet/ultraviolet"
)

// QuestionIndexID is the identifier for the conversation question index
// dialog.
const QuestionIndexID = "question_index"

// QuestionIndex is a dialog listing the user messages of the current
// session so a long conversation can be navigated by question. Selecting
// a row jumps the chat to that message.
type QuestionIndex struct {
	com       *common.Common
	help      help.Model
	list      *list.FilterableList
	input     textinput.Model
	sessionID string

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*QuestionIndex)(nil)

// NewQuestionIndex creates a question index dialog for the given session,
// loading its user messages.
func NewQuestionIndex(com *common.Common, sessionID string) (*QuestionIndex, error) {
	q := new(QuestionIndex)
	q.com = com
	q.sessionID = sessionID

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()
	q.help = help

	q.list = list.NewFilterableList()
	q.list.Focus()

	q.input = textinput.New()
	q.input.SetVirtualCursor(false)
	q.input.Placeholder = "Type to filter"
	q.input.SetStyles(com.Styles.TextInput)
	q.input.Focus()

	q.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "jump"),
	)
	q.keyMap.Next = key.NewBinding(
		key.WithKeys("down"),
		key.WithHelp("↓", "next question"),
	)
	q.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous question"),
	)
	q.keyMap.Close = CloseKey

	if err := q.load(); err != nil {
		return nil, err
	}
	q.list.SetSelected(0)
	return q, nil
}

// load populates the list from the session's user messages.
func (q *QuestionIndex) load() error {
	msgs, err := q.com.Workspace.ListUserMessages(context.TODO(), q.sessionID)
	if err != nil {
		return err
	}
	items := make([]list.FilterableItem, 0, len(msgs))
	for i, msg := range msgs {
		items = append(items, NewQuestionIndexItem(q.com.Styles, msg, i+1))
	}
	q.list.SetItems(items...)
	return nil
}

// ID implements Dialog.
func (q *QuestionIndex) ID() string {
	return QuestionIndexID
}

// HandleMsg implements Dialog.
func (q *QuestionIndex) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, q.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, q.keyMap.Select):
			if item := q.list.SelectedItem(); item != nil {
				qi, ok := item.(*QuestionIndexItem)
				if !ok {
					return nil
				}
				return ActionSelectQuestionIndex{MessageID: qi.MessageID()}
			}
		case key.Matches(msg, q.keyMap.Previous):
			q.list.Focus()
			if q.list.IsSelectedFirst() {
				q.list.SelectLast()
			} else {
				q.list.SelectPrev()
			}
			q.list.ScrollToSelected()
		case key.Matches(msg, q.keyMap.Next):
			q.list.Focus()
			if q.list.IsSelectedLast() {
				q.list.SelectFirst()
			} else {
				q.list.SelectNext()
			}
			q.list.ScrollToSelected()
		default:
			var cmd tea.Cmd
			q.input, cmd = q.input.Update(msg)
			q.list.SetFilter(q.input.Value())
			return ActionCmd{Cmd: cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (q *QuestionIndex) Cursor() *tea.Cursor {
	return InputCursor(q.com.Styles, q.input.Cursor())
}

// Draw implements Dialog.
func (q *QuestionIndex) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := q.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	q.input.SetWidth(dialogInputTextWidth(t, q.input, innerWidth))
	listHeight, listTotalHeight, _ := sizeDialogList(t, q.list, innerWidth, height)

	rc := NewRenderContext(t, width)
	rc.Title = "会话问题列表"

	inputView := t.Dialog.InputPrompt.Render(q.input.View())
	cur := q.Cursor()
	rc.AddPart(inputView)

	if q.list.Len() > 0 {
		listView := t.Dialog.List.Height(q.list.Height()).Render(q.list.Render())
		listView = joinScrollbar(t, listView, listHeight, listTotalHeight, listHeight, q.list.Offset())
		rc.AddPart(listView)
	} else {
		rc.AddPart(t.Dialog.NormalItem.Render("本会话暂无问题"))
	}

	rc.Help = renderDialogHelp(t, &q.help, q, innerWidth)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements help.KeyMap.
func (q *QuestionIndex) ShortHelp() []key.Binding {
	return []key.Binding{
		q.keyMap.Select,
		q.keyMap.Previous,
		q.keyMap.Next,
		q.keyMap.Close,
	}
}

// FullHelp implements help.KeyMap.
func (q *QuestionIndex) FullHelp() [][]key.Binding {
	return [][]key.Binding{q.ShortHelp()}
}

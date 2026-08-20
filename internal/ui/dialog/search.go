package dialog

import (
	"context"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
)

// SearchID is the identifier for the message search dialog.
const SearchID = "search"

// searchResultLimit caps how many messages a single search returns.
const searchResultLimit = 50

// searchResultMsg carries search results back to the search dialog.
type searchResultMsg struct {
	results []message.SearchResult
	err     error
}

// Search is a dialog that searches messages across sessions.
type Search struct {
	com   *common.Common
	help  help.Model
	list  *list.List
	input textinput.Model

	// query is the last query that was actually searched, used to
	// decide whether Enter re-searches or selects a result.
	query string

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}
}

var _ Dialog = (*Search)(nil)

// NewSearch creates a new search dialog.
func NewSearch(com *common.Common) *Search {
	s := new(Search)
	s.com = com

	help := help.New()
	help.Styles = com.Styles.DialogHelpStyles()
	s.help = help

	s.list = list.NewList()
	s.list.RegisterRenderCallback(list.FocusedRenderCallback(s.list))
	s.list.Focus()

	s.input = textinput.New()
	s.input.SetVirtualCursor(false)
	s.input.Placeholder = "Search messages…"
	s.input.SetStyles(com.Styles.TextInput)
	s.input.Focus()

	s.keyMap.Select = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "search / open"),
	)
	s.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next result"),
	)
	s.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous result"),
	)
	s.keyMap.Close = CloseKey

	return s
}

// ID implements Dialog.
func (s *Search) ID() string {
	return SearchID
}

// HandleMsg implements Dialog.
func (s *Search) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, s.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, s.keyMap.Select):
			if item := s.list.SelectedItem(); item != nil && s.query == s.input.Value() {
				result := item.(*SearchResultItem)
				return ActionSelectSearchResult{
					SessionID: result.SessionID(),
					MessageID: result.MessageID(),
				}
			}
			return ActionCmd{s.runSearchCmd()}
		case key.Matches(msg, s.keyMap.Previous):
			s.list.Focus()
			if s.list.IsSelectedFirst() {
				s.list.SelectLast()
			} else {
				s.list.SelectPrev()
			}
			s.list.ScrollToSelected()
		case key.Matches(msg, s.keyMap.Next):
			s.list.Focus()
			if s.list.IsSelectedLast() {
				s.list.SelectFirst()
			} else {
				s.list.SelectNext()
			}
			s.list.ScrollToSelected()
		default:
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			return ActionCmd{cmd}
		}
	case searchResultMsg:
		if msg.err != nil {
			return ActionCmd{util.ReportError(msg.err)}
		}
		s.setResults(msg.results)
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (s *Search) Cursor() *tea.Cursor {
	return InputCursor(s.com.Styles, s.input.Cursor())
}

// Draw implements [Dialog].
func (s *Search) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := s.com.Styles
	width := max(0, min(defaultDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	height := max(0, min(defaultDialogHeight, area.Dy()-t.Dialog.View.GetVerticalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	s.input.SetWidth(dialogInputTextWidth(t, s.input, innerWidth))
	listHeight, listTotalHeight, _ := sizeDialogList(t, s.list, innerWidth, height)

	rc := NewRenderContext(t, width)
	rc.Title = "Search messages"

	inputView := t.Dialog.InputPrompt.Render(s.input.View())
	cur := s.Cursor()
	rc.AddPart(inputView)

	if s.list.Len() > 0 {
		listView := t.Dialog.List.Height(s.list.Height()).Render(s.list.Render())
		listView = joinScrollbar(t, listView, listHeight, listTotalHeight, listHeight, s.list.Offset())
		rc.AddPart(listView)
	} else if s.query != "" {
		rc.AddPart(t.Dialog.NormalItem.Render("No matching messages"))
	}

	rc.Help = renderDialogHelp(t, &s.help, s, innerWidth)

	view := rc.Render()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements help.KeyMap.
func (s *Search) ShortHelp() []key.Binding {
	return []key.Binding{
		s.keyMap.Select,
		s.keyMap.Previous,
		s.keyMap.Next,
		s.keyMap.Close,
	}
}

// FullHelp implements help.KeyMap.
func (s *Search) FullHelp() [][]key.Binding {
	return [][]key.Binding{s.ShortHelp()}
}

// runSearchCmd runs the search against the workspace and delivers the
// results back to the dialog as a [searchResultMsg].
func (s *Search) runSearchCmd() tea.Cmd {
	query := strings.TrimSpace(s.input.Value())
	if query == "" {
		return nil
	}
	s.query = query
	return func() tea.Msg {
		results, err := s.com.Workspace.SearchMessages(context.TODO(), query, searchResultLimit)
		return searchResultMsg{results: results, err: err}
	}
}

// setResults replaces the result list with the given matches.
func (s *Search) setResults(results []message.SearchResult) {
	items := make([]list.Item, len(results))
	for i, result := range results {
		items[i] = NewSearchResultItem(s.com.Styles, result, s.query)
	}
	s.list.SetItems(items...)
	s.list.SelectFirst()
}

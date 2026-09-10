package dialog

import (
	"fmt"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/dustin/go-humanize"
	"github.com/sahilm/fuzzy"
)

// QuestionIndexItem renders one user question as a list row: the text
// preview as the title, with the question ordinal and the time it was
// asked shown in the info column.
type QuestionIndexItem struct {
	*list.Versioned
	t       *styles.Styles
	msg     message.Message
	ordinal int
	m       fuzzy.Match
	cache   map[int]string
	focused bool
}

var _ ListItem = (*QuestionIndexItem)(nil)

// NewQuestionIndexItem creates a question index list item.
func NewQuestionIndexItem(t *styles.Styles, msg message.Message, ordinal int) *QuestionIndexItem {
	return &QuestionIndexItem{
		Versioned: list.NewVersioned(),
		t:         t,
		msg:       msg,
		ordinal:   ordinal,
	}
}

// Finished implements list.Item.
func (q *QuestionIndexItem) Finished() bool { return true }

// Filter returns the filterable value of the question (its text).
func (q *QuestionIndexItem) Filter() string { return q.msg.Content().Text }

// ID returns the unique message id.
func (q *QuestionIndexItem) ID() string { return q.msg.ID }

// MessageID returns the underlying message id selected by the dialog.
func (q *QuestionIndexItem) MessageID() string { return q.msg.ID }

// SetMatch implements list.MatchSettable.
func (q *QuestionIndexItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(q.m, m) {
		return
	}
	q.cache = nil
	q.m = m
	if q.Versioned != nil {
		q.Bump()
	}
}

// SetFocused implements list.Focusable.
func (q *QuestionIndexItem) SetFocused(focused bool) {
	if q.focused == focused {
		return
	}
	q.cache = nil
	q.focused = focused
	if q.Versioned != nil {
		q.Bump()
	}
}

// Render implements list.Item. Each question renders as one line: the
// text preview (title) with the ordinal and time in the info column.
func (q *QuestionIndexItem) Render(width int) string {
	if q.cache == nil {
		q.cache = make(map[int]string)
	}
	if cached, ok := q.cache[width]; ok {
		return cached
	}

	styles := ListItemStyles{
		ItemBlurred:     q.t.Dialog.NormalItem,
		ItemFocused:     q.t.Dialog.SelectedItem,
		InfoTextBlurred: q.t.Dialog.Sessions.InfoBlurred,
		InfoTextFocused: q.t.Dialog.Sessions.InfoFocused,
	}

	title := q.msg.Content().Text
	if title == "" {
		title = "(empty question)"
	}
	info := fmt.Sprintf("%d · %s", q.ordinal, humanize.Time(time.Unix(q.msg.CreatedAt, 0)))

	content := renderItem(styles, title, info, q.focused, width, q.cache, &q.m)
	q.cache[width] = content
	return content
}

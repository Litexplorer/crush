package dialog

import (
	"strings"
	"time"

	"github.com/charmbracelet/crush/internal/message"
	"github.com/charmbracelet/crush/internal/ui/list"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/dustin/go-humanize"
)

// maxPreviewLen is the longest message preview rendered in a search
// result row before it is truncated with an ellipsis.
const maxPreviewLen = 60

// SearchResultItem renders one message search result: the session title,
// a relative timestamp, and a preview of the matched message with query
// occurrences underlined.
type SearchResultItem struct {
	*list.Versioned
	t       *styles.Styles
	Result  message.SearchResult
	query   string
	preview string
	focused bool
	cache   map[int]string
}

var _ list.Item = (*SearchResultItem)(nil)
var _ list.Focusable = (*SearchResultItem)(nil)

// NewSearchResultItem creates a search result list item.
func NewSearchResultItem(t *styles.Styles, result message.SearchResult, query string) *SearchResultItem {
	return &SearchResultItem{
		Versioned: list.NewVersioned(),
		t:         t,
		Result:    result,
		query:     query,
		preview:   searchPreview(result.Message.Content().Text, query),
	}
}

// Finished implements list.Item.
func (s *SearchResultItem) Finished() bool { return true }

// SessionID returns the session the matched message belongs to.
func (s *SearchResultItem) SessionID() string { return s.Result.Message.SessionID }

// MessageID returns the matched message id.
func (s *SearchResultItem) MessageID() string { return s.Result.Message.ID }

// SetFocused implements list.Focusable.
func (s *SearchResultItem) SetFocused(focused bool) {
	if s.focused == focused {
		return
	}
	s.cache = nil
	s.focused = focused
	if s.Versioned != nil {
		s.Bump()
	}
}

// Render implements list.Item. Each result renders as two lines: the
// session title with a relative timestamp, then the message preview
// with the query occurrences underlined.
func (s *SearchResultItem) Render(width int) string {
	if s.cache == nil {
		s.cache = make(map[int]string)
	}
	if cached, ok := s.cache[width]; ok {
		return cached
	}

	styles := ListItemStyles{
		ItemBlurred:     s.t.Dialog.NormalItem,
		ItemFocused:     s.t.Dialog.SelectedItem,
		InfoTextBlurred: s.t.Dialog.Sessions.InfoBlurred,
		InfoTextFocused: s.t.Dialog.Sessions.InfoFocused,
	}

	title := s.Result.SessionTitle
	if title == "" {
		title = "(untitled session)"
	}
	info := humanize.Time(time.Unix(s.Result.Message.CreatedAt, 0))
	line1 := renderItem(styles, title, info, s.focused, width, s.cache, nil)

	preview := s.preview
	if preview == "" {
		preview = "(no text content)"
	}
	preview = highlightQuery(preview, s.query)

	lineStyle := styles.ItemBlurred
	if s.focused {
		lineStyle = styles.ItemFocused
	}
	lineWidth := max(0, width-lineStyle.GetHorizontalFrameSize())
	line2 := lineStyle.Render(ansi.Truncate(preview, lineWidth, "…"))

	content := line1 + "\n" + line2
	s.cache[width] = content
	return content
}

// highlightQuery underlines every case-insensitive occurrence of query
// in text using the same underline technique as session list items.
func highlightQuery(text, query string) string {
	if query == "" {
		return text
	}
	ranges := queryVisibleRanges(text, query)
	if len(ranges) == 0 {
		return text
	}
	var parts []string
	lastPos := 0
	for _, rng := range ranges {
		if rng.start > lastPos {
			parts = append(parts, ansi.Cut(text, lastPos, rng.start))
		}
		parts = append(
			parts,
			ansi.NewStyle().Underline(true).String(),
			ansi.Cut(text, rng.start, rng.stop+1),
			ansi.NewStyle().Underline(false).String(),
		)
		lastPos = rng.stop + 1
	}
	if lastPos < ansi.StringWidth(text) {
		parts = append(parts, ansi.Cut(text, lastPos, ansi.StringWidth(text)))
	}
	return strings.Join(parts, "")
}

// queryVisibleRanges finds every case-insensitive occurrence of query in
// text and converts the byte ranges to visible (display) positions.
func queryVisibleRanges(text, query string) []struct{ start, stop int } {
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)
	var ranges []struct{ start, stop int }
	bytePos := 0
	for {
		idx := strings.Index(lowerText[bytePos:], lowerQuery)
		if idx < 0 {
			break
		}
		startByte := bytePos + idx
		stopByte := startByte + len(query)
		start, stop := bytePosToVisibleCharPos(text, [2]int{startByte, stopByte})
		ranges = append(ranges, struct{ start, stop int }{start, stop})
		bytePos = stopByte
	}
	return ranges
}

// searchPreview trims the message text to a window around the first
// query match so the highlight is always visible.
func searchPreview(text, query string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	lower := []rune(strings.ToLower(text))
	lq := []rune(strings.ToLower(strings.TrimSpace(query)))

	start, end := 0, len(runes)
	if len(lq) > 0 {
		idx := -1
		for i := 0; i+len(lq) <= len(lower); i++ {
			if string(lower[i:i+len(lq)]) == string(lq) {
				idx = i
				break
			}
		}
		if idx >= 0 {
			start = max(0, idx-10)
			end = min(len(runes), idx+len(lq)+30)
		}
	}
	win := string(runes[start:end])
	if start > 0 {
		win = "…" + win
	}
	if end < len(runes) {
		win += "…"
	}
	if ansi.StringWidth(win) > maxPreviewLen {
		win = ansi.Truncate(win, maxPreviewLen, "…")
	}
	return win
}

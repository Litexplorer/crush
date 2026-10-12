package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// altBackspace / altDelete / altLeft / altRight are the key presses the
// textarea binds to its word operations.
var (
	altBackspace = tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt}
	altDelete    = tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModAlt}
	altLeft      = tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}
	altRight     = tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt}
)

// newCJKWordTestUI builds a fully wired UI focused on the prompt. The word
// handlers run inside handleKeyPressMsg, which needs the dialog overlay,
// keymap and attachments to be present.
func newCJKWordTestUI(t testing.TB) *UI {
	t.Helper()
	u := newFrameTestUI(t)
	u.focus = uiFocusEditor
	u.textarea.Focus()
	return u
}

func TestHandleKeyPressMsgDeletesCJKWordBackward(t *testing.T) {
	u := newCJKWordTestUI(t)
	u.textarea.SetValue("你好世界")
	u.textarea.MoveToEnd()

	u.handleKeyPressMsg(altBackspace)

	if got := u.textarea.Value(); got != "你好" {
		t.Fatalf("Value() = %q, want %q", got, "你好")
	}
	if got := u.textarea.Column(); got != 2 {
		t.Fatalf("Column() = %d, want 2", got)
	}
}

func TestHandleKeyPressMsgDeletesCJKWordForward(t *testing.T) {
	u := newCJKWordTestUI(t)
	u.textarea.SetValue("你好世界")
	u.textarea.MoveToBegin()

	u.handleKeyPressMsg(altDelete)

	if got := u.textarea.Value(); got != "世界" {
		t.Fatalf("Value() = %q, want %q", got, "世界")
	}
	if got := u.textarea.Column(); got != 0 {
		t.Fatalf("Column() = %d, want 0", got)
	}
}

func TestHandleKeyPressMsgMovesByCJKWord(t *testing.T) {
	u := newCJKWordTestUI(t)
	u.textarea.SetValue("你好世界")
	u.textarea.MoveToEnd()

	u.handleKeyPressMsg(altLeft)

	if got := u.textarea.Value(); got != "你好世界" {
		t.Fatalf("Value() = %q, want unchanged %q", got, "你好世界")
	}
	if got := u.textarea.Column(); got != 2 {
		t.Fatalf("Column() after alt+left = %d, want 2", got)
	}

	u.handleKeyPressMsg(altRight)

	if got := u.textarea.Column(); got != 4 {
		t.Fatalf("Column() after alt+right = %d, want 4", got)
	}
}

func TestHandleKeyPressMsgKeepsNativeASCIIBehaviour(t *testing.T) {
	u := newCJKWordTestUI(t)
	u.textarea.SetValue("hello world")
	u.textarea.MoveToEnd()

	u.handleKeyPressMsg(altBackspace)

	if got := u.textarea.Value(); got != "hello " {
		t.Fatalf("Value() = %q, want %q", got, "hello ")
	}
}

func TestHandleKeyPressMsgDeletesSingleWordRunViaTextarea(t *testing.T) {
	u := newCJKWordTestUI(t)
	u.textarea.SetValue("你好世界")
	u.textarea.SetCursorColumn(2)

	// Inside one CJK word the handler defers to the textarea, which treats the
	// whole run as a single word.
	u.handleKeyPressMsg(altBackspace)

	if got := u.textarea.Value(); got != "世界" {
		t.Fatalf("Value() = %q, want %q", got, "世界")
	}
	if got := u.textarea.Column(); got != 0 {
		t.Fatalf("Column() = %d, want 0", got)
	}
}

func TestHandleKeyPressMsgKeepsTextareaWidth(t *testing.T) {
	u := newCJKWordTestUI(t)
	u.textarea.SetValue("你好世界")
	u.textarea.MoveToEnd()
	wantWidth := u.textarea.Width()
	wantMaxWidth := u.textarea.MaxWidth

	for range 5 {
		u.textarea.SetValue("你好世界")
		u.textarea.MoveToEnd()
		u.handleKeyPressMsg(altBackspace)
	}

	if got := u.textarea.Width(); got != wantWidth {
		t.Fatalf("Width() after repeated edits = %d, want %d", got, wantWidth)
	}
	if got := u.textarea.MaxWidth; got != wantMaxWidth {
		t.Fatalf("MaxWidth after repeated edits = %d, want %d", got, wantMaxWidth)
	}
}

func TestHandleKeyPressMsgDeletesCJKWordOnMiddleLine(t *testing.T) {
	u := newCJKWordTestUI(t)
	u.textarea.SetValue("你好世界\nabc")
	u.textarea.MoveToEnd()
	u.textarea.CursorUp()
	u.textarea.SetCursorColumn(4)

	u.handleKeyPressMsg(altBackspace)

	if got := u.textarea.Value(); got != "你好\nabc" {
		t.Fatalf("Value() = %q, want %q", got, "你好\nabc")
	}
	if got := u.textarea.Line(); got != 0 {
		t.Fatalf("Line() = %d, want 0", got)
	}
	if got := u.textarea.Column(); got != 2 {
		t.Fatalf("Column() = %d, want 2", got)
	}
}
package model

import (
	"testing"

	"charm.land/bubbles/v2/textarea"
)

func TestWordStartBackward(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		col     int
		want    int
		wantOK  bool
	}{
		{name: "pure CJK deletes last word", line: "你好世界", col: 4, want: 2, wantOK: true},
		{name: "pure CJK single word falls through", line: "你好世界", col: 2, want: 0, wantOK: false},
		{name: "ascii falls through", line: "hello world", col: 11, want: 0, wantOK: false},
		{name: "ascii run after CJK", line: "世界hello", col: 7, want: 2, wantOK: true},
		{name: "CJK run after ascii", line: "hello世界", col: 7, want: 5, wantOK: true},
		{name: "trailing spaces are skipped", line: "你好世界   ", col: 7, want: 2, wantOK: true},
		{name: "space separated single word falls through", line: "你好 世界", col: 5, want: 0, wantOK: false},
		{name: "word after space", line: "你好 世界你好", col: 7, want: 5, wantOK: true},
		{name: "at line start falls through", line: "你好世界", col: 0, want: 0, wantOK: false},
		{name: "empty line falls through", line: "", col: 0, want: 0, wantOK: false},
		{name: "whitespace only falls through", line: "   ", col: 3, want: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := wordStartBackward([]rune(tt.line), tt.col)
			if ok != tt.wantOK || (ok && got != tt.want) {
				t.Fatalf("wordStartBackward(%q, %d) = (%d, %v), want (%d, %v)",
					tt.line, tt.col, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestWordEndForward(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		col    int
		want   int
		wantOK bool
	}{
		{name: "pure CJK moves over first word", line: "你好世界", col: 0, want: 2, wantOK: true},
		{name: "pure CJK single word falls through", line: "你好世界", col: 2, want: 0, wantOK: false},
		{name: "ascii falls through", line: "hello world", col: 0, want: 0, wantOK: false},
		{name: "CJK run after ascii", line: "hello世界", col: 0, want: 5, wantOK: true},
		{name: "ascii run after CJK", line: "世界hello", col: 0, want: 2, wantOK: true},
		{name: "leading spaces are skipped", line: "   你好世界", col: 0, want: 5, wantOK: true},
		{name: "word after space", line: "你好 世界你好", col: 3, want: 5, wantOK: true},
		{name: "at line end falls through", line: "你好世界", col: 4, want: 0, wantOK: false},
		{name: "empty line falls through", line: "", col: 0, want: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := wordEndForward([]rune(tt.line), tt.col)
			if ok != tt.wantOK || (ok && got != tt.want) {
				t.Fatalf("wordEndForward(%q, %d) = (%d, %v), want (%d, %v)",
					tt.line, tt.col, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestReplaceTextareaLineSingleLine(t *testing.T) {
	ta := textarea.New()
	ta.SetWidth(40)
	ta.SetValue("你好世界")
	ta.MoveToEnd()

	replaceTextareaLine(&ta, "你好", 2)

	if got := ta.Value(); got != "你好" {
		t.Fatalf("Value() = %q, want %q", got, "你好")
	}
	if got := ta.Line(); got != 0 {
		t.Fatalf("Line() = %d, want 0", got)
	}
	if got := ta.Column(); got != 2 {
		t.Fatalf("Column() = %d, want 2", got)
	}
}

func TestReplaceTextareaLineLastLine(t *testing.T) {
	ta := textarea.New()
	ta.SetWidth(40)
	ta.SetValue("a\n你好世界")
	ta.MoveToEnd()

	replaceTextareaLine(&ta, "你好", 2)

	if got := ta.Value(); got != "a\n你好" {
		t.Fatalf("Value() = %q, want %q", got, "a\n你好")
	}
	if got := ta.Line(); got != 1 {
		t.Fatalf("Line() = %d, want 1", got)
	}
	if got := ta.Column(); got != 2 {
		t.Fatalf("Column() = %d, want 2", got)
	}
}

func TestReplaceTextareaLineMiddleLine(t *testing.T) {
	ta := textarea.New()
	ta.SetWidth(40)
	ta.SetValue("你好世界\nb")
	ta.MoveToEnd()
	ta.CursorUp()

	replaceTextareaLine(&ta, "你好", 2)

	if got := ta.Value(); got != "你好\nb" {
		t.Fatalf("Value() = %q, want %q", got, "你好\nb")
	}
	if got := ta.Line(); got != 0 {
		t.Fatalf("Line() = %d, want 0", got)
	}
	if got := ta.Column(); got != 2 {
		t.Fatalf("Column() = %d, want 2", got)
	}
}

func TestReplaceTextareaLinePreservesWidth(t *testing.T) {
	ta := textarea.New()
	ta.Prompt = "┃ "
	ta.SetWidth(120)
	wantWidth := ta.Width()
	wantMaxWidth := ta.MaxWidth

	for range 5 {
		ta.SetValue("你好世界")
		ta.MoveToEnd()
		replaceTextareaLine(&ta, "你好", 2)
	}

	if got := ta.Width(); got != wantWidth {
		t.Fatalf("Width() after repeated edits = %d, want %d", got, wantWidth)
	}
	if got := ta.MaxWidth; got != wantMaxWidth {
		t.Fatalf("MaxWidth after repeated edits = %d, want %d", got, wantMaxWidth)
	}
}

func TestContainsCJK(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{in: "hello", want: false},
		{in: "abc123!", want: false},
		{in: "café", want: false},
		{in: "你好", want: true},
		{in: "hello世界", want: true},
		{in: "こんにちは", want: true},
		{in: "안녕하세요", want: true},
	}

	for _, tt := range tests {
		if got := containsCJK(tt.in); got != tt.want {
			t.Errorf("containsCJK(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestWarmCJKSegmenterForSkipsASCII(t *testing.T) {
	if cmd := warmCJKSegmenterFor("hello world"); cmd != nil {
		t.Fatal("warmCJKSegmenterFor(ascii) should return nil")
	}
}

func TestWarmCJKSegmenterForLoadsDictionary(t *testing.T) {
	if cmd := warmCJKSegmenterFor("你好世界"); cmd != nil {
		cmd()
	}
	if cjkSegmenter() == nil {
		t.Fatal("segmenter unavailable after warm-up")
	}
	if !cjkSegReady.Load() {
		t.Fatal("segmenter not marked ready after load")
	}
}

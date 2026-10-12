package model

import (
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/go-ego/gse"
)

// cjkSegmentWindow caps how much of a whitespace-delimited run is handed to the
// segmenter. A word boundary next to the cursor never depends on text further
// away than this, and the cap keeps a pathological pasted line cheap.
const cjkSegmentWindow = 512

// noSoftWrapWidth is a width large enough that no prompt line is soft-wrapped
// while the cursor is restored after SetValue.
const noSoftWrapWidth = 1 << 16

var (
	cjkSegOnce  sync.Once
	cjkSeg      *gse.Segmenter
	cjkSegReady atomic.Bool
)

// wordDirection selects whether a word operation walks towards the start or the
// end of the line.
type wordDirection int

const (
	wordBackward wordDirection = iota
	wordForward
)

// runeRange is a half-open rune range within a line.
type runeRange struct {
	start int
	end   int
}

// cjkSegmenter returns the lazily built shared segmenter.
//
// 参数：无
// 核心流程：
// 1. 首次调用加载内嵌中文词典（SkipSubSeg/SkipLog 压低首次加载成本）
// 2. 加载完成置位 cjkSegReady，供后台预热判断
// 3. 加载失败返回 nil，调用方退回 textarea 原生空格分词
func cjkSegmenter() *gse.Segmenter {
	cjkSegOnce.Do(func() {
		var seg gse.Segmenter
		seg.SkipSubSeg = true
		seg.SkipLog = true
		if err := seg.LoadDictEmbed("zh"); err != nil {
			return
		}
		cjkSeg = &seg
		cjkSegReady.Store(true)
	})
	return cjkSeg
}

// warmCJKSegmenterFor starts the dictionary load in the background once the
// prompt holds CJK text, so the first word deletion afterwards does not stall.
//
// 参数：value 输入框当前全文
// 核心流程：
// 1. 词典已就绪或文本不含 CJK 时返回 nil，非中文用户零开销
// 2. 否则返回后台 Cmd 触发懒加载（Cmd 在独立 goroutine 中执行，不阻塞界面）
func warmCJKSegmenterFor(value string) tea.Cmd {
	if cjkSegReady.Load() || !containsCJK(value) {
		return nil
	}
	return func() tea.Msg {
		cjkSegmenter()
		return nil
	}
}

// containsCJK reports whether s holds a rune that needs dictionary-based
// segmentation rather than whitespace splitting.
//
// 参数：s 待检查的片段
// 核心流程：遍历 rune，命中汉字/假名/谚文即返回 true
func containsCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) ||
			unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) ||
			unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// cjkSegments splits s into word ranges expressed as rune offsets.
//
// 参数：s 不含空白的片段
// 核心流程：
// 1. 片段不含 CJK 时返回 nil，把行为让给 textarea 原生空格规则
// 2. 调用共享分词器，把 gse 的字节偏移换算成 rune 偏移
// 3. 只切出一个词（或分词器不可用）时返回 nil，等价于整段即一个词
func cjkSegments(s string) []runeRange {
	if !containsCJK(s) {
		return nil
	}
	seg := cjkSegmenter()
	if seg == nil {
		return nil
	}
	segments := seg.Segment([]byte(s))
	if len(segments) <= 1 {
		return nil
	}
	out := make([]runeRange, 0, len(segments))
	for _, segment := range segments {
		out = append(out, runeRange{
			start: utf8.RuneCountInString(s[:segment.Start()]),
			end:   utf8.RuneCountInString(s[:segment.End()]),
		})
	}
	return out
}

// wordStartBackward returns the rune index where the word preceding col begins.
//
// 参数：runes 当前行的 rune 序列；col 光标所在 rune 下标
// 核心流程：
// 1. 先跳过光标左侧的连续空白（与 textarea 原生行为一致）
// 2. 向左收敛到不含空白的 run，得到 [start, col)
// 3. 对 run 取尾部窗口分词，返回最后一个词的首 rune
// 4. 非 CJK、或整段即一个词、或已在行首时返回 false，交回原生逻辑
func wordStartBackward(runes []rune, col int) (int, bool) {
	if col <= 0 || col > len(runes) {
		return 0, false
	}

	i := col
	for i > 0 && unicode.IsSpace(runes[i-1]) {
		i--
	}
	if i == 0 {
		return 0, false
	}

	start := i
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}

	base := start
	if i-start > cjkSegmentWindow {
		base = i - cjkSegmentWindow
	}
	words := cjkSegments(string(runes[base:i]))
	if len(words) < 2 {
		return 0, false
	}
	last := words[len(words)-1]
	if last.start == 0 {
		return 0, false
	}
	return base + last.start, true
}

// wordEndForward returns the rune index where the word following col ends.
//
// 参数：runes 当前行的 rune 序列；col 光标所在 rune 下标
// 核心流程：
// 1. 先跳过光标右侧的连续空白
// 2. 向右收敛到不含空白的 run，得到 [col 之后, end)
// 3. 对 run 取头部窗口分词，返回第一个词的尾 rune
// 4. 非 CJK、或整段即一个词、或已在行尾时返回 false，交回原生逻辑
func wordEndForward(runes []rune, col int) (int, bool) {
	if col < 0 || col >= len(runes) {
		return 0, false
	}

	i := col
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		i++
	}
	if i >= len(runes) {
		return 0, false
	}

	end := i
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}

	limit := end
	if end-i > cjkSegmentWindow {
		limit = i + cjkSegmentWindow
	}
	words := cjkSegments(string(runes[i:limit]))
	if len(words) < 2 {
		return 0, false
	}
	first := words[0]
	if first.end >= limit-i {
		return 0, false
	}
	return i + first.end, true
}

// replaceTextareaLine swaps the cursor's logical line for content and moves the
// cursor to col within it, leaving every other line untouched.
//
// 参数：ta 目标 textarea；content 该行新内容；col 新光标列（rune 下标）
// 核心流程：
// 1. 拆分整段文本，只替换光标所在行，其余行原样保留
// 2. 临时放宽宽度关闭软换行，让 SetValue 后一次 CursorDown 恰好等于一逻辑行
// 3. 逐行下移复位光标到目标行与目标列
// 4. 按实测 gutter 宽度还原外宽，使高度按真实软换行重新计算
func replaceTextareaLine(ta *textarea.Model, content string, col int) {
	lines := strings.Split(ta.Value(), "\n")
	row := ta.Line()
	if row < 0 || row >= len(lines) {
		return
	}
	lines[row] = content

	// SetWidth takes the outer width while Width reports the inner one, so the
	// gutter has to be measured rather than inferred. Feeding the inner width
	// straight back into SetWidth would shave the gutter off on every edit.
	// MaxWidth caps the input width, so it is lifted for the duration of the
	// measurement or the widening would silently clamp.
	innerWidth := ta.Width()
	maxWidth := ta.MaxWidth
	ta.MaxWidth = 0
	ta.SetWidth(noSoftWrapWidth)
	reservedWidth := noSoftWrapWidth - ta.Width()

	ta.SetValue(strings.Join(lines, "\n"))
	ta.MoveToBegin()
	for range row {
		ta.CursorDown()
	}
	ta.SetCursorColumn(col)

	ta.SetWidth(innerWidth + reservedWidth)
	ta.MaxWidth = maxWidth
}

// handleCJKWordKeys intercepts the textarea word keys and applies CJK-aware
// word boundaries, falling through untouched for every other key.
//
// 参数：msg 当前按键
// 核心流程：
// 1. 内联编辑器或存在选区时直接放行，避免打断既有交互
// 2. 只接管 Alt+Backspace / Alt+Delete / Alt+← / Alt+→ 四个词级键
// 3. 命中则按 CJK 边界改写或移动；未命中返回 false 交回 textarea 原生逻辑
func (m *UI) handleCJKWordKeys(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if m.activeInline != nil || m.textarea.HasSelection() {
		return false, nil
	}

	km := m.textarea.KeyMap
	switch {
	case key.Matches(msg, km.DeleteWordBackward):
		return m.applyCJKWordEdit(wordBackward)
	case key.Matches(msg, km.DeleteWordForward):
		return m.applyCJKWordEdit(wordForward)
	case key.Matches(msg, km.WordBackward):
		return m.applyCJKWordMove(wordBackward)
	case key.Matches(msg, km.WordForward):
		return m.applyCJKWordMove(wordForward)
	}
	return false, nil
}

// currentLineRunes returns the cursor's logical line and its rune column.
//
// 参数：无
// 核心流程：按 Line() 取出当前行文本转成 rune，与 Column() 一并返回
func (m *UI) currentLineRunes() ([]rune, int) {
	lines := strings.Split(m.textarea.Value(), "\n")
	row := m.textarea.Line()
	if row < 0 || row >= len(lines) {
		return nil, 0
	}
	return []rune(lines[row]), m.textarea.Column()
}

// applyCJKWordEdit deletes one CJK word in the given direction.
//
// 参数：dir 删除方向
// 核心流程：
// 1. 取当前行与光标列，求该方向的 CJK 词边界
// 2. 边界求不出（非 CJK/行首行尾）时返回 false，让 textarea 原生处理
// 3. 拼出新行内容，光标停在删除起点，并回报高度变化
func (m *UI) applyCJKWordEdit(dir wordDirection) (bool, tea.Cmd) {
	runes, col := m.currentLineRunes()
	if runes == nil {
		return false, nil
	}

	var start, end int
	var ok bool
	if dir == wordBackward {
		start, ok = wordStartBackward(runes, col)
		end = col
	} else {
		start = col
		end, ok = wordEndForward(runes, col)
	}
	if !ok || start >= end {
		return false, nil
	}

	next := string(runes[:start]) + string(runes[end:])
	prevHeight := m.textarea.Height()
	replaceTextareaLine(&m.textarea, next, start)
	return true, m.handleTextareaHeightChange(prevHeight)
}

// applyCJKWordMove moves the cursor by one CJK word in the given direction.
//
// 参数：dir 移动方向
// 核心流程：
// 1. 取当前行与光标列，求该方向的 CJK 词边界
// 2. 求不出时返回 false，让 textarea 原生按空格跳词
// 3. 命中则只移动光标，不改动文本与高度
func (m *UI) applyCJKWordMove(dir wordDirection) (bool, tea.Cmd) {
	runes, col := m.currentLineRunes()
	if runes == nil {
		return false, nil
	}

	var target int
	var ok bool
	if dir == wordBackward {
		target, ok = wordStartBackward(runes, col)
	} else {
		target, ok = wordEndForward(runes, col)
	}
	if !ok {
		return false, nil
	}

	m.textarea.SetCursorColumn(target)
	return true, nil
}

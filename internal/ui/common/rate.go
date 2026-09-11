package common

import (
	"fmt"
	"math"
	"sync"
	"time"
	"unicode/utf8"
)

// rateWindow is the trailing window the live output rate is averaged over.
const rateWindow = time.Second

// rateHistory is how much sample history is retained. It must be longer
// than rateWindow so the window always has a sample to anchor on.
const rateHistory = 2 * rateWindow

// rateEwmaAlpha smooths the step change that happens whenever the oldest
// sample leaves the trailing window.
const rateEwmaAlpha = 0.3

// rateMinSpan is how much history is required before a rate is reported.
// Without it the first couple of deltas are divided by a few milliseconds
// and a normal burst of tokens reads as thousands of tok/s.
const rateMinSpan = 500 * time.Millisecond

// rateSample is one observation of the cumulative output token count.
type rateSample struct {
	at     time.Time
	tokens int64
}

// tokenRate tracks the live token emission rate for the current turn. It
// mirrors turnTimer: package-level state, reset when a turn starts, read
// from the render path on every animation frame.
var tokenRate struct {
	mu       sync.Mutex
	samples  []rateSample
	smoothed float64
	hasValue bool
}

/*
"""
无参数
核心流程:
1. 丢弃上一回合累积的采样窗口与平滑值
2. 由 StartTurn 在回合起点调用，保证速率只统计本回合
"""
*/
func StartRate() {
	tokenRate.mu.Lock()
	defer tokenRate.mu.Unlock()
	tokenRate.samples = tokenRate.samples[:0]
	tokenRate.smoothed = 0
	tokenRate.hasValue = false
}

/*
"""
text: 助手消息正文的累计文本
thinking: 助手消息思考内容的累计文本
核心流程:
1. 估算正文与思考的累计 token 数（含 CJK 权重）
2. 累计值回退时丢弃整个窗口（provider 重试或新 assistant 消息都会回退）
3. 追加本次采样，并裁剪掉超出 rateHistory 的旧样本
4. 历史不足 rateMinSpan 时不产出速率，避免开局把突发当成高速率
5. 用窗口内的差分速率做 EWMA 平滑，供渲染路径直接读取
"""
*/
func ObserveTokens(text, thinking string) {
	tokens := approxTokens(text) + approxTokens(thinking)

	tokenRate.mu.Lock()
	defer tokenRate.mu.Unlock()

	now := time.Now()
	previous := int64(0)
	if n := len(tokenRate.samples); n > 0 {
		previous = tokenRate.samples[n-1].tokens
	}
	if tokens < previous {
		tokenRate.samples = tokenRate.samples[:0]
		tokenRate.smoothed = 0
		tokenRate.hasValue = false
	}
	tokenRate.samples = append(tokenRate.samples, rateSample{at: now, tokens: tokens})

	cut := now.Add(-rateHistory)
	drop := 0
	for drop < len(tokenRate.samples)-1 && tokenRate.samples[drop].at.Before(cut) {
		drop++
	}
	if drop > 0 {
		tokenRate.samples = append(tokenRate.samples[:0], tokenRate.samples[drop:]...)
	}

	if len(tokenRate.samples) < 2 {
		return
	}
	if now.Sub(tokenRate.samples[0].at) < rateMinSpan {
		return
	}
	rate := rateFromSamples(tokenRate.samples, now)
	if tokenRate.hasValue {
		tokenRate.smoothed = rateEwmaAlpha*rate + (1-rateEwmaAlpha)*tokenRate.smoothed
	} else {
		tokenRate.smoothed = rate
		tokenRate.hasValue = true
	}
}

/*
"""
无参数；返回 "42 tok/s" 形式的字符串，尚未产生速率时返回空串
核心流程:
1. 尚未获得有效速率窗口时返回空串
2. 距最后一次输出超过一个窗口说明已停止吐字，返回 "0 tok/s"
3. 否则返回 EWMA 平滑后的速率并取整，避免 20fps 下数字抖动
4. 本函数只读，可以从渲染路径每个动画帧安全调用
"""
*/
func TokensPerSecond() string {
	tokenRate.mu.Lock()
	defer tokenRate.mu.Unlock()

	if !tokenRate.hasValue || len(tokenRate.samples) == 0 {
		return ""
	}
	// 距最后一次输出已超过一个窗口，说明当前没有在吐字
	if time.Since(tokenRate.samples[len(tokenRate.samples)-1].at) >= rateWindow {
		return "0 tok/s"
	}
	return fmt.Sprintf("%d tok/s", int(math.Round(tokenRate.smoothed)))
}

/*
"""
samples: 按时间升序排列的（时间, 累计 token）采样点
now: 计算速率的当前时刻
返回窗口内的平均 token 速率；样本不足或跨度为 0 时返回 0
核心流程:
1. 以最后一个采样点为基准，锚点取窗口起点之前最近的采样点
2. 流开始不足一个窗口时退回首个采样点，速率会自然爬升
3. 速率为（末累计 - 锚累计）/ 时间跨度
4. 距最后一次输出超过一个窗口时返回 0，避免展示过期速率
"""
*/
func rateFromSamples(samples []rateSample, now time.Time) float64 {
	if len(samples) < 2 {
		return 0
	}
	last := samples[len(samples)-1]
	if now.Sub(last.at) >= rateWindow {
		return 0
	}

	anchor := samples[0]
	cut := now.Add(-rateWindow)
	for _, s := range samples {
		if s.at.After(cut) {
			break
		}
		anchor = s
	}
	span := last.at.Sub(anchor.at)
	if span <= 0 {
		return 0
	}
	return float64(last.tokens-anchor.tokens) / span.Seconds()
}

/*
"""
s: 待估算的文本
核心流程:
1. 分别统计 ASCII 与非 ASCII 字符数
2. ASCII 按约 4 字符 1 token 计，非 ASCII 按 1 字符 1 token 计
3. 与 internal/agent/usage_fallback.go 的 approxTokenCount 保持同一口径
"""
*/
func approxTokens(s string) int64 {
	if s == "" {
		return 0
	}
	var ascii, nonASCII int
	for _, r := range s {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			nonASCII++
		}
	}
	return int64((ascii+3)/4 + nonASCII)
}

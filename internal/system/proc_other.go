//go:build !linux

// proc_other.go
//
// 核心流程：非 Linux 平台没有 /proc，指标采集未实现 —— 这里只提供主机名/平台/核数等
// 可移植信息，Sample 会返回 Supported=false，前端据此展示「不支持」。
package system

import (
	"os"
	"runtime"
)

// readCounters 非 Linux 平台一律视为采集不可用。
func readCounters() (counters, bool) {
	return counters{}, false
}

// baseHostInfo 返回可移植的主机信息。
func baseHostInfo() (string, string, int, float64) {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "unknown"
	}
	return name, runtime.GOOS, runtime.NumCPU(), 0
}

//go:build linux

// metrics_test.go
//
// 核心流程：验证采样器在真实 /proc 上的输出形状 —— 首帧标记 warming、第二帧给出正的时间窗口与速率，
// 以及差分母（时间片）换算逻辑本身。
package system

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSampleReportsHostSnapshot 校验首帧读数：平台可读、主机信息与内存/磁盘都有值。
func TestSampleReportsHostSnapshot(t *testing.T) {
	sampler := New()
	snapshot := sampler.Sample()

	require.True(t, snapshot.Supported)
	require.NotEmpty(t, snapshot.Host.Hostname)
	require.Equal(t, "linux", snapshot.Host.Platform)
	require.Positive(t, snapshot.Host.NumCPU)
	require.Positive(t, snapshot.Memory.TotalBytes)
	require.NotEmpty(t, snapshot.Disks)
	require.InDelta(t, 100.0, snapshot.Memory.Percent+float64(snapshot.Memory.AvailableBytes)/float64(snapshot.Memory.TotalBytes)*100, 1.5)
}

// TestSampleSecondFrameReportsRates 校验第二帧：关闭 warming 并给出真实窗口；网卡速率非负。
func TestSampleSecondFrameReportsRates(t *testing.T) {
	sampler := New()
	first := sampler.Sample()
	require.True(t, first.Warming)

	time.Sleep(minInterval + 100*time.Millisecond)
	second := sampler.Sample()

	require.False(t, second.Warming)
	require.Positive(t, second.IntervalMS)
	require.GreaterOrEqual(t, second.CPU.Percent, 0.0)
	require.LessOrEqual(t, second.CPU.Percent, 100.0)
	for _, network := range second.Networks {
		require.GreaterOrEqual(t, network.RxBytesPerSec, 0.0)
		require.GreaterOrEqual(t, network.TxBytesPerSec, 0.0)
	}
}

// TestSampleReusesCacheWithinMinInterval 校验细密轮询复用缓存，避免拿到 0 速率。
func TestSampleReusesCacheWithinMinInterval(t *testing.T) {
	sampler := New()
	_ = sampler.Sample()
	time.Sleep(minInterval + 100*time.Millisecond)
	first := sampler.Sample()
	cached := sampler.Sample()

	require.Equal(t, first.SampledAt, cached.SampledAt)
}

// TestBusyPercent 校验差分母边界：总时间片为 0 或全空闲时不出现负值或除零。
func TestBusyPercent(t *testing.T) {
	require.Equal(t, 0.0, busyPercent(0, 0))
	require.Equal(t, 0.0, busyPercent(100, 100))
	require.Equal(t, 50.0, busyPercent(200, 100))
	require.Equal(t, 100.0, busyPercent(100, 0))
	// 计数回绕（/proc 被重置）时不应出现超过 100 的值
	require.Equal(t, 0.0, busyPercent(10, 20))
}

// metrics.go
//
// 核心流程：把平台层的累积计数（/proc）换算成瞬时指标 ——
// CPU 占用与网卡吞吐都是累积值，只能对两次采样求差分；
// 采样间隔小于 minInterval 时直接复用上次结果，避免细密轮询拿到 0 或抖动值。
package system

import (
	"sync"
	"time"

	"github.com/charmbracelet/crush/internal/proto"
)

// minInterval 两次真实采样之间的最小间隔：比这更密的请求复用缓存快照。
const minInterval = 500 * time.Millisecond

// Sampler 持有上一帧累积计数，按需计算差分指标；零值不可用，需经 New 构造。
type Sampler struct {
	mu       sync.Mutex
	prev     counters
	prevAt   time.Time
	cached   proto.SystemMetrics
	cachedAt time.Time
}

// New 构造采样器：此时不读 /proc，第一次 Sample 才建立基线。
func New() *Sampler {
	return &Sampler{}
}

var (
	defaultSampler *Sampler
	defaultOnce    sync.Once
)

// Default 返回进程内单例。
//
// 指标是全局的，多个 handler 共用一个基线，否则并发请求各自建立基线会让差分窗口互相打架。
func Default() *Sampler {
	defaultOnce.Do(func() { defaultSampler = New() })
	return defaultSampler
}

// Sample 返回当前瞬时指标快照。
//
// 命中缓存（距上次采样不足 minInterval）直接返回；否则读一次平台累积计数，
// 与上一帧求差得到 CPU 百分比与网卡速率。首帧没有上一帧：CPU 按开机以来的均值给出，
// 网卡速率标记 Warming 并置 0。
func (s *Sampler) Sample() proto.SystemMetrics {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if !s.cachedAt.IsZero() && now.Sub(s.cachedAt) < minInterval {
		return s.cached
	}

	cur, ok := readCounters()
	if !ok {
		snapshot := unsupportedSnapshot()
		s.cached, s.cachedAt = snapshot, now
		return snapshot
	}

	snapshot := proto.SystemMetrics{
		SampledAt: now.UnixMilli(),
		Supported: true,
		Host: proto.SystemHost{
			Hostname: cur.hostname,
			Platform: cur.platform,
			NumCPU:   len(cur.perCore),
			UptimeS:  cur.uptime,
		},
		CPU:    cpuSnapshot(cur, s.prev, now.Sub(s.prevAt)),
		Memory: memorySnapshot(cur),
		Disks:  diskSnapshot(cur),
	}

	if s.prevAt.IsZero() {
		snapshot.Warming = true
		snapshot.IntervalMS = int64(cur.uptime * 1000)
		snapshot.Networks = make([]proto.SystemNetwork, 0, len(cur.net))
		for _, intf := range cur.net {
			snapshot.Networks = append(snapshot.Networks, proto.SystemNetwork{
				Name:         intf.name,
				RxTotalBytes: intf.rx,
				TxTotalBytes: intf.tx,
			})
		}
	} else {
		window := now.Sub(s.prevAt)
		snapshot.IntervalMS = window.Milliseconds()
		snapshot.Networks = networkSnapshot(cur, s.prev, window.Seconds())
	}

	s.prev, s.prevAt = cur, now
	s.cached, s.cachedAt = snapshot, now
	return snapshot
}

// cpuSnapshot 计算整体与逐核占用。
//
// 参数：cur 当前累积计数；prev 上一帧；window 两次采样的时间差。
// 核心流程：总时间片差 = 全部字段变化之和，空闲片差 = idle+iowait 变化之和；
// 百分比 = (总差 - 空闲差) / 总差。首帧没有上一帧时按开机以来的累积值计算。
func cpuSnapshot(cur, prev counters, window time.Duration) proto.SystemCPU {
	result := proto.SystemCPU{
		Load1:      cur.load1,
		Load5:      cur.load5,
		Load15:     cur.load15,
		QuotaCores: cur.cpuQuotaCores,
	}

	if window > 0 && prev.cpuTotal > 0 && cur.cpuTotal >= prev.cpuTotal {
		result.Percent = busyPercent(cur.cpuTotal-prev.cpuTotal, cur.cpuIdle-prev.cpuIdle)
		for index, core := range cur.perCore {
			if index >= len(prev.perCore) {
				result.PerCore = append(result.PerCore, 0)
				continue
			}
			base := prev.perCore[index]
			result.PerCore = append(result.PerCore, busyPercent(core.total-base.total, core.idle-base.idle))
		}
		return result
	}

	// 首帧：用开机以来的累积值给出平均占用，避免页面刚打开时没有数字。
	result.Percent = busyPercent(cur.cpuTotal, cur.cpuIdle)
	result.PerCore = make([]float64, 0, len(cur.perCore))
	for _, core := range cur.perCore {
		result.PerCore = append(result.PerCore, busyPercent(core.total, core.idle))
	}
	return result
}

// busyPercent 把时间片增量换算成占用百分比并钳制到 0~100。
func busyPercent(total, idle uint64) float64 {
	if total == 0 || idle >= total {
		return 0
	}
	return clampPercent(float64(total-idle) / float64(total) * 100)
}

// memorySnapshot 把内存累积值换算成字节数。
//
// 参数：cur 当前累积计数（含 cgroup 限额）。
// 核心流程：默认按 /proc/meminfo：总量减可用得已用；读到 cgroup 限额且小于宿主总量时，
// 改为限额口径 —— 已用取 memory.usage 扣掉 inactive_file（不可回收缓存，与 docker stats 一致），
// 可用 = 限额 - 已用；swap 相减得已用 swap。
func memorySnapshot(cur counters) proto.SystemMemory {
	total := cur.memory.total
	var used uint64
	if total > cur.memory.available {
		used = total - cur.memory.available
	}
	limitSource := "host"

	if cur.memoryLimit > 0 && cur.memoryLimit < total {
		total = cur.memoryLimit
		used = cur.memoryUsage
		if cur.memoryInactive <= used {
			used -= cur.memoryInactive
		}
		limitSource = "cgroup"
	}
	var swapUsed uint64
	if cur.memory.swapTotal > cur.memory.swapFree {
		swapUsed = cur.memory.swapTotal - cur.memory.swapFree
	}
	return proto.SystemMemory{
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: total - used,
		Percent:        clampPercent(ratio(used, total) * 100),
		SwapTotalBytes: cur.memory.swapTotal,
		SwapUsedBytes:  swapUsed,
		LimitSource:    limitSource,
	}
}

// diskSnapshot 逐个挂载点换算已用/可用字节与占用百分比。
func diskSnapshot(cur counters) []proto.SystemDisk {
	disks := make([]proto.SystemDisk, 0, len(cur.disks))
	for _, disk := range cur.disks {
		disks = append(disks, proto.SystemDisk{
			Mount:      disk.mount,
			FSType:     disk.fsType,
			TotalBytes: disk.total,
			UsedBytes:  disk.used,
			FreeBytes:  disk.free,
			Percent:    clampPercent(ratio(disk.used, disk.total) * 100),
		})
	}
	return disks
}

// networkSnapshot 由两次采样的收发字节差求每秒速率。
//
// 参数：cur/prev 两帧累积计数；seconds 采样窗口长度（秒）。
// 上一帧没有的网卡（新插的热点等）速率记 0，只报总量。
func networkSnapshot(cur, prev counters, seconds float64) []proto.SystemNetwork {
	networks := make([]proto.SystemNetwork, 0, len(cur.net))
	for _, intf := range cur.net {
		item := proto.SystemNetwork{
			Name:         intf.name,
			RxTotalBytes: intf.rx,
			TxTotalBytes: intf.tx,
		}
		if base, ok := prev.net[intf.name]; ok && seconds > 0 {
			if intf.rx >= base.rx {
				item.RxBytesPerSec = float64(intf.rx-base.rx) / seconds
			}
			if intf.tx >= base.tx {
				item.TxBytesPerSec = float64(intf.tx-base.tx) / seconds
			}
		}
		networks = append(networks, item)
	}
	return networks
}

// unsupportedSnapshot 在平台没有采集实现时返回带原因的空快照，
// 前端据此展示「不支持」而不是显示一堆 0。
func unsupportedSnapshot() proto.SystemMetrics {
	hostname, platform, numCPU, uptime := baseHostInfo()
	return proto.SystemMetrics{
		SampledAt: time.Now().UnixMilli(),
		Supported: false,
		Reason:    "system metrics are not collected on " + platform,
		Host: proto.SystemHost{
			Hostname: hostname,
			Platform: platform,
			NumCPU:   numCPU,
			UptimeS:  uptime,
		},
	}
}

// ratio 返回 part/total，total 为 0 时返回 0，避免除零。
func ratio(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}

// clampPercent 把百分比钳制到 0~100，抹掉浮点误差带来的负值。
func clampPercent(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
}

// types.go
//
// 核心流程：定义采样器与平台采集实现之间的数据契约 —— 全是累积值（开机至今的总量），
// 差值换算在 metrics.go 里完成，平台实现只负责"读一次、原样交上来"。
package system

// coreCounters 单核时间片累积值。
type coreCounters struct {
	total uint64
	idle  uint64
}

// netCounters 单网卡收发字节累积值。
type netCounters struct {
	name string
	rx   uint64
	tx   uint64
}

// memoryCounters 内存累积值（字节）。
type memoryCounters struct {
	total     uint64
	available uint64
	swapTotal uint64
	swapFree  uint64
}

// diskCounters 单个挂载点的容量（字节）。
type diskCounters struct {
	mount  string
	fsType string
	total  uint64
	used   uint64
	free   uint64
}

// counters 一次平台采样的全部累积值。
type counters struct {
	hostname string
	platform string
	uptime   float64

	cpuTotal uint64
	cpuIdle  uint64
	perCore  []coreCounters
	load1    float64
	load5    float64
	load15   float64

	memory memoryCounters
	disks  []diskCounters
	net    map[string]netCounters

	// 容器限额（cgroup v1/v2）：memoryLimit 为 0 表示没有限额，按宿主机口径展示；
	// cpuQuotaCores 为 0 表示 CPU 未设配额
	memoryLimit    uint64
	memoryUsage    uint64
	memoryInactive uint64
	cpuQuotaCores  float64
}

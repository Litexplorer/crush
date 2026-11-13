package proto

// SystemMetrics is the response for GET /v1/system/metrics: a momentary
// snapshot of the host running the server. CPU and network figures are rates
// derived from the delta between two consecutive samples, so callers should
// poll at an interval of at least half a second.
type SystemMetrics struct {
	SampledAt  int64           `json:"sampled_at"`  // unix milliseconds
	IntervalMS int64           `json:"interval_ms"` // delta window used for the rates
	Warming    bool            `json:"warming"`     // true when there was no previous sample yet
	Supported  bool            `json:"supported"`   // false on platforms without a collector
	Reason     string          `json:"reason,omitempty"`
	Host       SystemHost      `json:"host"`
	CPU        SystemCPU       `json:"cpu"`
	Memory     SystemMemory    `json:"memory"`
	Disks      []SystemDisk    `json:"disks"`
	Networks   []SystemNetwork `json:"networks"`
}

// SystemHost identifies the machine whose metrics are reported.
type SystemHost struct {
	Hostname string  `json:"hostname"`
	Platform string  `json:"platform"`
	NumCPU   int     `json:"num_cpu"`
	UptimeS  float64 `json:"uptime_s"`
}

// SystemCPU is the overall and per-core utilisation of the sample window.
type SystemCPU struct {
	Percent float64   `json:"percent"`
	PerCore []float64 `json:"per_core"`
	Load1   float64   `json:"load1"`
	Load5   float64   `json:"load5"`
	Load15  float64   `json:"load15"`
	// QuotaCores is the CPU quota expressed in cores (cgroup cpu.max /
	// cpu.cfs_quota_us). Zero means the container has no CPU limit.
	QuotaCores float64 `json:"quota_cores"`
}

// SystemMemory reports physical memory and swap in bytes.
type SystemMemory struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	Percent        float64 `json:"percent"`
	SwapTotalBytes uint64  `json:"swap_total_bytes"`
	SwapUsedBytes  uint64  `json:"swap_used_bytes"`
	// LimitSource is "cgroup" when the figures come from the container's
	// cgroup limit, or "host" when /proc/meminfo is reported as is. Container
	// limits are far smaller than the host's total on shared machines, so a
	// client should show which number it is looking at.
	LimitSource string `json:"limit_source"`
}

// SystemDisk is the space usage of one mounted filesystem.
type SystemDisk struct {
	Mount      string  `json:"mount"`
	FSType     string  `json:"fs_type"`
	TotalBytes uint64  `json:"total_bytes"`
	UsedBytes  uint64  `json:"used_bytes"`
	FreeBytes  uint64  `json:"free_bytes"`
	Percent    float64 `json:"percent"`
}

// SystemNetwork is the throughput of one interface over the sample window.
type SystemNetwork struct {
	Name          string  `json:"name"`
	RxBytesPerSec float64 `json:"rx_bytes_per_sec"`
	TxBytesPerSec float64 `json:"tx_bytes_per_sec"`
	RxTotalBytes  uint64  `json:"rx_total_bytes"`
	TxTotalBytes  uint64  `json:"tx_total_bytes"`
}

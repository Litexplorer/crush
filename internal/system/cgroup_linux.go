//go:build linux

// cgroup_linux.go
//
// 核心流程：容器里 /proc/meminfo 是宿主口径（125GB），限额要另外从 cgroup 读 ——
// 依次尝试 v2（memory.max / memory.current）与 v1（memory.limit_in_bytes / memory.usage_in_bytes），
// v1 既试挂载点根（容器内 cgroup 命名空间把自身 cgroup 映射到根），也试 /proc/self/cgroup 给出的子路径；
// CPU 同理读配额（cpu.max / cpu.cfs_quota_us），换算成可用核数。
package system

import (
	"os"
	"strconv"
	"strings"
)

// cgroupMemoryProbe 单个候选目录一次探测的结果。
type cgroupMemoryProbe struct {
	limit    uint64
	usage    uint64
	inactive uint64
	ok       bool
}

// cgroupLimits 采集到的容器限额；零值表示没有 cgroup 限制（裸机或未设限）。
type cgroupLimits struct {
	memoryLimit    uint64
	memoryUsage    uint64
	memoryInactive uint64
	memoryFound    bool
	cpuQuotaCores  float64
}

// unlimitedMemory 是 v1 未设限时 memory.limit_in_bytes 的哨兵值（约 2^63），远大于任何真实内存。
const unlimitedMemory = uint64(1) << 60

// readCgroupLimits 读取容器限额；读不到任何限额时返回零值。
func readCgroupLimits() cgroupLimits {
	result := cgroupLimits{}
	for _, dir := range cgroupMemoryDirs() {
		if probe, ok := probeCgroupMemory(dir); ok {
			result.memoryLimit = probe.limit
			result.memoryUsage = probe.usage
			result.memoryInactive = probe.inactive
			result.memoryFound = true
			break
		}
	}
	result.cpuQuotaCores = readCPUQuotaCores()
	return result
}

// cgroupMemoryDirs 按优先级列出候选目录：v2 自身 cgroup → v2 根 → v1 子路径 → v1 挂载根。
func cgroupMemoryDirs() []string {
	var dirs []string
	if path, ok := selfCgroupPath(""); ok {
		dirs = append(dirs, "/sys/fs/cgroup"+path)
	}
	dirs = append(dirs, "/sys/fs/cgroup")
	if path, ok := selfCgroupPath("memory"); ok && path != "" {
		dirs = append(dirs, "/sys/fs/cgroup/memory"+path)
	}
	dirs = append(dirs, "/sys/fs/cgroup/memory")
	return dirs
}

// probeCgroupMemory 在单个目录里依次尝试 v2 与 v1 的限额文件。
func probeCgroupMemory(dir string) (cgroupMemoryProbe, bool) {
	// v2：memory.max 为 "max" 表示不限制
	if raw, err := os.ReadFile(dir + "/memory.max"); err == nil {
		if limit, ok := parseCgroupLimit(strings.TrimSpace(string(raw))); ok {
			probe := cgroupMemoryProbe{limit: limit, ok: true}
			probe.usage = parseUint(strings.TrimSpace(readText(dir + "/memory.current")))
			probe.inactive = memoryStatValue(dir+"/memory.stat", "inactive_file")
			return probe, true
		}
	}

	// v1：memory.limit_in_bytes
	if raw, err := os.ReadFile(dir + "/memory.limit_in_bytes"); err == nil {
		if limit, ok := parseCgroupLimit(strings.TrimSpace(string(raw))); ok {
			probe := cgroupMemoryProbe{limit: limit, ok: true}
			probe.usage = parseUint(strings.TrimSpace(readText(dir + "/memory.usage_in_bytes")))
			probe.inactive = memoryStatValue(dir+"/memory.stat", "total_inactive_file")
			if probe.inactive == 0 {
				probe.inactive = memoryStatValue(dir+"/memory.stat", "inactive_file")
			}
			return probe, true
		}
	}
	return cgroupMemoryProbe{}, false
}

// parseCgroupLimit 解析限额文本："max" 或 0 视为不限制，v1 的 2^63 哨兵同样视为不限制。
func parseCgroupLimit(raw string) (uint64, bool) {
	if raw == "" || raw == "max" {
		return 0, false
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 || value >= unlimitedMemory {
		return 0, false
	}
	return value, true
}

// memoryStatValue 从 memory.stat 中取指定字段（v2 与 v1 字段名不同，调用方自行选）。
func memoryStatValue(path, key string) uint64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		name, value, ok := strings.Cut(line, " ")
		if !ok || name != key {
			continue
		}
		return parseUint(strings.TrimSpace(value))
	}
	return 0
}

// readCPUQuotaCores 换算 CPU 配额对应的核数；未设限返回 0。
func readCPUQuotaCores() float64 {
	// v2：cpu.max 形如 "2000000 100000" 或 "max 100000"
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		if cores := quotaCores(strings.Fields(string(raw))); cores > 0 {
			return cores
		}
	}

	// v1：cpu.cfs_quota_us / cpu.cfs_period_us，多个控制器挂载点都试一遍
	path, _ := selfCgroupPath("cpu,cpuacct")
	for _, dir := range []string{
		"/sys/fs/cgroup/cpu,cpuacct" + path,
		"/sys/fs/cgroup/cpu,cpuacct",
		"/sys/fs/cgroup/cpu" + path,
		"/sys/fs/cgroup/cpu",
	} {
		quota := strings.TrimSpace(readText(dir + "/cpu.cfs_quota_us"))
		period := strings.TrimSpace(readText(dir + "/cpu.cfs_period_us"))
		if cores := quotaCores([]string{quota, period}); cores > 0 {
			return cores
		}
	}
	return 0
}

// quotaCores 把 "quota period" 两个字段换算成核数；-1 或 max 表示未设限。
func quotaCores(fields []string) float64 {
	if len(fields) < 2 || fields[0] == "max" {
		return 0
	}
	quota, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || quota <= 0 {
		return 0
	}
	period, err := strconv.ParseFloat(fields[1], 64)
	if err != nil || period <= 0 {
		return 0
	}
	return quota / period
}

// selfCgroupPath 从 /proc/self/cgroup 取出指定控制器的路径；controller 为空表示 cgroup v2 的 "0::"。
func selfCgroupPath(controller string) (string, bool) {
	raw, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 {
			continue
		}
		if controller == "" {
			if fields[0] != "0" || fields[1] != "" {
				continue
			}
			return fields[2], true
		}
		for _, name := range strings.Split(fields[1], ",") {
			if name == controller {
				return fields[2], true
			}
		}
	}
	return "", false
}

// readText 读取小文件文本，失败返回空串（限额文件缺失是常态，不是错误）。
func readText(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

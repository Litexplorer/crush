//go:build linux

// proc_linux.go
//
// 核心流程：Linux 下从 /proc 读累积计数并做 statfs ——
// /proc/stat 取 CPU 总时间片与逐核时间片，/proc/net/dev 取网卡收发字节，
// /proc/meminfo 取内存，/proc/mounts + statfs 取磁盘容量，/proc/loadavg 与 /proc/uptime 取负载与开机时长。
package system

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// virtualFileSystems 统计磁盘时不纳入的伪文件系统类型。
var virtualFileSystems = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "cgroup": true,
	"cgroup2": true, "securityfs": true, "debugfs": true, "tracefs": true, "pstore": true,
	"bpf": true, "autofs": true, "mqueue": true, "hugetlbfs": true, "fusectl": true,
	"configfs": true, "ramfs": true, "squashfs": true, "binfmt_misc": true,
}

// readCounters 读取一次全部累积值；/proc 不可读（非 Linux 环境或沙箱）时返回 false。
func readCounters() (counters, bool) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return counters{}, false
	}

	result := counters{
		hostname: hostname(),
		platform: "linux",
		net:      map[string]netCounters{},
	}
	readCPUTimes(stat, &result)
	result.load1, result.load5, result.load15 = readLoadAvg()
	result.uptime = readUptime()
	result.memory = readMemory()
	result.disks = readDisks()
	result.net = readNetDev()

	// 容器限额：/proc/meminfo 是宿主口径，有 cgroup 限额时以限额为准
	limits := readCgroupLimits()
	if limits.memoryFound {
		result.memoryLimit = limits.memoryLimit
		result.memoryUsage = limits.memoryUsage
		result.memoryInactive = limits.memoryInactive
	}
	result.cpuQuotaCores = limits.cpuQuotaCores
	return result, true
}

// readCPUTimes 解析 /proc/stat：第一行 cpu 为总计，其后 cpu0/cpu1… 为逐核。
func readCPUTimes(stat []byte, result *counters) {
	scanner := bufio.NewScanner(strings.NewReader(string(stat)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// fields[0] 是 "cpu" 或 "cpuN"，其余是 user nice system idle iowait irq softirq steal…
		var total uint64
		for _, raw := range fields[1:] {
			value, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				total = 0
				break
			}
			total += value
		}
		if total == 0 {
			continue
		}
		idle := parseUint(fields[4])
		if len(fields) > 5 {
			idle += parseUint(fields[5]) // iowait 同样视为空闲
		}
		if fields[0] == "cpu" {
			result.cpuTotal, result.cpuIdle = total, idle
			continue
		}
		result.perCore = append(result.perCore, coreCounters{total: total, idle: idle})
	}
}

// readMemory 解析 /proc/meminfo 的 MemTotal / MemAvailable / SwapTotal / SwapFree（单位 kB）。
func readMemory() memoryCounters {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return memoryCounters{}
	}
	var result memoryCounters
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb := parseUint(fields[0]) * 1024
		switch key {
		case "MemTotal":
			result.total = kb
		case "MemAvailable":
			result.available = kb
		case "SwapTotal":
			result.swapTotal = kb
		case "SwapFree":
			result.swapFree = kb
		}
	}
	return result
}

// readLoadAvg 解析 /proc/loadavg 的前三个数（1/5/15 分钟平均负载）。
func readLoadAvg() (float64, float64, float64) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	return parseFloat(fields[0]), parseFloat(fields[1]), parseFloat(fields[2])
}

// readUptime 解析 /proc/uptime 的第一个数（秒）。
func readUptime() float64 {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	return parseFloat(fields[0])
}

// readDisks 解析 /proc/mounts，对真实设备挂载点做 statfs，按设备去重。
func readDisks() []diskCounters {
	raw, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var disks []diskCounters
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		device, mount, fsType := unescapeMount(fields[0]), unescapeMount(fields[1]), fields[2]
		if virtualFileSystems[fsType] || seen[device] {
			continue
		}
		if !strings.HasPrefix(device, "/dev/") && mount != "/" {
			continue
		}
		// 容器会把单文件挂到 / 下（如 /.dockerenv、/dev/termination-log），它们不是文件系统，跳过。
		if info, err := os.Stat(mount); err != nil || !info.IsDir() {
			continue
		}
		stat := syscall.Statfs_t{}
		if err := syscall.Statfs(mount, &stat); err != nil {
			continue
		}
		blockSize := uint64(stat.Bsize)
		total := stat.Blocks * blockSize
		if total == 0 {
			continue
		}
		seen[device] = true
		free := stat.Bavail * blockSize
		disks = append(disks, diskCounters{
			mount:  mount,
			fsType: fsType,
			total:  total,
			used:   total - stat.Bfree*blockSize,
			free:   free,
		})
	}
	return disks
}

// readNetDev 解析 /proc/net/dev，跳过回环口（lo 的流量不反映对外网络状况）。
func readNetDev() map[string]netCounters {
	raw, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return map[string]netCounters{}
	}
	result := map[string]netCounters{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := scanner.Text()
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" || name == "lo" || name == "Inter-|" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		result[name] = netCounters{
			name: name,
			rx:   parseUint(fields[0]),
			tx:   parseUint(fields[8]),
		}
	}
	return result
}

// baseHostInfo 返回主机名、平台、核数与开机时长，供不支持采集的平台复用。
func baseHostInfo() (string, string, int, float64) {
	return hostname(), runtime.GOOS, runtime.NumCPU(), readUptime()
}

// hostname 取主机名，失败时退回 unknown，避免整个快照因为一个可选字段失败。
func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "unknown"
	}
	return name
}

// unescapeMount 还原 /proc/mounts 里 \040（空格）等八进制转义。
func unescapeMount(value string) string {
	if !strings.Contains(value, "\\") {
		return value
	}
	replacer := strings.NewReplacer("\\040", " ", "\\011", "\t", "\\012", "\n", "\\134", "\\")
	return replacer.Replace(value)
}

// parseUint 解析失败按 0 处理：单个字段异常不该让整块指标不可用。
func parseUint(value string) uint64 {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

// parseFloat 解析失败按 0 处理。
func parseFloat(value string) float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	return parsed
}

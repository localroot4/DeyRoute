package sysinfo

import (
	"bytes"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// CPUSampler turns the aggregate counters of /proc/stat into a busy
// percentage between two samples. It is safe for concurrent use.
type CPUSampler struct {
	root        string
	mu          sync.Mutex
	total, idle uint64
}

// NewCPUSampler returns a sampler of the /proc/stat below root.
func NewCPUSampler(root string) *CPUSampler { return &CPUSampler{root: root} }

// Sample returns the whole-system CPU usage in percent (one decimal) since
// the previous sample. It is 0 on the first call, when /proc/stat is
// unreadable and when a counter went backwards (a wrap or a reset), which
// starts a new baseline.
func (c *CPUSampler) Sample() float64 {
	total, idle, ok := readCPU(c.root)
	if !ok {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prevTotal, prevIdle := c.total, c.idle
	c.total, c.idle = total, idle
	if prevTotal == 0 || total <= prevTotal || idle < prevIdle {
		return 0
	}
	dt, di := total-prevTotal, idle-prevIdle
	if di > dt {
		return 0
	}
	pct := float64(dt-di) * 100 / float64(dt)
	return math.Round(pct*10) / 10
}

// readCPU parses the aggregate "cpu" line of /proc/stat: total jiffies and
// idle+iowait jiffies.
func readCPU(root string) (total, idle uint64, ok bool) {
	data, err := os.ReadFile(filepath.Join(root, "/proc/stat")) // #nosec G304 -- procfs below root
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := bytes.Cut(data, []byte("\n"))
	f := strings.Fields(string(line))
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, s := range f[1:] {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		if i >= 8 { // guest and guest_nice are already part of user/nice
			break
		}
		total += n
		if i == 3 || i == 4 { // idle, iowait
			idle += n
		}
	}
	return total, idle, true
}

// CPUs returns the number of CPUs this process can use: runtime.NumCPU
// (which honours the affinity mask) capped by the CPU quota of its cgroup
// (v2 cpu.max, v1 cpu.cfs_quota_us/cpu.cfs_period_us), rounded up; at
// least 1.
func CPUs(root string) int { return cpusCapped(root, runtime.NumCPU()) }

// cpusCapped caps n by the cgroup CPU quota below root.
func cpusCapped(root string, n int) int {
	if q, ok := cgroupQuota(root); ok {
		if c := int(math.Ceil(q)); c >= 1 && c < n {
			n = c
		}
	}
	return max(n, 1)
}

// cgroupV1Mounts are where cgroup v1 mounts the cpu controller.
var cgroupV1Mounts = []string{"cpu,cpuacct", "cpu", "cpuacct,cpu"}

// cgroupQuota returns the smallest CPU quota (in CPUs) of this process's
// cgroup and its ancestors; ok is false when none is set. Without a
// readable /proc/self/cgroup only the top cgroup directory is read (in a
// container that is the container's own cgroup).
func cgroupQuota(root string) (cpus float64, ok bool) {
	v2, v1 := "/", "/"
	data, err := os.ReadFile(filepath.Join(root, "/proc/self/cgroup")) // #nosec G304 -- procfs below root
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) != 3 || !strings.HasPrefix(parts[2], "/") {
				continue
			}
			p := path.Clean(parts[2])
			switch {
			case parts[0] == "0" && parts[1] == "":
				v2 = p
			case hasController(parts[1], "cpu"):
				v1 = p
			}
		}
	}
	best := math.Inf(1)
	walk := func(base, p string, read func(dir string) (float64, bool)) {
		for {
			if q, found := read(filepath.Join(root, base, p)); found && q < best {
				best = q
			}
			if p == "/" {
				return
			}
			p = path.Dir(p)
		}
	}
	walk("/sys/fs/cgroup", v2, readCPUMax)
	for _, m := range cgroupV1Mounts {
		walk(filepath.Join("/sys/fs/cgroup", m), v1, readCFSQuota)
	}
	if math.IsInf(best, 1) {
		return 0, false
	}
	return best, true
}

// hasController reports whether the comma-separated list has c.
func hasController(list, c string) bool {
	for _, s := range strings.Split(list, ",") {
		if s == c {
			return true
		}
	}
	return false
}

// readCPUMax parses cgroup v2 cpu.max ("max 100000" or "50000 100000").
func readCPUMax(dir string) (float64, bool) {
	f := strings.Fields(readTrim(filepath.Join(dir, "cpu.max")))
	if len(f) < 1 || f[0] == "max" {
		return 0, false
	}
	period := 100000.0
	if len(f) >= 2 {
		p, err := strconv.ParseFloat(f[1], 64)
		if err != nil || p <= 0 {
			return 0, false
		}
		period = p
	}
	return quota(f[0], period)
}

// readCFSQuota parses cgroup v1 cpu.cfs_quota_us (-1 = no limit) and
// cpu.cfs_period_us.
func readCFSQuota(dir string) (float64, bool) {
	q := readTrim(filepath.Join(dir, "cpu.cfs_quota_us"))
	p, err := strconv.ParseFloat(readTrim(filepath.Join(dir, "cpu.cfs_period_us")), 64)
	if q == "" || err != nil || p <= 0 {
		return 0, false
	}
	return quota(q, p)
}

// quota turns a quota in microseconds per period into CPUs.
func quota(s string, period float64) (float64, bool) {
	q, err := strconv.ParseFloat(s, 64)
	if err != nil || q <= 0 || math.IsInf(q, 0) || math.IsNaN(q) {
		return 0, false
	}
	return q / period, true
}

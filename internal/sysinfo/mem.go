package sysinfo

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// MemInfo is the RAM of the host in bytes (0 = unknown).
type MemInfo struct {
	Total     uint64
	Available uint64
}

// Used is the RAM in use: Total minus Available (0 when unknown).
func (m MemInfo) Used() uint64 {
	if m.Available >= m.Total {
		return 0
	}
	return m.Total - m.Available
}

// Mem reads MemTotal and MemAvailable from /proc/meminfo. A kernel without
// MemAvailable (before 3.14) gets MemFree+Buffers+Cached instead.
func Mem(root string) MemInfo {
	kb := readKB(filepath.Join(root, "/proc/meminfo"),
		"MemTotal:", "MemAvailable:", "MemFree:", "Buffers:", "Cached:")
	m := MemInfo{Total: kb["MemTotal:"] * 1024}
	avail, ok := kb["MemAvailable:"]
	if !ok {
		avail = kb["MemFree:"] + kb["Buffers:"] + kb["Cached:"]
	}
	m.Available = min(avail*1024, m.Total)
	return m
}

// SelfRSS returns the resident memory (VmRSS) of this process in bytes
// (0 when unknown).
func SelfRSS(root string) uint64 {
	return readKB(filepath.Join(root, "/proc/self/status"), "VmRSS:")["VmRSS:"] * 1024
}

// readKB reads the "Key:  123 kB" lines of a /proc status-like file for
// the given keys; a key that is missing or malformed is absent.
func readKB(path string, keys ...string) map[string]uint64 {
	res := map[string]uint64{}
	f, err := os.Open(path) // #nosec G304 -- procfs below root
	if err != nil {
		return res
	}
	defer func() { _ = f.Close() }()
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !want[fields[0]] {
			continue
		}
		if _, dup := res[fields[0]]; dup {
			continue
		}
		if n, err := strconv.ParseUint(fields[1], 10, 64); err == nil && n < 1<<53 {
			res[fields[0]] = n
		}
	}
	return res
}

// KernelRelease returns the running kernel release
// (/proc/sys/kernel/osrelease), "" when unknown.
func KernelRelease(root string) string {
	return readTrim(filepath.Join(root, "/proc/sys/kernel/osrelease"))
}

// OSPrettyName returns PRETTY_NAME from /etc/os-release (or
// /usr/lib/os-release), runtime.GOOS when unknown.
func OSPrettyName(root string) string {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		data, err := os.ReadFile(filepath.Join(root, p)) // #nosec G304 -- fixed system file below root
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				if u, err := strconv.Unquote(v); err == nil {
					v = u
				}
				v = strings.Trim(v, `"'`)
				if v != "" {
					return v
				}
			}
		}
	}
	return runtime.GOOS
}

// readTrim returns the trimmed content of a small file ("" on error).
func readTrim(path string) string {
	data, err := os.ReadFile(path) // #nosec G304 -- system information files below root
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readInt returns the integer in a small file; ok is false when it is
// missing or not a number.
func readInt(path string) (int, bool) {
	n, err := strconv.Atoi(readTrim(path))
	return n, err == nil
}

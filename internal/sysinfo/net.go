package sysinfo

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Route flags of /proc/net/route and /proc/net/ipv6_route.
const (
	rtfUp     = 0x0001
	rtfReject = 0x0200
)

// DefaultRoute returns the interface of the IPv4 default route with the
// lowest metric (/proc/net/route), falling back to the IPv6 default route
// (/proc/net/ipv6_route); "" when there is none.
func DefaultRoute(root string) string {
	if dev := defaultRoute4(root); dev != "" {
		return dev
	}
	return defaultRoute6(root)
}

// defaultRoute4 parses /proc/net/route: Iface Destination Gateway Flags
// RefCnt Use Metric Mask ..., addresses in hex.
func defaultRoute4(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "/proc/net/route")) // #nosec G304 -- procfs below root
	if err != nil {
		return ""
	}
	best, bestMetric := "", uint64(math.MaxUint64)
	for i, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" || !ValidIface(f[0]) || f[0] == "lo" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&rtfUp == 0 || flags&rtfReject != 0 {
			continue
		}
		metric, err := strconv.ParseUint(f[6], 10, 64)
		if err != nil {
			continue
		}
		if metric < bestMetric {
			best, bestMetric = f[0], metric
		}
	}
	return best
}

// defaultRoute6 parses /proc/net/ipv6_route: dest dest_len src src_len
// next_hop metric refcnt use flags iface, numbers in hex. The kernel's
// unreachable default routes on lo are skipped.
func defaultRoute6(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "/proc/net/ipv6_route")) // #nosec G304 -- procfs below root
	if err != nil {
		return ""
	}
	zero := strings.Repeat("0", 32)
	best, bestMetric := "", uint64(math.MaxUint64)
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[0] != zero || f[1] != "00" || !ValidIface(f[9]) || f[9] == "lo" {
			continue
		}
		flags, err := strconv.ParseUint(f[8], 16, 32)
		if err != nil || flags&rtfUp == 0 || flags&rtfReject != 0 {
			continue
		}
		metric, err := strconv.ParseUint(f[5], 16, 32)
		if err != nil {
			continue
		}
		if metric < bestMetric {
			best, bestMetric = f[9], metric
		}
	}
	return best
}

// NICInfo describes a network interface (/sys/class/net/<name>).
type NICInfo struct {
	Name string
	MTU  int // 0 = unknown
	// SpeedMbps is the link speed; -1 = unknown (virtual NICs report none).
	SpeedMbps int
	// RxBytes and TxBytes are the interface byte counters since boot
	// (statistics/rx_bytes and tx_bytes).
	RxBytes uint64
	TxBytes uint64
}

// NIC reads the MTU, speed and byte counters of interface name; ok is false
// when the name is invalid or the interface does not exist.
func NIC(root, name string) (NICInfo, bool) {
	if !ValidIface(name) {
		return NICInfo{}, false
	}
	dir := filepath.Join(root, "/sys/class/net", name)
	if _, err := os.Stat(dir); err != nil {
		return NICInfo{}, false
	}
	n := NICInfo{Name: name, SpeedMbps: -1}
	if mtu, ok := readInt(filepath.Join(dir, "mtu")); ok && mtu > 0 {
		n.MTU = mtu
	}
	// Reading speed fails (EINVAL) on a link without one; some drivers
	// report -1 or 2^32-1.
	if s, ok := readInt(filepath.Join(dir, "speed")); ok && s > 0 && s < 10_000_000 {
		n.SpeedMbps = s
	}
	n.RxBytes = readUint(filepath.Join(dir, "statistics", "rx_bytes"))
	n.TxBytes = readUint(filepath.Join(dir, "statistics", "tx_bytes"))
	return n, true
}

// ValidIface reports whether name is a valid Linux interface name: 1-15
// bytes, no '/', ':' or whitespace, not "." or "..".
func ValidIface(name string) bool {
	if name == "" || len(name) > 15 || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r == '/' || r == ':' || r <= ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

// readUint returns the unsigned integer in a small file (0 when unknown).
func readUint(path string) uint64 {
	n, err := strconv.ParseUint(readTrim(path), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

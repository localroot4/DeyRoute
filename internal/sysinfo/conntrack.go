package sysinfo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// ConntrackInfo describes the connection tracking table. Without the
// nf_conntrack module everything is zero and Loaded is false.
type ConntrackInfo struct {
	Loaded bool
	Max    int // nf_conntrack_max
	Count  int // nf_conntrack_count (entries in use)
	// Buckets is the hash table size (nf_conntrack_buckets, or the
	// module's hashsize parameter).
	Buckets int
	// EstablishedTimeout is nf_conntrack_tcp_timeout_established in
	// seconds.
	EstablishedTimeout int
}

// Conntrack reads the conntrack facts from /proc/sys/net/netfilter. The
// table is loaded when nf_conntrack_max exists.
func Conntrack(root string) ConntrackInfo {
	dir := filepath.Join(root, "/proc/sys/net/netfilter")
	if !exists(root, "/proc/sys/net/netfilter/nf_conntrack_max") {
		return ConntrackInfo{}
	}
	c := ConntrackInfo{Loaded: true}
	c.Max, _ = readInt(filepath.Join(dir, "nf_conntrack_max"))
	c.Count, _ = readInt(filepath.Join(dir, "nf_conntrack_count"))
	if b, ok := readInt(filepath.Join(dir, "nf_conntrack_buckets")); ok {
		c.Buckets = b
	} else {
		c.Buckets, _ = readInt(filepath.Join(root, "/sys/module/nf_conntrack/parameters/hashsize"))
	}
	c.EstablishedTimeout, _ = readInt(filepath.Join(dir, "nf_conntrack_tcp_timeout_established"))
	return c
}

// BBRAvailable reports whether the kernel can use the BBR congestion
// control: it is listed in net.ipv4.tcp_available_congestion_control, the
// tcp_bbr module is loaded, or tcp_bbr is a module of the running kernel
// (modules.dep or modules.builtin) that the kernel loads on demand.
func BBRAvailable(root string) bool {
	for _, cc := range strings.Fields(readTrim(filepath.Join(root, "/proc/sys/net/ipv4/tcp_available_congestion_control"))) {
		if cc == "bbr" {
			return true
		}
	}
	return exists(root, "/sys/module/tcp_bbr") || kernelModule(root, "/tcp_bbr.ko")
}

// FQAvailable reports whether the fq queueing discipline can be used: it is
// the default qdisc already, the sch_fq module is loaded, or it is a module
// of the running kernel (modules.dep or modules.builtin).
func FQAvailable(root string) bool {
	if readTrim(filepath.Join(root, "/proc/sys/net/core/default_qdisc")) == "fq" {
		return true
	}
	return exists(root, "/sys/module/sch_fq") || kernelModule(root, "/sch_fq.ko")
}

// kernelModule reports whether modules.dep or modules.builtin of the
// running kernel lists a module whose path contains suffix (also matching
// compressed modules such as tcp_bbr.ko.zst).
func kernelModule(root, suffix string) bool {
	rel := KernelRelease(root)
	if rel == "" || strings.ContainsAny(rel, "/ ") || rel == "." || rel == ".." {
		return false
	}
	for _, name := range []string{"modules.dep", "modules.builtin"} {
		data, err := os.ReadFile(filepath.Join(root, "lib", "modules", rel, name)) // #nosec G304 -- fixed file below root
		if err == nil && bytes.Contains(data, []byte(suffix)) {
			return true
		}
	}
	return false
}

// Package sysinfo reads the host facts the hub and the node share: CPU
// usage and count, memory, kernel release, virtualization, BBR and fq
// availability, the root qdisc and the MTU/speed of the default-route
// interface, and the conntrack table.
//
// It only reads files below a root ("" or "/" = the live system, a temp
// directory in tests) and, for the root qdisc, asks the kernel through
// rtnetlink. It never starts a process (section 15) and never changes
// anything. Every reader is best effort: a missing or malformed file gives
// the zero value ("unknown"), never an error.
package sysinfo

import "path/filepath"

// Facts are the measured facts a tuning plan is computed from. The fields
// match api.TuneFacts one to one, so api.TuneFacts(f) converts them.
type Facts struct {
	MemBytes          uint64 `json:"mem_bytes"`
	MemAvailableBytes uint64 `json:"mem_available_bytes,omitempty"`
	CPUs              int    `json:"cpus"`
	Kernel            string `json:"kernel,omitempty"`
	// Virt is the container type (Virt*); "" on a VM or bare metal.
	Virt            string `json:"virt,omitempty"`
	BBRAvailable    bool   `json:"bbr_available"`
	FQAvailable     bool   `json:"fq_available"`
	Qdisc           string `json:"qdisc,omitempty"` // root qdisc of the default-route interface
	ConntrackLoaded bool   `json:"conntrack_loaded"`
	ConntrackMax    int    `json:"conntrack_max,omitempty"`
	ConntrackCount  int    `json:"conntrack_count,omitempty"`
	NIC             string `json:"nic,omitempty"` // default-route interface
	NICMTU          int    `json:"nic_mtu,omitempty"`
	NICSpeedMbps    int    `json:"nic_speed_mbps,omitempty"` // 0 = unknown
}

// Collect gathers the facts of the host below root. The root qdisc is a
// kernel query, not a file: it is read only for the live system (root ""
// or "/"), so a fake tree in tests gives the same facts on every machine.
func Collect(root string) Facts {
	mem := Mem(root)
	ct := Conntrack(root)
	f := Facts{
		MemBytes:          mem.Total,
		MemAvailableBytes: mem.Available,
		CPUs:              CPUs(root),
		Kernel:            KernelRelease(root),
		Virt:              Virt(root),
		BBRAvailable:      BBRAvailable(root),
		FQAvailable:       FQAvailable(root),
		ConntrackLoaded:   ct.Loaded,
		ConntrackMax:      ct.Max,
		ConntrackCount:    ct.Count,
		NIC:               DefaultRoute(root),
	}
	if f.NIC == "" {
		return f
	}
	if nic, ok := NIC(root, f.NIC); ok {
		f.NICMTU = nic.MTU
		f.NICSpeedMbps = max(nic.SpeedMbps, 0)
	}
	if LiveRoot(root) {
		f.Qdisc = RootQdisc(f.NIC)
		if f.Qdisc == "fq" {
			f.FQAvailable = true
		}
	}
	return f
}

// LiveRoot reports whether root is the live system ("" or "/").
func LiveRoot(root string) bool { return root == "" || filepath.Clean(root) == "/" }

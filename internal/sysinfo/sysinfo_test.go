package sysinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
)

// The fields of Facts and api.TuneFacts must stay identical: this is a
// compile-time check of the conversion.
var _ = api.TuneFacts(Facts{})

func write(t *testing.T, root, p, content string) {
	t.Helper()
	full := filepath.Join(root, p)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func TestCollectVM(t *testing.T) {
	root := filepath.Join("testdata", "vm")
	f := Collect(root)
	require.Equal(t, Facts{
		MemBytes:          2014256 * 1024,
		MemAvailableBytes: 1532100 * 1024,
		CPUs:              min(runtime.NumCPU(), 2), // cpu.max 150000/100000 rounds up to 2
		Kernel:            "6.1.0-21-amd64",
		Virt:              VirtNone,
		BBRAvailable:      true, // tcp_bbr.ko.xz in modules.dep
		FQAvailable:       true, // sch_fq.ko.xz (sch_fq_codel alone would not do)
		Qdisc:             "",   // a fake tree never asks the kernel
		ConntrackLoaded:   true,
		ConntrackMax:      262144,
		ConntrackCount:    1234,
		NIC:               "eth0", // metric 100 beats wg0's 200
		NICMTU:            1500,
		NICSpeedMbps:      1000,
	}, f)

	ct := Conntrack(root)
	require.Equal(t, ConntrackInfo{Loaded: true, Max: 262144, Count: 1234, Buckets: 65536, EstablishedTimeout: 432000}, ct)
	nic, ok := NIC(root, "eth0")
	require.True(t, ok)
	require.Equal(t, NICInfo{Name: "eth0", MTU: 1500, SpeedMbps: 1000, RxBytes: 123456789, TxBytes: 987654321}, nic)
	require.Equal(t, uint64(23456*1024), SelfRSS(root))
	require.Equal(t, uint64((2014256-1532100)*1024), Mem(root).Used())
}

func TestCollectOpenVZ(t *testing.T) {
	root := filepath.Join("testdata", "openvz")
	f := Collect(root)
	require.Equal(t, VirtOpenVZ, f.Virt, "/proc/vz without /proc/bc")
	require.Equal(t, uint64(1048576*1024), f.MemBytes)
	require.Equal(t, uint64((524288+262144)*1024), f.MemAvailableBytes, "no MemAvailable: MemFree+Buffers+Cached")
	require.False(t, f.ConntrackLoaded)
	require.Zero(t, f.ConntrackMax)
	require.False(t, f.BBRAvailable)
	require.False(t, f.FQAvailable)
	require.Equal(t, "venet0", f.NIC)
	require.Equal(t, 1500, f.NICMTU)
	require.Zero(t, f.NICSpeedMbps, "speed -1 is unknown")
	nic, ok := NIC(root, "venet0")
	require.True(t, ok)
	require.Equal(t, -1, nic.SpeedMbps)
	require.Equal(t, runtime.NumCPU(), f.CPUs, "no cgroup quota")
}

func TestCollectEmptyRoot(t *testing.T) {
	f := Collect(t.TempDir())
	require.Equal(t, Facts{CPUs: runtime.NumCPU()}, f)
	require.Equal(t, MemInfo{}, Mem(t.TempDir()))
	require.Zero(t, MemInfo{}.Used())
	require.Zero(t, MemInfo{Total: 10, Available: 20}.Used())
}

func TestCPUSampler(t *testing.T) {
	root := t.TempDir()
	c := NewCPUSampler(root)
	require.Zero(t, c.Sample(), "no /proc/stat")
	write(t, root, "/proc/stat", "cpu  100 0 100 800 0 0 0 0 0 0\n")
	require.Zero(t, c.Sample(), "first sample")
	write(t, root, "/proc/stat", "cpu  150 0 150 900 0 0 0 0 0 0\n")
	require.InDelta(t, 50.0, c.Sample(), 0.01)
	write(t, root, "/proc/stat", "cpu  150 0 150 900 0 0 0 0 0 0\n")
	require.Zero(t, c.Sample(), "no progress")
	// iowait counts as idle; guest columns are not counted twice.
	write(t, root, "/proc/stat", "cpu  175 0 150 950 25 0 0 0 999 999\n")
	require.InDelta(t, 25.0, c.Sample(), 0.01)
	// A counter that went backwards (wrap or reset) gives 0 and a new
	// baseline; the next sample is measured from it.
	write(t, root, "/proc/stat", "cpu  10 0 10 80 0 0 0 0 0 0\n")
	require.Zero(t, c.Sample(), "counter wrap")
	write(t, root, "/proc/stat", "cpu  40 0 10 150 0 0 0 0 0 0\n")
	require.InDelta(t, 30.0, c.Sample(), 0.01)
	// Idle going backwards alone is also a reset.
	write(t, root, "/proc/stat", "cpu  80 0 10 140 0 0 0 0 0 0\n")
	require.Zero(t, c.Sample())
	write(t, root, "/proc/stat", "intr 1\n")
	require.Zero(t, c.Sample())
	write(t, root, "/proc/stat", "cpu 1 x 3 4\n")
	require.Zero(t, c.Sample())
	write(t, root, "/proc/stat", "cpu 1 2 3\n")
	require.Zero(t, c.Sample())
}

func TestCPUSamplerConcurrent(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/proc/stat", "cpu  100 0 100 800 0 0 0 0 0 0\n")
	c := NewCPUSampler(root)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				v := c.Sample()
				require.GreaterOrEqual(t, v, 0.0)
				require.LessOrEqual(t, v, 100.0)
			}
		}()
	}
	wg.Wait()
}

func TestCPUs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		n     int
		want  int
	}{
		{"no cgroup files", nil, 4, 4},
		{"v2 unlimited", map[string]string{"/sys/fs/cgroup/cpu.max": "max 100000\n"}, 4, 4},
		{"v2 half a CPU", map[string]string{"/sys/fs/cgroup/cpu.max": "50000 100000\n"}, 4, 1},
		{"v2 2.5 CPUs", map[string]string{"/sys/fs/cgroup/cpu.max": "250000 100000\n"}, 4, 3},
		{"v2 quota above NumCPU", map[string]string{"/sys/fs/cgroup/cpu.max": "800000 100000\n"}, 4, 4},
		{"v2 quota without period", map[string]string{"/sys/fs/cgroup/cpu.max": "200000\n"}, 4, 2},
		{"v2 malformed", map[string]string{"/sys/fs/cgroup/cpu.max": "x 100000\n"}, 4, 4},
		{"v2 zero period", map[string]string{"/sys/fs/cgroup/cpu.max": "100000 0\n"}, 4, 4},
		{"v2 own cgroup and parent: the smaller wins", map[string]string{
			"/proc/self/cgroup":                                        "0::/system.slice/deyroute-hub.service\n",
			"/sys/fs/cgroup/system.slice/cpu.max":                      "100000 100000\n",
			"/sys/fs/cgroup/system.slice/deyroute-hub.service/cpu.max": "300000 100000\n",
		}, 8, 1},
		{"v2 path outside the namespace: the top cgroup", map[string]string{
			"/proc/self/cgroup":      "0::/docker/abc\n",
			"/sys/fs/cgroup/cpu.max": "200000 100000\n",
		}, 8, 2},
		{"v1 quota", map[string]string{
			"/proc/self/cgroup": "12:memory:/x\n4:cpu,cpuacct:/user.slice\n",
			"/sys/fs/cgroup/cpu,cpuacct/user.slice/cpu.cfs_quota_us":  "150000\n",
			"/sys/fs/cgroup/cpu,cpuacct/user.slice/cpu.cfs_period_us": "100000\n",
		}, 8, 2},
		{"v1 unlimited", map[string]string{
			"/sys/fs/cgroup/cpu/cpu.cfs_quota_us":  "-1\n",
			"/sys/fs/cgroup/cpu/cpu.cfs_period_us": "100000\n",
		}, 8, 8},
		{"at least 1", nil, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for p, c := range tc.files {
				write(t, root, p, c)
			}
			require.Equal(t, tc.want, cpusCapped(root, tc.n))
		})
	}
	require.Equal(t, 2, cpusCapped(filepath.Join("testdata", "vm"), 16))
	require.GreaterOrEqual(t, CPUs(""), 1)
}

func TestMemAndKernel(t *testing.T) {
	root := t.TempDir()
	write(t, root, "/proc/meminfo", "MemTotal:  x kB\nMemAvailable: 10 kB\n")
	require.Equal(t, MemInfo{}, Mem(root), "malformed total: available is capped at it")
	write(t, root, "/proc/meminfo", "MemTotal: 100 kB\nMemAvailable: 400 kB\n")
	require.Equal(t, MemInfo{Total: 102400, Available: 102400}, Mem(root))
	require.Empty(t, KernelRelease(root))
	write(t, root, "/proc/sys/kernel/osrelease", " 5.15.0-1-generic \n")
	require.Equal(t, "5.15.0-1-generic", KernelRelease(root))
}

func TestOSAndSelfRSS(t *testing.T) {
	root := t.TempDir()
	require.Equal(t, runtime.GOOS, OSPrettyName(root))
	write(t, root, "/usr/lib/os-release", "PRETTY_NAME='Arch Linux'\n")
	require.Equal(t, "Arch Linux", OSPrettyName(root))
	write(t, root, "/etc/os-release", "NAME=Debian\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")
	require.Equal(t, "Debian GNU/Linux 12 (bookworm)", OSPrettyName(root))
	require.Zero(t, SelfRSS(root))
	write(t, root, "/proc/self/status", "VmRSS:\n")
	require.Zero(t, SelfRSS(root))
	write(t, root, "/proc/self/status", "VmRSS: x kB\n")
	require.Zero(t, SelfRSS(root))
	write(t, root, "/proc/self/status", "Name:\tx\nVmRSS:\t  12 kB\n")
	require.Equal(t, uint64(12*1024), SelfRSS(root))
}

func TestVirt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"bare metal or VM", nil, VirtNone},
		{"openvz container", map[string]string{"/proc/vz/veinfo": ""}, VirtOpenVZ},
		{"openvz host", map[string]string{"/proc/vz/veinfo": "", "/proc/bc/0/resources": ""}, VirtNone},
		{"systemd lxc", map[string]string{"/run/systemd/container": "lxc\n"}, VirtLXC},
		{"systemd lxc-libvirt", map[string]string{"/run/systemd/container": "lxc-libvirt\n"}, VirtLXC},
		{"systemd docker", map[string]string{"/run/systemd/container": "docker\n"}, VirtDocker},
		{"systemd nspawn", map[string]string{"/run/systemd/container": "systemd-nspawn\n"}, VirtContainer},
		{"environ lxc", map[string]string{"/proc/1/environ": "PATH=/bin\x00container=lxc\x00TERM=linux\x00"}, VirtLXC},
		{"environ podman", map[string]string{"/proc/1/environ": "container=podman\x00"}, VirtContainer},
		{"environ empty value", map[string]string{"/proc/1/environ": "container=\x00"}, VirtNone},
		{"dockerenv", map[string]string{"/.dockerenv": ""}, VirtDocker},
		{"containerenv", map[string]string{"/run/.containerenv": ""}, VirtContainer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for p, c := range tc.files {
				write(t, root, p, c)
			}
			require.Equal(t, tc.want, Virt(root))
		})
	}
}

func TestConntrack(t *testing.T) {
	root := t.TempDir()
	require.Equal(t, ConntrackInfo{}, Conntrack(root), "missing conntrack: not loaded")
	write(t, root, "/proc/sys/net/netfilter/nf_conntrack_max", "65536\n")
	write(t, root, "/sys/module/nf_conntrack/parameters/hashsize", "16384\n")
	require.Equal(t, ConntrackInfo{Loaded: true, Max: 65536, Buckets: 16384}, Conntrack(root))
	write(t, root, "/proc/sys/net/netfilter/nf_conntrack_max", "garbage\n")
	require.Equal(t, ConntrackInfo{Loaded: true, Buckets: 16384}, Conntrack(root))
}

func TestBBRAndFQ(t *testing.T) {
	root := t.TempDir()
	require.False(t, BBRAvailable(root))
	require.False(t, FQAvailable(root))

	write(t, root, "/proc/sys/kernel/osrelease", "6.8.0\n")
	write(t, root, "/lib/modules/6.8.0/modules.builtin", "kernel/net/sched/sch_fq_codel.ko\nkernel/net/sched/sch_fq_pie.ko\n")
	require.False(t, FQAvailable(root), "fq_codel and fq_pie are not fq")
	write(t, root, "/lib/modules/6.8.0/modules.builtin", "kernel/net/sched/sch_fq.ko\n")
	require.True(t, FQAvailable(root))

	write(t, root, "/proc/sys/net/ipv4/tcp_available_congestion_control", "reno cubic bbr\n")
	require.True(t, BBRAvailable(root))

	other := t.TempDir()
	write(t, other, "/sys/module/tcp_bbr/refcnt", "0\n")
	write(t, other, "/sys/module/sch_fq/refcnt", "0\n")
	require.True(t, BBRAvailable(other))
	require.True(t, FQAvailable(other))

	live := t.TempDir()
	write(t, live, "/proc/sys/net/core/default_qdisc", "fq\n")
	require.True(t, FQAvailable(live))

	// A release that could escape lib/modules is ignored.
	bad := t.TempDir()
	write(t, bad, "/proc/sys/kernel/osrelease", "..\n")
	write(t, bad, "/lib/modules.dep", "kernel/net/ipv4/tcp_bbr.ko\n")
	require.False(t, BBRAvailable(bad))
}

func TestDefaultRoute(t *testing.T) {
	root := t.TempDir()
	require.Empty(t, DefaultRoute(root))
	header := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"
	write(t, root, "/proc/net/route", header+
		"ens3\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n"+
		"ens4\t00000000\t0102A8C0\t0002\t0\t0\t10\t00000000\t0\t0\t0\n"+ // not up
		"ens5\t00000000\t0103A8C0\t0203\t0\t0\t20\t00000000\t0\t0\t0\n"+ // reject
		"ens6\t00000000\t0104A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"+
		"ens7\t00000000\t0105A8C0\t0003\t0\t0\t50\t000000FF\t0\t0\t0\n"+ // not a default mask
		"ens8\t00000000\t0105A8C0\t0003\t0\t0\tx\t00000000\t0\t0\t0\n"+ // bad metric
		"ens9\t00000000\t0105A8C0\tzz\t0\t0\t1\t00000000\t0\t0\t0\n"+ // bad flags
		"lo\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n"+
		"bad/name\t00000000\t00000000\t0001\t0\t0\t0\t00000000\t0\t0\t0\n"+
		"short\n")
	require.Equal(t, "ens6", DefaultRoute(root))

	// No IPv4 default: the IPv6 table.
	write(t, root, "/proc/net/route", header+"ens3\t0002A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n")
	zero := "00000000000000000000000000000000"
	write(t, root, "/proc/net/ipv6_route",
		zero+" 00 "+zero+" 00 "+zero+" ffffffff 00000001 00000000 00200200       lo\n"+
			zero+" 00 "+zero+" 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003     ens3\n"+
			zero+" 00 "+zero+" 00 fe800000000000000000000000000001 00000100 00000001 00000000 00000003     ens9\n"+
			"20010db8000000000000000000000000 40 "+zero+" 00 "+zero+" 00000100 00000001 00000000 00000001     ens3\n"+
			zero+" 00 "+zero+" 00 "+zero+" zz 00000001 00000000 00000003     ens4\n")
	require.Equal(t, "ens9", DefaultRoute(root), "metric 0x100 beats 0x400")
}

func TestNICAndIfaceNames(t *testing.T) {
	root := t.TempDir()
	_, ok := NIC(root, "eth0")
	require.False(t, ok, "missing interface")
	for _, bad := range []string{"", ".", "..", "a/b", "a:b", "a b", "0123456789abcdef"} {
		require.False(t, ValidIface(bad), bad)
		_, ok := NIC(root, bad)
		require.False(t, ok, bad)
	}
	require.True(t, ValidIface("enp0s31f6"))
	write(t, root, "/sys/class/net/eth0/speed", "4294967295\n")
	write(t, root, "/sys/class/net/eth0/mtu", "x\n")
	nic, ok := NIC(root, "eth0")
	require.True(t, ok)
	require.Equal(t, NICInfo{Name: "eth0", SpeedMbps: -1}, nic)
}

func TestLiveRoot(t *testing.T) {
	require.True(t, LiveRoot(""))
	require.True(t, LiveRoot("/"))
	require.True(t, LiveRoot("//"))
	require.False(t, LiveRoot(t.TempDir()))
}

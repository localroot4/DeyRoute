package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/exec"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

func writeFile(t *testing.T, root, p, content string) {
	t.Helper()
	full := filepath.Join(root, p)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

const collectorSecret = "collector-secret-7TgH2kLp"

// fakeRoot builds a server tree: /etc, /proc, /var/log/deyroute, secrets.
func fakeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "/etc/os-release", "NAME=\"Ubuntu\"\nVERSION=\"22.04\"\nPRETTY_NAME=\"Ubuntu 22.04.4 LTS\"\n")
	writeFile(t, root, "/proc/sys/kernel/osrelease", "6.8.0-45-generic\n")
	writeFile(t, root, "/proc/sys/kernel/arch", "x86_64\n")
	writeFile(t, root, "/proc/uptime", "273600.55 1000.00\n") // 3d 04:00
	writeFile(t, root, "/proc/meminfo", "MemTotal:        2000000 kB\nMemFree:  100 kB\nMemAvailable:     100000 kB\n")
	writeFile(t, root, "/proc/sys/net/ipv4/tcp_congestion_control", "bbr\n")
	writeFile(t, root, "/proc/sys/net/ipv4/tcp_available_congestion_control", "reno cubic bbr\n")
	writeFile(t, root, "/proc/sys/net/core/somaxconn", "65535\n")
	writeFile(t, root, "/proc/sys/net/ipv4/tcp_rmem", "4096\t87380   16777216\n")
	writeFile(t, root, "/etc/sysctl.d/99-deyroute.conf", "# managed by deyroute (profile: balanced)\nnet.core.somaxconn = 65535\n")

	// Logs: 250 lines in hub.log, a tunnel log with secrets, a rotated
	// file and a symlink to a key that must be skipped.
	var hub strings.Builder
	for i := 1; i <= 250; i++ {
		fmt.Fprintf(&hub, `{"ts":"2026-09-30T10:00:00Z","level":"info","msg":"line %d"}`+"\n", i)
	}
	writeFile(t, root, "/var/log/deyroute/hub.log", hub.String())
	writeFile(t, root, "/var/log/deyroute/tunnels/main.log",
		"token="+collectorSecret+"\nstarting\n"+fakeKey+"listening on :443")
	writeFile(t, root, "/var/log/deyroute/hub.log.1.gz", "binary")
	writeFile(t, root, "/etc/deyroute/secrets/hub.key", fakeKey)
	require.NoError(t, os.Symlink(filepath.Join(root, "/etc/deyroute/secrets/hub.key"), filepath.Join(root, "/var/log/deyroute/key.log")))

	// Backends.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "/var/lib/deyroute/bin/backhaul/v0.6.5"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "/var/lib/deyroute/bin/frp/app%2Fv0.61.0"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "/var/lib/deyroute/bin/empty"), 0o755))
	writeFile(t, root, "/var/lib/deyroute/bin/deyroute.prev", "old")

	// Certificates: CA (10 years), a tunnel cert, an acme cert and keys.
	ca, err := tlsutil.NewCA("DEYROUTE CA", testNow.AddDate(-1, 0, 0))
	require.NoError(t, err)
	writeFile(t, root, "/etc/deyroute/secrets/ca.crt", string(ca.CertPEM))
	cert, key, err := ca.IssueTunnel("main", nil, []string{"example.com"}, testNow.AddDate(-3, 0, 5))
	require.NoError(t, err)
	writeFile(t, root, "/etc/deyroute/secrets/tls/main/cert.pem", string(cert))
	writeFile(t, root, "/etc/deyroute/secrets/tls/main/key.pem", string(key))
	writeFile(t, root, "/etc/deyroute/secrets/tls/main/acme/cert.pem", "garbage")
	writeFile(t, root, "/etc/deyroute/secrets/node.crt", "not a cert")
	return root
}

func fakeRunner() *exec.Fake {
	f := exec.NewFake()
	show := "systemctl show %s --property=" + systemd.ShowProperties
	f.On(fmt.Sprintf(show, systemd.HubUnit), exec.OK("ActiveState=active\nSubState=running\nMainPID=812\nNRestarts=0\nActiveEnterTimestamp=Tue 2026-09-29 08:00:00 UTC\nResult=success\n"))
	f.On(fmt.Sprintf(show, systemd.NodeUnit), exec.OK("ActiveState=inactive\nSubState=dead\nMainPID=0\nNRestarts=0\nActiveEnterTimestamp=\nResult=success\n"))
	f.On("systemctl list-units deyroute-tun@* --all --full --no-legend --plain", exec.OK(
		"deyroute-tun@main.de-1.backhaul-wssmux.service loaded activating auto-restart DEYROUTE tunnel\n"))
	f.On(fmt.Sprintf(show, "deyroute-tun@main.de-1.backhaul-wssmux.service"), exec.OK(
		"ActiveState=activating\nSubState=auto-restart\nMainPID=0\nNRestarts=7\nResult=exit-code\n"))
	f.On("nft list tables", exec.OK("table inet deyroute\n"))
	f.On("nft list table inet deyroute", exec.OK("table inet deyroute {\n\tchain input {\n\t}\n}\n"))
	f.On("ufw status", exec.OK("Status: active\n"))
	f.On("firewall-cmd --state", exec.Fail(252, "not running"))
	f.On("iptables -S INPUT", exec.OK("-P INPUT ACCEPT\n"))
	f.On("ss -Hlntup", exec.OK(`tcp LISTEN 0 4096 0.0.0.0:443 0.0.0.0:* users:(("nginx",pid=1234,fd=6)) token=`+collectorSecret+"\n"))
	return f
}

func fakeStatfs(p string) (uint64, uint64, error) {
	switch filepath.Base(p) {
	case "var":
		return 100 << 30, 5 << 30, nil // 5 % free
	case "etc":
		return 20 << 30, 10 << 30, nil // same numbers as / → same filesystem
	}
	return 20 << 30, 10 << 30, nil
}

func newCollector(t *testing.T) (*Collector, *exec.Fake, string) {
	dlog.RegisterSecret(collectorSecret)
	root := fakeRoot(t)
	f := fakeRunner()
	return &Collector{Root: root, Runner: f, Now: func() time.Time { return testNow }, Statfs: fakeStatfs}, f, root
}

func TestCollectEverything(t *testing.T) {
	c, _, _ := newCollector(t)
	col := c.Collect(context.Background())

	for _, s := range []string{SectionOS, SectionVersions, SectionUnits, SectionFirewall, SectionSysctl, SectionPorts, SectionCerts,
		"logs/hub.log", "logs/tunnels/main.log"} {
		require.Contains(t, col.Sections, s)
	}
	require.NotContains(t, col.Sections, "logs/hub.log.1.gz")
	require.NotContains(t, col.Sections, "logs/key.log", "symlinks are never followed")
	for name, text := range col.Sections {
		require.NotContains(t, text, collectorSecret, name)
		require.NotContains(t, text, "PRIVATE KEY", name)
		require.NotContains(t, text, "MC4CAQAw", name)
	}

	require.Equal(t, map[string]float64{"/": 50, "/var": 5}, col.DiskFreePct)
	require.InDelta(t, 5.0, col.MemAvailPct, 0.001)
	require.Equal(t, "active", col.UnitStates[systemd.HubUnit])
	require.Equal(t, 7, col.UnitRestarts["deyroute-tun@main.de-1.backhaul-wssmux.service"])
	require.True(t, col.BBRActive)
	require.True(t, col.BBRAvailable)
	require.False(t, col.BBRApplied, "the fake 99-deyroute.conf has no tcp_congestion_control line")
	require.Equal(t, "balanced", col.SysctlProfile)
	require.Len(t, col.Certs, 2)

	// The measurements drive the rules.
	f := Facts{Role: "hub", Now: testNow, Sections: map[string]string{SectionOS: "from daemon"}}
	col.Apply(&f)
	require.Equal(t, "from daemon", f.Sections[SectionOS], "existing sections win")
	require.Equal(t, col.Sections[SectionUnits], f.Sections[SectionUnits])
	rules := rulesOf(Run(f))
	require.Contains(t, rules, RuleCrashLoop)
	require.Contains(t, rules, RuleResources)
	require.NotContains(t, rules, RuleServiceUnit)
	require.NotContains(t, rules, RuleTuning)

	var empty Facts
	col.Apply(&empty)
	require.NotEmpty(t, empty.Sections)
}

// TestCollectBBRForR11: R11 reports BBR only when the applied profile sets
// it, the kernel has it and does not use it.
func TestCollectBBRForR11(t *testing.T) {
	c, _, root := newCollector(t)
	writeFile(t, root, "/etc/sysctl.d/99-deyroute.conf",
		"# managed by deyroute (profile: balanced)\nnet.core.default_qdisc = fq\nnet.ipv4.tcp_congestion_control = bbr\n")
	writeFile(t, root, "/proc/sys/net/ipv4/tcp_congestion_control", "cubic\n")
	r11 := func() []string {
		f := Facts{Role: "hub", Now: testNow}
		c.Collect(context.Background()).Apply(&f)
		require.True(t, f.BBRApplied)
		var out []string
		for _, fd := range Run(f) {
			if fd.Rule == RuleTuning {
				out = append(out, fd.Message)
			}
		}
		return out
	}
	require.Equal(t, []string{"BBR is set by the balanced profile but the kernel does not use it"}, r11())

	// A kernel without tcp_bbr: section 12 skips BBR, nothing to fix.
	writeFile(t, root, "/proc/sys/net/ipv4/tcp_available_congestion_control", "reno cubic\n")
	require.Empty(t, r11())
}

func TestCollectorOS(t *testing.T) {
	c, _, root := newCollector(t)
	s := c.OS()
	require.Contains(t, s, "collected: 2026-09-30T10:00:00Z")
	require.Contains(t, s, "os: Ubuntu 22.04.4 LTS")
	require.Contains(t, s, "kernel: 6.8.0-45-generic")
	require.Contains(t, s, "(x86_64)")
	require.Contains(t, s, "uptime: 3d 04:00")
	require.Contains(t, s, "memory: total 1953 MiB, available 97 MiB (5.0%)")
	require.Contains(t, s, "disk /: total 20.0 GiB, free 10.0 GiB (50.0%)")
	require.Contains(t, s, "disk /etc: same filesystem as /")
	require.Equal(t, map[string]float64{"/": 50, "/var": 5}, c.DiskFreePct())
	require.InDelta(t, 5.0, c.MemAvailPct(), 0.001)

	// Missing files and statfs errors are reported, not fatal.
	require.NoError(t, os.Remove(filepath.Join(root, "/proc/meminfo")))
	require.NoError(t, os.Remove(filepath.Join(root, "/proc/uptime")))
	writeFile(t, root, "/etc/os-release", "NAME=Debian\nVERSION=\"12 (bookworm)\"\n")
	c.Statfs = func(string) (uint64, uint64, error) { return 0, 0, errors.New("boom") }
	s = c.OS()
	require.Contains(t, s, "os: Debian 12 (bookworm)")
	require.Contains(t, s, "memory: unknown")
	require.Contains(t, s, "uptime: unknown")
	require.Contains(t, s, "disk /: unknown (boom)")
	require.Zero(t, c.MemAvailPct())
	require.Empty(t, c.DiskFreePct())

	writeFile(t, root, "/etc/os-release", "")
	writeFile(t, root, "/proc/uptime", "garbage")
	writeFile(t, root, "/proc/meminfo", "MemTotal: 1 kB\n")
	s = c.OS()
	require.Contains(t, s, "os: unknown")
	require.Contains(t, s, "uptime: unknown")
	require.Contains(t, s, "memory: unknown")
}

func TestCollectorVersions(t *testing.T) {
	c, _, root := newCollector(t)
	s := c.Versions()
	require.Contains(t, s, "deyroute ")
	require.Contains(t, s, "backhaul v0.6.5")
	require.Contains(t, s, "frp app/v0.61.0")
	require.Contains(t, s, "empty (no version directory)")
	require.Contains(t, s, "previous deyroute binary")

	require.NoError(t, os.RemoveAll(filepath.Join(root, "/var/lib/deyroute/bin")))
	require.Contains(t, c.Versions(), "backends: none installed")
}

func TestCollectorUnits(t *testing.T) {
	c, f, _ := newCollector(t)
	s := c.Units(context.Background())
	require.Contains(t, s, "deyroute-hub.service  active/running  pid=812  restarts=0  since=2026-09-29T08:00:00Z  result=success")
	require.Contains(t, s, "deyroute-node.service  inactive/dead")
	require.Contains(t, s, "deyroute-tun@main.de-1.backhaul-wssmux.service  activating/auto-restart  pid=0  restarts=7")
	states, restarts := c.UnitFacts(context.Background())
	require.Equal(t, "inactive", states[systemd.NodeUnit])
	require.Equal(t, 7, restarts["deyroute-tun@main.de-1.backhaul-wssmux.service"])

	// systemctl failures end up in the section text.
	f2 := exec.NewFake()
	c.Runner = f2
	s = c.Units(context.Background())
	require.Contains(t, s, "deyroute-hub.service  error:")
	require.Contains(t, s, "deyroute-tun@*  error:")
	_ = f
}

func TestCollectorLogs(t *testing.T) {
	c, _, root := newCollector(t)
	logs := c.Logs()
	hub := logs["logs/hub.log"]
	ls := lines(hub)
	require.Len(t, ls, LogTailLines)
	require.Contains(t, ls[0], `"line 51"`)
	require.Contains(t, ls[len(ls)-1], `"line 250"`)
	main := logs["logs/tunnels/main.log"]
	require.Contains(t, main, "token=***")
	require.Contains(t, main, "starting")
	require.Contains(t, main, "listening on :443")
	require.NotContains(t, main, "MC4CAQAw")

	// No log directory: no sections.
	require.NoError(t, os.RemoveAll(filepath.Join(root, "/var/log/deyroute")))
	require.Empty(t, c.Logs())
}

func TestTailFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")

	require.NoError(t, os.WriteFile(p, nil, 0o600))
	s, err := tailPath(p, 3, 1<<20)
	require.NoError(t, err)
	require.Empty(t, s)

	require.NoError(t, os.WriteFile(p, []byte("a\nb\nc\nd\ne"), 0o600))
	s, err = tailPath(p, 3, 1<<20)
	require.NoError(t, err)
	require.Equal(t, "c\nd\ne", s)

	// Byte limit cuts a partial line, which is dropped.
	long := strings.Repeat("x", 100) + "\n" + strings.Repeat("y", 10) + "\nlast\n"
	require.NoError(t, os.WriteFile(p, []byte(long), 0o600))
	s, err = tailPath(p, 10, 20) // "xxx\n" is partial and dropped
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("y", 10)+"\nlast\n", s)
	s, err = tailPath(p, 10, 8) // "yy\n" is partial and dropped
	require.NoError(t, err)
	require.Equal(t, "last\n", s)
	s, err = tailPath(p, 10, 3)
	require.NoError(t, err)
	require.Empty(t, s)

	// Many lines across chunk boundaries.
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "line-%05d\n", i)
	}
	require.NoError(t, os.WriteFile(p, []byte(b.String()), 0o600))
	s, err = tailPath(p, 200, 1<<20)
	require.NoError(t, err)
	ls := lines(s)
	require.Len(t, ls, 200)
	require.Equal(t, "line-04800", ls[0])
	require.Equal(t, "line-04999", ls[199])

	_, err = tailPath(filepath.Join(dir, "missing"), 3, 10)
	require.Error(t, err)
}

// tailPath is tailFile on a file path (tests).
func tailPath(p string, n int, maxBytes int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	return tailFile(f, st.Size(), n, maxBytes)
}

func TestCollectorFirewall(t *testing.T) {
	c, f, _ := newCollector(t)
	s := c.Firewall(context.Background())
	require.Contains(t, s, "detected: nftables, ufw")
	require.Contains(t, s, "--- nft list table inet deyroute")
	require.Contains(t, s, "chain input")

	f.On("nft list table inet deyroute", exec.Fail(1, "Error: No such file or directory"))
	f.On("nft list tables", exec.Fail(1, "no nft"))
	f.On("ufw status", exec.OK("Status: inactive\n"))
	s = c.Firewall(context.Background())
	require.Contains(t, s, "detected: none")
	require.Contains(t, s, "(table inet deyroute does not exist)")

	f.On("nft list table inet deyroute", exec.Fail(1, "permission denied"))
	require.Contains(t, c.Firewall(context.Background()), "error:")
}

func TestCollectorSysctl(t *testing.T) {
	c, _, root := newCollector(t)
	s := c.Sysctl()
	require.Contains(t, s, "profile: balanced")
	require.Contains(t, s, "bbr available: yes")
	require.Contains(t, s, "bbr active: yes")
	require.Contains(t, s, "net.core.somaxconn = 65535")
	require.Contains(t, s, "net.ipv4.tcp_rmem = 4096 87380 16777216")
	require.Contains(t, s, "net.ipv4.tcp_fastopen = (missing)")
	require.Contains(t, s, "net.ipv4.ip_forward = (missing)")
	require.Contains(t, SysctlKeys(), "net.ipv4.tcp_notsent_lowat")
	require.Contains(t, SysctlKeys(), "net.ipv4.tcp_available_congestion_control")

	writeFile(t, root, "/etc/sysctl.d/99-deyroute.conf", "no header\n")
	s, bbr, profile := c.sysctlInfo()
	require.Contains(t, s, "profile: unknown")
	require.Empty(t, profile)
	require.True(t, bbr)
}

func TestCollectorPorts(t *testing.T) {
	c, f, root := newCollector(t)
	s := c.Ports(context.Background())
	require.Contains(t, s, "source: ss -Hlntup")
	require.Contains(t, s, `"nginx",pid=1234`)
	require.NotContains(t, s, collectorSecret)

	f.On("ss -Hlntup", exec.OK(""))
	require.Contains(t, c.Ports(context.Background()), "no listening sockets")

	// ss missing: /proc/net fallback with owner lookup.
	f.On("ss -Hlntup", exec.Fail(127, "not found"))
	writeFile(t, root, "/proc/net/tcp",
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
			"   0: 00000000:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 4242 1\n"+
			"   1: 0100007F:1F90 0100007F:D431 01 00000000:00000000 00:00000000 00000000     0        0 4343 1\n"+
			"   bad line\n")
	writeFile(t, root, "/proc/net/tcp6",
		"  sl  local_address rem_address st\n"+
			"   0: 00000000000000000000000000000000:0035 00000000000000000000000000000000:0000 0A 0 0 0 0 0 5151 1\n")
	writeFile(t, root, "/proc/net/udp",
		"  sl  local_address rem_address st\n"+
			"   0: 00000000:6987 00000000:0000 07 0 0 0 0 0 6161 1\n"+
			"   1: 0100007F:6988 08080808:0035 01 0 0 0 0 0 6262 1\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "/proc/1234/fd"), 0o755))
	require.NoError(t, os.Symlink("socket:[4242]", filepath.Join(root, "/proc/1234/fd/6")))
	writeFile(t, root, "/proc/1234/comm", "nginx\n")

	s = c.Ports(context.Background())
	require.Contains(t, s, "source: /proc/net (ss failed:")
	require.Contains(t, s, "tcp  0.0.0.0:443  nginx (pid 1234)")
	require.Contains(t, s, "tcp  [::]:53")
	require.Contains(t, s, "udp  0.0.0.0:27015")
	require.NotContains(t, s, "8080")
	require.NotContains(t, s, "27016")

	for _, f := range []string{"tcp", "tcp6", "udp"} {
		require.NoError(t, os.Remove(filepath.Join(root, "/proc/net", f)))
	}
	require.Contains(t, c.Ports(context.Background()), "no listening sockets")

	for _, bad := range []string{"nocolon", "0100007F:zz", "zz:0001", "0100:0001"} {
		_, ok := parseHexAddrPort(bad)
		require.False(t, ok, bad)
	}
}

func TestCollectorCerts(t *testing.T) {
	c, _, root := newCollector(t)
	s := c.Certs()
	require.Contains(t, s, "ca.crt  kind=ca  subject=DEYROUTE CA")
	require.Contains(t, s, "tls/main/cert.pem  kind=tunnel")
	require.Contains(t, s, "tls/main/acme/cert.pem  not a certificate")
	require.Contains(t, s, "node.crt  not a certificate")
	require.NotContains(t, s, "key.pem")
	require.NotContains(t, s, "hub.key")
	require.NotContains(t, s, "PRIVATE")

	certs := c.CertExpiries()
	require.Len(t, certs, 2)
	require.Equal(t, "ca", certs[0].Kind)
	require.Equal(t, "tunnel", certs[1].Kind)
	require.Equal(t, "main", certs[1].Tunnel)
	// The tunnel cert (3 years from 3 years minus 5 days ago) expires soon.
	fs := Run(Facts{Role: "hub", Now: testNow, Certs: certs})
	require.Equal(t, []string{RuleCertExpiry}, rulesOf(fs))
	require.Contains(t, fs[0].Fix, "--tunnel main")

	require.NoError(t, os.RemoveAll(filepath.Join(root, "/etc/deyroute/secrets")))
	require.Contains(t, c.Certs(), "cannot list /etc/deyroute/secrets")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "/etc/deyroute/secrets"), 0o700))
	require.Contains(t, c.Certs(), "no certificates found")

	for name, want := range map[string]bool{"ca.crt": true, "cert.pem": true, "hub.key": false, "key.pem": false, "keycert.crt": false, "x.pem": false} {
		require.Equal(t, want, isCertFile(name), name)
	}
	for rel, kind := range map[string]string{"hub.crt": "hub", "node.crt": "node", "tls/x/acme/cert.pem": "tunnel", "misc/a.crt": "other"} {
		k, _ := certKind(rel)
		require.Equal(t, kind, k, rel)
	}
}

func TestCollectorDefaults(t *testing.T) {
	c := &Collector{}
	require.Equal(t, "/", c.root())
	require.NotNil(t, c.runner())
	require.WithinDuration(t, time.Now(), c.now(), time.Minute)
	require.Equal(t, DefaultCommandTimeout, c.timeout())
	// statfs(2) of the real root works on the build machine.
	total, _, err := c.statfs("/")
	require.NoError(t, err)
	require.NotZero(t, total)
	require.Equal(t, "5 MiB", human(5<<20))
	require.Zero(t, pct(1, 0))
}

func TestFactsFromNodeCollection(t *testing.T) {
	// A node whose control channel is down while its tunnel unit runs.
	col := Collection{UnitStates: map[string]string{systemd.NodeUnit: "active", "deyroute-tun@main.de-1.backhaul-wssmux.service": "active"}}
	f := Facts{Role: "node", Now: testNow, Status: api.Status{Role: "node", NodeSelf: &api.NodeSelf{ID: "de-1"}}}
	col.Apply(&f)
	require.Equal(t, []string{RuleControlOffline}, rulesOf(Run(f)))
}

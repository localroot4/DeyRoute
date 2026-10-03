package sysctl

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

func symlink(t *testing.T, root, target, p string) {
	t.Helper()
	full := filepath.Join(root, p)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.Symlink(target, full))
}

func TestCheckDriftAndOverrides(t *testing.T) {
	m, root := fakeRoot(t)
	drift, findings, err := m.Check(sysinfo.Facts{})
	require.NoError(t, err)
	require.Empty(t, drift, "nothing applied")
	require.Empty(t, findings)

	_, _, err = m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	drift, findings, err = m.Check(sysinfo.Facts{})
	require.NoError(t, err)
	require.Empty(t, drift)
	require.Empty(t, findings)

	// Changed at runtime.
	writeProc(t, root, KeySomaxconn, "1000")
	// Sorted before 99-deyroute.conf: deyroute wins at boot, not reported.
	writeHostFile(t, root, "etc/sysctl.d/98-x.conf", "net.core.somaxconn = 1\nnet.ipv4.tcp_fin_timeout = 99\n")
	// /etc/sysctl.conf is applied last.
	writeHostFile(t, root, "etc/sysctl.conf", "# admin\nnet.ipv4.tcp_fin_timeout = 30\n")
	// A later file in /run with '/' separators and '-'; the same value as
	// deyroute's is not an override.
	writeHostFile(t, root, "run/sysctl.d/99-zz.conf", "-net/ipv4/tcp_keepalive_time = 300\nnet/ipv4/tcp_keepalive_probes = 9\n")
	// Masked by a link to /dev/null in /etc: never applied.
	writeHostFile(t, root, "usr/lib/sysctl.d/99-zzz.conf", "net.ipv4.tcp_fastopen = 1\n")
	symlink(t, root, "/dev/null", "etc/sysctl.d/99-zzz.conf")
	// The same name in /etc masks the one in /usr/lib.
	writeHostFile(t, root, "usr/lib/sysctl.d/99-zz.conf", "net.ipv4.tcp_mtu_probing = 0\n")
	writeHostFile(t, root, "etc/sysctl.d/99-zz.conf", "net.ipv4.tcp_tw_reuse = 1\n")

	want := []DriftItem{
		{Key: KeySomaxconn, Want: "65535", Live: "1000"},
		{Key: "net.ipv4.tcp_fin_timeout", Want: "15", Live: "15", OverriddenBy: "/etc/sysctl.conf"},
	}
	drift, _, err = m.Check(sysinfo.Facts{})
	require.NoError(t, err)
	require.Equal(t, want, drift)
	require.Equal(t, api.TuneDrift{Key: "net.ipv4.tcp_fin_timeout", Want: "15", Live: "15", OverriddenBy: "/etc/sysctl.conf"}, drift[1].API())

	// /run's 99-zz.conf is masked by /etc's: its value is not reported.
	writeHostFile(t, root, "etc/sysctl.d/99-zz.conf", "")
	require.NoError(t, os.Remove(filepath.Join(root, "etc/sysctl.d/99-zz.conf")))
	writeHostFile(t, root, "run/sysctl.d/99-zz.conf", "-net/ipv4/tcp_keepalive_probes = 9\n")
	drift, _, err = m.Check(sysinfo.Facts{})
	require.NoError(t, err)
	require.Equal(t, append(want, DriftItem{Key: "net.ipv4.tcp_keepalive_probes", Want: "5", Live: "5",
		OverriddenBy: "/run/sysctl.d/99-zz.conf"}), drift)

	// Debian's 99-sysctl.conf link to /etc/sysctl.conf names the real file
	// and is read once.
	symlink(t, root, "../sysctl.conf", "etc/sysctl.d/99-sysctl.conf")
	files := m.sysctlFiles()
	n := 0
	for _, f := range files {
		if f.Path == "/etc/sysctl.conf" {
			n++
			require.Equal(t, "99-sysctl.conf", f.Name)
		}
	}
	require.Equal(t, 1, n, "%v", files)
	drift, _, err = m.Check(sysinfo.Facts{})
	require.NoError(t, err)
	require.Equal(t, "/etc/sysctl.conf", drift[1].OverriddenBy)
}

func TestCheckMissingKeysAndHashsize(t *testing.T) {
	m, root := autoRoot(t)
	writeHostFile(t, root, config.SysctlConfPath, "# managed by deyroute (profile: auto)\n"+
		"net.netfilter.nf_conntrack_max = 524288\nnet.core.nothere = 1\n")
	require.NoError(t, os.Remove(filepath.Join(root, "proc/sys/net/netfilter/nf_conntrack_max")))
	writeHostFile(t, root, PathModprobe, "options nf_conntrack hashsize=131072\n")
	drift, _, err := m.Check(sysinfo.Facts{})
	require.NoError(t, err)
	require.Equal(t, []DriftItem{
		{Key: "net.core.nothere", Want: "1"}, // a conntrack key is not drift before the module loads
		{Key: PathHashsize, Want: "131072", Live: "16384"},
	}, drift)

	require.NoError(t, os.Remove(m.ConfPath()))
	require.NoError(t, os.Mkdir(m.ConfPath(), 0o750))
	_, _, err = m.Check(sysinfo.Facts{})
	require.Error(t, err)
}

func TestCheckFindings(t *testing.T) {
	m, root := autoRoot(t)
	_, _, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	writeProc(t, root, "fs.file-nr", "90\t0\t100")
	writeProc(t, root, KeyCongestion, "cubic")
	f := sysinfo.Facts{ConntrackLoaded: true, ConntrackMax: 1000, ConntrackCount: 850, NIC: "eth0", Qdisc: "pfifo_fast"}
	_, findings, err := m.Check(f)
	require.NoError(t, err)
	var msgs []string
	for _, fd := range findings {
		msgs = append(msgs, fd.Check+"/"+fd.Severity+": "+fd.Message())
	}
	require.Equal(t, []string{
		"conntrack_fill/warn: the connection tracking table is 85% full (850 of 1000 entries)",
		"nofile/warn: 90% of the system's file handles are in use (90 of 100)",
		"bbr/warn: BBR was applied but the congestion control is cubic",
		"qdisc/info: eth0 still uses the pfifo_fast queue; fq applies after the next reboot",
	}, msgs)
	require.Equal(t, api.TuneFinding{Check: "qdisc", Severity: "info", Message: findings[3].Message()}, findings[3].API())

	// Below the thresholds, a multiqueue root (fq per queue) and BBR on.
	writeProc(t, root, "fs.file-nr", "10\t0\t100")
	writeProc(t, root, KeyCongestion, "bbr")
	f = sysinfo.Facts{ConntrackLoaded: true, ConntrackMax: 1000, ConntrackCount: 800, NIC: "eth0", Qdisc: "mq"}
	_, findings, err = m.Check(f)
	require.NoError(t, err)
	require.Empty(t, findings)
}

func TestForwardingUsers(t *testing.T) {
	m, root := fakeRoot(t)
	require.Equal(t, "", m.forwardingUsers())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/class/net/eth0"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/class/net/wg-dey0"), 0o750))
	require.Equal(t, "", m.forwardingUsers(), "plain NICs and tunnels are not other users")
	writeHostFile(t, root, "etc/sysctl.d/99-deyroute.conf", "net.ipv4.ip_forward = 1\n")
	require.Equal(t, "", m.forwardingUsers(), "deyroute's own file does not count")
	writeHostFile(t, root, "etc/sysctl.conf", "net.ipv4.ip_forward=1\n")
	require.Equal(t, "/etc/sysctl.conf", m.forwardingUsers())
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/class/net/virbr0"), 0o750))
	require.Equal(t, "virbr0", m.forwardingUsers())
}

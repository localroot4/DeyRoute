package sysctl

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// originals are the fake kernel's values before deyroute (typical defaults,
// with the kernel's tab-separated triples).
var originals = map[string]string{
	KeyDefaultQdisc:                 "fq_codel",
	KeyCongestion:                   "cubic",
	KeyAvailableCC:                  "reno cubic bbr",
	"net.core.somaxconn":            "4096",
	"net.ipv4.tcp_max_syn_backlog":  "1024",
	"net.core.netdev_max_backlog":   "1000",
	"net.ipv4.ip_local_port_range":  "32768\t60999",
	"net.ipv4.tcp_fin_timeout":      "60",
	"net.ipv4.tcp_tw_reuse":         "2",
	"net.ipv4.tcp_keepalive_time":   "7200",
	"net.ipv4.tcp_keepalive_intvl":  "75",
	"net.ipv4.tcp_keepalive_probes": "9",
	"net.ipv4.tcp_fastopen":         "1",
	"net.ipv4.tcp_mtu_probing":      "0",
	"net.core.rmem_max":             "212992",
	"net.core.wmem_max":             "212992",
	"net.ipv4.tcp_rmem":             "4096\t131072\t6291456",
	"net.ipv4.tcp_wmem":             "4096\t16384\t4194304",
	"net.ipv4.udp_rmem_min":         "4096",
	"net.ipv4.udp_wmem_min":         "4096",
	"fs.file-max":                   "9223372036854775807",
	KeyIPForward:                    "0",
	KeyNotsentLowat:                 "4294967295",
	"kernel.osrelease":              "6.8.0-45-generic",
}

func fakeRoot(t *testing.T) (Manager, string) {
	t.Helper()
	root := t.TempDir()
	for k, v := range originals {
		p := filepath.Join(root, "proc", "sys", strings.ReplaceAll(k, ".", "/"))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(v+"\n"), 0o600))
	}
	return Manager{Root: root}, root
}

func get(t *testing.T, m Manager, key string) string {
	t.Helper()
	v, ok := m.Get(key)
	require.True(t, ok, key)
	return v
}

func TestProfileMatchesSpec(t *testing.T) {
	spec, err := os.ReadFile("../../docs/spec/DEYROUTE-spec-v1.0.fa.md")
	require.NoError(t, err)
	text := string(spec)
	i := strings.Index(text, "`balanced`")
	require.Greater(t, i, 0)
	j := strings.Index(text[i:], "```text\n")
	require.Greater(t, j, 0)
	block := text[i+j+len("```text\n"):]
	block = block[:strings.Index(block, "```")]
	var want []KV
	for _, l := range strings.Split(strings.TrimSpace(block), "\n") {
		l, _, _ = strings.Cut(l, "#")
		k, v, ok := strings.Cut(l, "=")
		require.True(t, ok, l)
		want = append(want, KV{strings.TrimSpace(k), strings.TrimSpace(v)})
	}
	require.Equal(t, KeyIPForward, want[len(want)-1].Key)

	got, err := Profile(config.SysctlBalanced, true)
	require.NoError(t, err)
	require.Equal(t, want, got)

	got, err = Profile(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Equal(t, want[:len(want)-1], got)
}

func TestProfiles(t *testing.T) {
	bal, err := Profile(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Len(t, bal, 20)
	require.Equal(t, KV{KeyDefaultQdisc, "fq"}, bal[0])
	require.Equal(t, KV{KeyCongestion, "bbr"}, bal[1])

	agg, err := Profile(config.SysctlAggressive, true)
	require.NoError(t, err)
	require.Len(t, agg, 22)
	idx := map[string]int{}
	vals := map[string]string{}
	for i, kv := range agg {
		idx[kv.Key] = i
		vals[kv.Key] = kv.Value
	}
	require.Equal(t, "67108864", vals["net.core.rmem_max"])
	require.Equal(t, "67108864", vals["net.core.wmem_max"])
	require.Equal(t, "4096 87380 67108864", vals["net.ipv4.tcp_rmem"])
	require.Equal(t, "4096 65536 67108864", vals["net.ipv4.tcp_wmem"])
	require.Equal(t, "16384", vals[KeyNotsentLowat])
	require.Equal(t, idx["net.ipv4.tcp_wmem"]+1, idx[KeyNotsentLowat])
	require.Equal(t, KV{KeyIPForward, "1"}, agg[len(agg)-1])
	// Everything else is identical to balanced, in the same order.
	var rest []KV
	for _, kv := range agg {
		if _, over := aggressiveOverrides[kv.Key]; !over && kv.Key != KeyNotsentLowat && kv.Key != KeyIPForward {
			rest = append(rest, kv)
		}
	}
	var balRest []KV
	for _, kv := range bal {
		if _, over := aggressiveOverrides[kv.Key]; !over {
			balRest = append(balRest, kv)
		}
	}
	require.Equal(t, balRest, rest)

	off, err := Profile(config.SysctlOff, true)
	require.NoError(t, err)
	require.Empty(t, off)

	_, err = Profile("turbo", false)
	require.True(t, deyerr.HasCode(err, deyerr.C013))
	require.Contains(t, deyerr.As(err).Why(), "off, balanced, aggressive")
	require.Equal(t, "a.b = c", KV{"a.b", "c"}.String())
}

func TestApplyBalancedAndRevert(t *testing.T) {
	m, root := fakeRoot(t)
	applied, warnings, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Empty(t, warnings)
	want, _ := Profile(config.SysctlBalanced, false)
	require.Equal(t, want, applied)
	for _, kv := range want {
		require.Equal(t, kv.Value, get(t, m, kv.Key), kv.Key)
	}
	require.Equal(t, "0", get(t, m, KeyIPForward))

	conf, err := os.ReadFile(filepath.Join(root, "etc/sysctl.d/99-deyroute.conf"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(conf)), "\n")
	require.Equal(t, "# managed by deyroute (profile: balanced)", lines[0])
	require.Contains(t, string(conf), "net.core.default_qdisc = fq\nnet.ipv4.tcp_congestion_control = bbr\nnet.core.somaxconn = 65535\n")
	require.Contains(t, string(conf), "net.ipv4.tcp_rmem = 4096 87380 16777216\n")
	fi, err := os.Stat(m.ConfPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())

	backup, err := os.ReadFile(filepath.Join(root, "var/lib/deyroute/sysctl-before-deyroute.conf"))
	require.NoError(t, err)
	require.Contains(t, string(backup), "net.core.somaxconn = 4096\n")
	require.Contains(t, string(backup), "net.ipv4.tcp_rmem = 4096 131072 6291456\n")
	require.Contains(t, string(backup), "net.ipv4.tcp_congestion_control = cubic\n")
	require.NotContains(t, string(backup), KeyIPForward)
	fi, err = os.Stat(m.BackupPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	cur, err := m.Current()
	require.NoError(t, err)
	require.Equal(t, config.SysctlBalanced, cur)
	require.True(t, m.BBRActive())

	// Idempotent: same backup, same result.
	applied2, warnings, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, applied, applied2)
	backup2, _ := os.ReadFile(m.BackupPath())
	require.Equal(t, backup, backup2)

	st, err := m.Status()
	require.NoError(t, err)
	require.Equal(t, config.SysctlBalanced, st.Profile)
	require.True(t, st.BBRAvailable)
	require.True(t, st.BBRActive)
	require.Equal(t, want, st.Applied)

	require.NoError(t, m.Revert())
	for k, v := range originals {
		require.Equal(t, normalize(v), get(t, m, k), k)
	}
	_, err = os.Stat(m.ConfPath())
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(m.BackupPath())
	require.True(t, os.IsNotExist(err))
	cur, err = m.Current()
	require.NoError(t, err)
	require.Equal(t, config.SysctlOff, cur)

	// Nothing to revert.
	require.NoError(t, m.Revert())
}

func TestSwitchProfilesRestoresDroppedKeys(t *testing.T) {
	m, _ := fakeRoot(t)
	_, _, err := m.Apply(config.SysctlBalanced, true)
	require.NoError(t, err)
	require.Equal(t, "1", get(t, m, KeyIPForward))

	_, warnings, err := m.Apply(config.SysctlAggressive, true)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, "67108864", get(t, m, "net.core.rmem_max"))
	require.Equal(t, "16384", get(t, m, KeyNotsentLowat))
	cur, _ := m.Current()
	require.Equal(t, config.SysctlAggressive, cur)

	// The backup gained tcp_notsent_lowat with its original value but kept
	// the pre-deyroute rmem_max (never the balanced value).
	b, err := m.readKVFile(m.BackupPath())
	require.NoError(t, err)
	vals := map[string]string{}
	for _, kv := range b {
		vals[kv.Key] = kv.Value
	}
	require.Equal(t, "4294967295", vals[KeyNotsentLowat])
	require.Equal(t, "212992", vals["net.core.rmem_max"])
	require.Equal(t, "0", vals[KeyIPForward])

	// Back to balanced without ip_forward: dropped keys return to originals.
	_, _, err = m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Equal(t, "4294967295", get(t, m, KeyNotsentLowat))
	require.Equal(t, "0", get(t, m, KeyIPForward))
	require.Equal(t, "16777216", get(t, m, "net.core.rmem_max"))

	// BBR switched off in the config: cc restored, fq kept, no warning.
	applied, warnings, err := m.ApplyWith(ApplyOptions{Profile: config.SysctlBalanced, BBR: false})
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, applied, 19)
	require.Equal(t, "cubic", get(t, m, KeyCongestion))
	require.Equal(t, "fq", get(t, m, KeyDefaultQdisc))
	st, err := m.Status()
	require.NoError(t, err)
	require.False(t, st.BBRActive)
	require.True(t, st.BBRAvailable)

	// off reverts everything (no transport needs forwarding).
	applied, warnings, err = m.Apply(config.SysctlOff, false)
	require.NoError(t, err)
	require.Nil(t, applied)
	require.Nil(t, warnings)
	for k, v := range originals {
		require.Equal(t, normalize(v), get(t, m, k), k)
	}
}

func TestExistingBackupIsNeverOverwritten(t *testing.T) {
	m, root := fakeRoot(t)
	p := filepath.Join(root, "var/lib/deyroute")
	require.NoError(t, os.MkdirAll(p, 0o750))
	require.NoError(t, os.WriteFile(m.BackupPath(), []byte("# old\nnet.core.somaxconn = 128\n../../etc/passwd = x\nbroken line\n"), 0o600))
	_, _, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	b, err := m.readKVFile(m.BackupPath())
	require.NoError(t, err)
	require.Equal(t, KV{"net.core.somaxconn", "128"}, b[0])
	require.NoError(t, m.Revert())
	require.Equal(t, "128", get(t, m, "net.core.somaxconn"))
}

func TestApplyWithoutBBR(t *testing.T) {
	m, root := fakeRoot(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "proc/sys/net/ipv4/tcp_available_congestion_control"), []byte("reno cubic\n"), 0o600))
	require.False(t, m.BBRAvailable())
	applied, warnings, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "tcp_bbr is not available")
	require.Contains(t, warnings[0], "reno cubic")
	require.NotContains(t, warnings[0], KeyDefaultQdisc)
	// Only BBR is skipped (section 12): default_qdisc = fq is its own line.
	require.Len(t, applied, 19)
	require.Equal(t, KV{KeyDefaultQdisc, "fq"}, applied[0])
	require.Equal(t, "cubic", get(t, m, KeyCongestion))
	require.Equal(t, "fq", get(t, m, KeyDefaultQdisc))
	conf, _ := os.ReadFile(m.ConfPath())
	require.NotContains(t, string(conf), KeyCongestion)
	require.Contains(t, string(conf), "net.core.default_qdisc = fq\n")
}

func TestBBRAvailableFromModules(t *testing.T) {
	m, root := fakeRoot(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "proc/sys/net/ipv4/tcp_available_congestion_control"), []byte("reno cubic\n"), 0o600))
	modDir := filepath.Join(root, "lib/modules/6.8.0-45-generic")
	require.NoError(t, os.MkdirAll(modDir, 0o750))
	require.False(t, m.BBRAvailable())
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "modules.dep"), []byte("kernel/net/ipv4/tcp_bbr.ko.zst:\n"), 0o600))
	require.True(t, m.BBRAvailable())

	// The fake kernel does not "load" the module, so the write sticks but a
	// real kernel that refused would be reported; here it applies.
	applied, warnings, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, applied, 20)

	require.NoError(t, os.Remove(filepath.Join(modDir, "modules.dep")))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "modules.builtin"), []byte("kernel/net/ipv4/tcp_bbr.ko\n"), 0o600))
	require.True(t, m.BBRAvailable())

	require.NoError(t, os.WriteFile(filepath.Join(root, "proc/sys/kernel/osrelease"), []byte("../../x\n"), 0o600))
	require.False(t, m.BBRAvailable())
}

// readOnlyKey replaces the fake key file with a symlink to a real
// read-only sysctl, so reads work and writes fail with EACCES even as root.
func readOnlyKey(t *testing.T, root, key string) {
	t.Helper()
	const ro = "/proc/sys/kernel/osrelease"
	if _, err := os.Stat(ro); err != nil {
		t.Skip("no " + ro)
	}
	p := filepath.Join(root, "proc", "sys", strings.ReplaceAll(key, ".", "/"))
	require.NoError(t, os.RemoveAll(p))
	require.NoError(t, os.Symlink(ro, p))
}

func TestMissingAndUnwritableKeys(t *testing.T) {
	m, root := fakeRoot(t)
	require.NoError(t, os.Remove(filepath.Join(root, "proc/sys/net/ipv4/tcp_fastopen")))
	readOnlyKey(t, root, "net.ipv4.tcp_mtu_probing")

	applied, warnings, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Len(t, warnings, 2, "%v", warnings)
	require.Equal(t, "skip net.ipv4.tcp_fastopen: not available on this kernel", warnings[0])
	require.True(t, strings.HasPrefix(warnings[1], "failed net.ipv4.tcp_mtu_probing = 1: "), warnings[1])
	require.Len(t, applied, 18)
	for _, kv := range applied {
		require.NotEqual(t, "net.ipv4.tcp_mtu_probing", kv.Key)
	}
	conf, _ := os.ReadFile(m.ConfPath())
	require.NotContains(t, string(conf), "tcp_fastopen")
	// The conf keeps the unwritable key: it may succeed at boot.
	require.Contains(t, string(conf), "net.ipv4.tcp_mtu_probing = 1\n")
}

func TestRestoreWarningOnProfileSwitch(t *testing.T) {
	m, root := fakeRoot(t)
	_, _, err := m.Apply(config.SysctlAggressive, false)
	require.NoError(t, err)
	readOnlyKey(t, root, KeyNotsentLowat)
	_, warnings, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.True(t, strings.HasPrefix(warnings[0], "restore "+KeyNotsentLowat+": "), warnings[0])
}

func TestBBRWriteNotEffective(t *testing.T) {
	m, root := fakeRoot(t)
	readOnlyKey(t, root, KeyCongestion)
	_, warnings, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Len(t, warnings, 2, "%v", warnings)
	require.Contains(t, warnings[1], "after writing bbr")
}

func TestWriteFailures(t *testing.T) {
	m, root := fakeRoot(t)
	// /etc/sysctl.d is a file → conf write fails with X032 after the backup.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc/sysctl.d"), nil, 0o600))
	_, _, err := m.Apply(config.SysctlBalanced, false)
	require.True(t, deyerr.HasCode(err, deyerr.X032), "%v", err)
	require.Equal(t, "4096", get(t, m, "net.core.somaxconn"), "nothing applied when the conf cannot be written")

	// Backup directory unusable → X032 before anything else.
	m2, root2 := fakeRoot(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root2, "var/lib"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root2, "var/lib/deyroute"), nil, 0o600))
	_, _, err = m2.Apply(config.SysctlBalanced, false)
	require.True(t, deyerr.HasCode(err, deyerr.X000) || deyerr.HasCode(err, deyerr.X032), "%v", err)
	_, statErr := os.Stat(m2.ConfPath())
	require.True(t, os.IsNotExist(statErr))

	// Unknown profile.
	_, _, err = m2.Apply("fast", false)
	require.True(t, deyerr.HasCode(err, deyerr.C013))
}

func TestRevertFailureKeepsBackup(t *testing.T) {
	m, root := fakeRoot(t)
	_, _, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	backupBefore, err := os.ReadFile(m.BackupPath())
	require.NoError(t, err)

	// A key that disappeared since apply is skipped.
	require.NoError(t, os.Remove(filepath.Join(root, "proc/sys/net/core/somaxconn")))
	// A key that cannot be written makes Revert fail and keep the backup.
	readOnlyKey(t, root, "net.ipv4.tcp_fin_timeout")
	err = m.Revert()
	require.True(t, deyerr.HasCode(err, deyerr.X033), "%v", err)
	require.Contains(t, deyerr.As(err).Message(), "net.ipv4.tcp_fin_timeout")
	after, err := os.ReadFile(m.BackupPath())
	require.NoError(t, err)
	require.Equal(t, backupBefore, after)
	_, err = os.Stat(m.ConfPath())
	require.True(t, os.IsNotExist(err), "conf is removed even when a value could not be restored")
	// Every other key was still restored.
	require.Equal(t, "1024", get(t, m, "net.ipv4.tcp_max_syn_backlog"))

	// Once the key is writable again, Revert completes and drops the backup.
	p := filepath.Join(root, "proc/sys/net/ipv4/tcp_fin_timeout")
	require.NoError(t, os.Remove(p))
	require.NoError(t, os.WriteFile(p, []byte("15\n"), 0o600))
	require.NoError(t, m.Revert())
	require.Equal(t, "60", get(t, m, "net.ipv4.tcp_fin_timeout"))
	_, err = os.Stat(m.BackupPath())
	require.True(t, os.IsNotExist(err))
}

func TestSetInvalidKey(t *testing.T) {
	m, _ := fakeRoot(t)
	require.True(t, deyerr.HasCode(m.set("../etc/passwd", "x"), deyerr.X033))
	_, ok := m.Get("../etc/passwd")
	require.False(t, ok)
	_, ok = m.Get("net.core.nonexistent")
	require.False(t, ok)
}

func TestCurrentErrors(t *testing.T) {
	m, root := fakeRoot(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc/sysctl.d"), 0o750))
	require.NoError(t, os.WriteFile(m.ConfPath(), []byte("net.core.somaxconn = 1\n"), 0o600))
	_, err := m.Current()
	require.Error(t, err)
	_, err = m.Status()
	require.Error(t, err)

	require.NoError(t, os.Remove(m.ConfPath()))
	require.NoError(t, os.Mkdir(m.ConfPath(), 0o750))
	_, err = m.Current()
	require.Error(t, err)
	_, err = m.Applied()
	require.Error(t, err)
}

func TestMemoryRecommendation(t *testing.T) {
	require.True(t, RecommendAggressive(4<<30))
	require.True(t, RecommendAggressive(4_025_000*1024)) // a "4 GB" VPS
	require.False(t, RecommendAggressive(2<<30))
	require.False(t, RecommendAggressive(3_500_000*1024))

	m, root := fakeRoot(t)
	_, err := m.MemTotal()
	require.Error(t, err)
	require.Equal(t, config.SysctlBalanced, m.Recommended())

	mi := filepath.Join(root, "proc/meminfo")
	require.NoError(t, os.WriteFile(mi, []byte("MemTotal:        8123456 kB\nMemFree: 1 kB\n"), 0o600))
	mem, err := m.MemTotal()
	require.NoError(t, err)
	require.Equal(t, uint64(8123456*1024), mem)
	require.Equal(t, config.SysctlAggressive, m.Recommended())

	_, warnings, err := m.Apply(config.SysctlAggressive, false)
	require.NoError(t, err)
	require.Empty(t, warnings)

	require.NoError(t, os.WriteFile(mi, []byte("MemTotal:        1000000 kB\n"), 0o600))
	require.Equal(t, config.SysctlBalanced, m.Recommended())
	// aggressive on 1 GB is applied with a warning (section 12: >= 4 GB).
	applied, warnings, err := m.Apply(config.SysctlAggressive, false)
	require.NoError(t, err)
	require.Len(t, applied, 21)
	require.Equal(t, []string{"aggressive is meant for servers with 4 GB RAM or more; this one has 976 MB (balanced suits it better)"}, warnings)
	_, warnings, err = m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Empty(t, warnings)

	require.NoError(t, os.WriteFile(mi, []byte("MemTotal: lots kB\n"), 0o600))
	_, err = m.MemTotal()
	require.Error(t, err)
	require.NoError(t, os.WriteFile(mi, []byte("MemFree: 1 kB\n"), 0o600))
	_, err = m.MemTotal()
	require.Error(t, err)
}

func TestDefaultRootPaths(t *testing.T) {
	m := Manager{}
	require.Equal(t, "/etc/sysctl.d/99-deyroute.conf", m.ConfPath())
	require.Equal(t, "/var/lib/deyroute/sysctl-before-deyroute.conf", m.BackupPath())
	p, ok := m.procPath("net.ipv4.tcp_rmem")
	require.True(t, ok)
	require.Equal(t, "/proc/sys/net/ipv4/tcp_rmem", p)
	p, ok = m.procPath("fs.file-max")
	require.True(t, ok)
	require.Equal(t, "/proc/sys/fs/file-max", p)
}

// Directories created by Apply stay traversable: when sysctl is the first
// to create /var/lib/deyroute, the deyroute user must still reach
// /var/lib/deyroute/bin/<backend>/ to execute backends.
func TestApplyCreatesTraversableDirs(t *testing.T) {
	m, root := fakeRoot(t)
	_, _, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	for _, d := range []string{"var/lib/deyroute", "etc/sysctl.d"} {
		fi, err := os.Stat(filepath.Join(root, d))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), fi.Mode().Perm(), d)
	}
}

// Re-applying the same profile (every reconcile) does not rewrite the conf;
// a wrong mode is repaired.
func TestApplyDoesNotRewriteUnchangedConf(t *testing.T) {
	m, _ := fakeRoot(t)
	_, _, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	before, err := os.Stat(m.ConfPath())
	require.NoError(t, err)
	_, _, err = m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	after, err := os.Stat(m.ConfPath())
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after), "unchanged conf was replaced")

	require.NoError(t, os.Chmod(m.ConfPath(), 0o600))
	_, _, err = m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	fi, err := os.Stat(m.ConfPath())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
}

// Concurrent applies (Local API request + reconcile loop) must never record
// a value deyroute wrote as the pre-deyroute value.
func TestConcurrentApplyKeepsOriginals(t *testing.T) {
	for round := 0; round < 5; round++ {
		m, _ := fakeRoot(t)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				profile := config.SysctlBalanced
				if i%2 == 1 {
					profile = config.SysctlAggressive
				}
				_, _, err := m.Apply(profile, i%3 == 0)
				require.NoError(t, err)
			}(i)
		}
		wg.Wait()
		b, err := m.readKVFile(m.BackupPath())
		require.NoError(t, err)
		for _, kv := range b {
			require.Equal(t, normalize(originals[kv.Key]), kv.Value, "round %d: backup of %s", round, kv.Key)
		}
		require.NoError(t, m.Revert())
		for k, v := range originals {
			require.Equal(t, normalize(v), get(t, m, k), k)
		}
	}
}

func TestParseKV(t *testing.T) {
	got := parseKV([]byte("# c\n; c\n\na.b = 1\na.b = 2\n x.y=  3   4 \nnoeq\nBAD.KEY = 1\n"))
	require.Equal(t, []KV{{"a.b", "1"}, {"x.y", "3 4"}}, got)
}

// A runtime change (ip_forward for a NAT transport) is recorded in the
// backup, so Revert and uninstall restore the server's own value.
func TestEnsureRecordsTheOriginalValue(t *testing.T) {
	m, _ := fakeRoot(t)
	require.NoError(t, m.Ensure(KeyIPForward, "1"))
	require.Equal(t, "1", get(t, m, KeyIPForward))
	require.NoError(t, m.Ensure(KeyIPForward, "1"), "idempotent")
	backup, err := os.ReadFile(m.BackupPath())
	require.NoError(t, err)
	require.Contains(t, string(backup), KeyIPForward+" = 0")

	// A later profile keeps the first original; Revert restores it.
	_, _, err = m.Apply(config.SysctlBalanced, true)
	require.NoError(t, err)
	require.NoError(t, m.Revert())
	require.Equal(t, "0", get(t, m, KeyIPForward))

	require.Error(t, m.Ensure("net.no.such.key", "1"))
}

// "off" while a WireGuard/AmneziaWG side forwards keeps ip_forward on (and
// still remembers the original 0 for uninstall).
func TestApplyOffKeepsIPForwardWhenNeeded(t *testing.T) {
	m, _ := fakeRoot(t)
	require.NoError(t, m.Ensure(KeyIPForward, "1"))
	_, _, err := m.ApplyWith(ApplyOptions{Profile: config.SysctlBalanced, BBR: true, IPForward: true})
	require.NoError(t, err)
	_, _, err = m.ApplyWith(ApplyOptions{Profile: config.SysctlOff, IPForward: true})
	require.NoError(t, err)
	require.Equal(t, "1", get(t, m, KeyIPForward))
	backup, err := os.ReadFile(m.BackupPath())
	require.NoError(t, err)
	require.Contains(t, string(backup), KeyIPForward+" = 0")
	require.NoError(t, m.Revert())
	require.Equal(t, "0", get(t, m, KeyIPForward))

	// Without the need, off restores 0.
	require.NoError(t, m.Ensure(KeyIPForward, "1"))
	_, _, err = m.ApplyWith(ApplyOptions{Profile: config.SysctlOff})
	require.NoError(t, err)
	require.Equal(t, "0", get(t, m, KeyIPForward))
}

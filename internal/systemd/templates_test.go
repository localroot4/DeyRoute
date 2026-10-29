package systemd

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const deployDir = "../../deploy/systemd"

func TestTemplatesIdenticalToDeploy(t *testing.T) {
	entries, err := os.ReadDir(deployDir)
	require.NoError(t, err)
	var deployed []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".service") {
			deployed = append(deployed, e.Name())
		}
	}
	want := append([]string(nil), UnitNames...)
	sort.Strings(want)
	sort.Strings(deployed)
	require.Equal(t, want, deployed, "deploy/systemd and the embedded units differ in file set")

	tpl := Templates()
	require.Len(t, tpl, len(UnitNames))
	for _, name := range UnitNames {
		disk, err := os.ReadFile(filepath.Join(deployDir, name))
		require.NoError(t, err)
		require.Equal(t, string(disk), string(tpl[name]),
			"internal/systemd/units/%s must be a byte-for-byte copy of deploy/systemd/%s", name, name)
	}

	// Templates returns copies.
	tpl[HubUnit][0] = 'X'
	again, ok := Template(HubUnit)
	require.True(t, ok)
	require.NotEqual(t, byte('X'), again[0])
	_, ok = Template("nope.service")
	require.False(t, ok)
}

// sections parses a unit file into section → lines (comments dropped).
func sections(t *testing.T, data []byte) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	cur := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		switch {
		case l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, ";"):
		case strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]"):
			cur = l
		default:
			out[cur] = append(out[cur], l)
		}
	}
	require.NoError(t, sc.Err())
	return out
}

func contains(lines []string, l string) bool {
	for _, x := range lines {
		if x == l {
			return true
		}
	}
	return false
}

// TestHardeningLinesMatchSpec extracts the ini block of section 11 from the
// specification and compares it with HardeningLines, so the list cannot
// drift from the spec.
func TestHardeningLinesMatchSpec(t *testing.T) {
	spec, err := os.ReadFile("../../docs/spec/DEYROUTE-spec-v1.0.fa.md")
	require.NoError(t, err)
	text := string(spec)
	anchor := strings.Index(text, "deyroute-tun@.service`؛")
	require.Greater(t, anchor, 0, "section 11 hardening heading not found")
	start := strings.Index(text[anchor:], "```ini\n")
	require.GreaterOrEqual(t, start, 0)
	block := text[anchor+start+len("```ini\n"):]
	block = block[:strings.Index(block, "```")]
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(block), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && l != "[Service]" {
			lines = append(lines, l)
		}
	}
	require.Equal(t, lines, HardeningLines)
}

func TestTunTemplateHardening(t *testing.T) {
	data, ok := Template(TunTemplate)
	require.True(t, ok)
	sec := sections(t, data)
	for _, l := range HardeningLines {
		if strings.HasPrefix(l, "StartLimitIntervalSec=") {
			// systemd reads the start-rate limit from [Unit] only.
			require.True(t, contains(sec["[Unit]"], l), "template [Unit] lacks %q", l)
			continue
		}
		require.True(t, contains(sec["[Service]"], l), "template [Service] lacks %q", l)
	}
	require.True(t, contains(sec["[Install]"], "WantedBy=multi-user.target"))
	// No per-instance values in the shared template.
	for _, l := range sec["[Service]"] {
		require.False(t, strings.HasPrefix(l, "ExecStart="), l)
		require.False(t, strings.HasPrefix(l, "WorkingDirectory="), l)
	}
}

func TestDaemonUnits(t *testing.T) {
	for _, name := range []string{HubUnit, NodeUnit} {
		data, ok := Template(name)
		require.True(t, ok)
		svc := sections(t, data)["[Service]"]
		for _, l := range []string{
			"Type=notify", "Restart=always", "WatchdogSec=30", "NotifyAccess=main",
			"ProtectHome=true", "PrivateTmp=true", "NoNewPrivileges=true", "LimitNOFILE=1048576",
		} {
			require.True(t, contains(svc, l), "%s lacks %q", name, l)
		}
	}
	hub, _ := Template(HubUnit)
	require.Contains(t, string(hub), "ExecStart=/usr/local/bin/deyroute daemon hub")
	node, _ := Template(NodeUnit)
	require.Contains(t, string(node), "ExecStart=/usr/local/bin/deyroute daemon node")
}

// TestUnitFilesHaveHardening checks the sandbox of the root daemons (DR-20):
// ProtectSystem=strict with an exact write list, an exact capability
// bounding set, the other directives of the design, and that every
// ReadWritePaths= and CapabilityBoundingSet= line carries its reason in a
// comment line right above it. systemd reads a trailing "# …" as part of
// the value, so no directive line may carry one; the parser here does not
// join continuation lines, so none may end in a backslash either. The
// deploy/systemd copies must be identical to the embedded ones.
func TestUnitFilesHaveHardening(t *testing.T) {
	common := []string{
		"ProtectSystem=strict", "ProtectHome=true", "PrivateTmp=true", "NoNewPrivileges=true",
		"ProtectKernelModules=false", "ProtectControlGroups=true", "RestrictSUIDSGID=true",
		"LockPersonality=true", "SystemCallArchitectures=native",
	}
	caps := []string{
		"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_FOWNER", "CAP_NET_ADMIN",
		"CAP_NET_BIND_SERVICE", "CAP_NET_RAW", "CAP_SYS_PTRACE",
	}
	want := map[string]struct{ paths, caps []string }{
		HubUnit: {
			paths: []string{
				"-/etc/modprobe.d", "-/etc/modules-load.d", "-/etc/sysctl.d", "/etc/deyroute",
				"/etc/systemd/system", "/run", "/usr/local/bin", "/var/lib/deyroute", "/var/log/deyroute",
			},
			caps: caps,
		},
		NodeUnit: {
			paths: []string{"/etc", "/run", "/usr/local/bin", "/var/lib", "/var/log"},
			caps:  append([]string{"CAP_AUDIT_WRITE"}, caps...),
		},
	}
	for _, name := range []string{HubUnit, NodeUnit} {
		data, ok := Template(name)
		require.True(t, ok)
		disk, err := os.ReadFile(filepath.Join(deployDir, name))
		require.NoError(t, err)
		require.Equal(t, string(disk), string(data), "deploy/systemd/%s differs from the embedded unit", name)

		var paths, bound []string
		prevComment := false
		for i, raw := range strings.Split(string(data), "\n") {
			l := strings.TrimSpace(raw)
			comment := strings.HasPrefix(l, "#") || strings.HasPrefix(l, ";")
			if l != "" && !comment && !strings.HasPrefix(l, "[") {
				require.NotContains(t, l, "#", "%s:%d: a trailing comment is part of the value: %q", name, i+1, l)
				require.NotContains(t, l, " ;", "%s:%d: a trailing comment is part of the value: %q", name, i+1, l)
				require.False(t, strings.HasSuffix(l, `\`), "%s:%d: no continuation lines: %q", name, i+1, l)
			}
			for key, dst := range map[string]*[]string{"ReadWritePaths=": &paths, "CapabilityBoundingSet=": &bound} {
				if v, ok := strings.CutPrefix(l, key); ok {
					require.True(t, prevComment, "%s:%d: %q needs its reason in a comment line above it", name, i+1, l)
					*dst = append(*dst, strings.Fields(v)...)
				}
			}
			prevComment = comment
		}
		svc := sections(t, data)["[Service]"]
		for _, l := range common {
			require.True(t, contains(svc, l), "%s lacks %q", name, l)
		}
		for _, l := range svc {
			// sysctl writes /proc/sys and the conntrack hash size in /sys.
			require.False(t, strings.HasPrefix(l, "ProtectKernelTunables="), "%s: %q", name, l)
			require.False(t, strings.HasPrefix(l, "AmbientCapabilities="), "%s: %q", name, l)
		}
		sort.Strings(paths)
		sort.Strings(bound)
		w := want[name]
		sort.Strings(w.paths)
		sort.Strings(w.caps)
		require.Equal(t, w.paths, paths, "%s ReadWritePaths", name)
		require.Equal(t, w.caps, bound, "%s CapabilityBoundingSet", name)
	}
}

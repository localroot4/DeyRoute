package systemd

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

type dropInCase struct {
	name    string
	spec    backend.UnitSpec
	workDir string
	logFile string
}

func dropInCases() []dropInCase {
	return []dropInCase{
		{
			name: "backhaul-plain",
			spec: backend.UnitSpec{
				ExecStart: []string{"/var/lib/deyroute/bin/backhaul/v0.6.5/backhaul", "-c", "/etc/deyroute/backends/backhaul/main/de-1/wssmux/config.toml"},
			},
			workDir: "/etc/deyroute/backends/backhaul/main/de-1/wssmux",
			logFile: "/var/log/deyroute/tunnels/main.log",
		},
		{
			name: "wireguard-root-oneshot",
			spec: backend.UnitSpec{
				ExecStart:       []string{"/usr/local/bin/deyroute", "wg", "up", "--tunnel", "main", "--config", "/etc/deyroute/backends/wireguard/main/de-1/kernel/wg.json"},
				ExecStop:        [][]string{{"/usr/local/bin/deyroute", "wg", "down", "--tunnel", "main"}},
				Type:            "oneshot",
				RemainAfterExit: true,
				RunAsRoot:       true,
				ExtraCaps:       []string{"CAP_NET_ADMIN"},
				AddressFamilies: []string{"AF_NETLINK"},
			},
			workDir: "/etc/deyroute/backends/wireguard/main/de-1/kernel",
			logFile: "/var/log/deyroute/tunnels/main.log",
		},
		{
			name: "waterwall-no-mdwe",
			spec: backend.UnitSpec{
				ExecStart:        []string{"/var/lib/deyroute/bin/waterwall/v1.37/Waterwall"},
				WorkingDirectory: "/etc/deyroute/backends/waterwall/game/nl-2/reverse-reality",
				Env:              map[string]string{"WATERWALL_LOG": "debug"},
				DropHardening: map[string]string{
					"MemoryDenyWriteExecute": "Waterwall maps writable+executable memory at start-up and is killed under MemoryDenyWriteExecute",
				},
				ReadWritePaths: []string{"/etc/deyroute/backends/waterwall/game/nl-2/reverse-reality/log"},
			},
			workDir: "/ignored/because/spec/sets/it",
			logFile: "/var/log/deyroute/tunnels/game.log",
		},
		{
			name: "escaping",
			spec: backend.UnitSpec{
				ExecStart: []string{"/opt/my bin/tool", "--name", "a b", `quote"d`, "it's", "100%", "$HOME", `back\slash`, ";", "", "line1\nline2", "tab\there", "bell\a", "ok"},
				ExecStartPre: [][]string{
					{"/usr/local/bin/deyroute", "check", "--tunnel", "t1"},
					{},
				},
				Env: map[string]string{
					"B":       `say "hi" 50% $X \n`,
					"A":       "1",
					"bad key": "x",
					"LINE":    "a\nb",
				},
				Type:            "exec",
				ExtraCaps:       []string{"CAP_NET_RAW", "CAP_NET_ADMIN", "CAP_NET_RAW", "not-a-cap", "CAP_NET_BIND_SERVICE"},
				AddressFamilies: []string{"AF_PACKET", "AF_NETLINK", "AF_INET", "bogus"},
				ReadWritePaths:  []string{"/var/lib/x", "-/run/deyroute y", "/var/lib/x", "", "/bad\npath"},
				DropHardening: map[string]string{
					"SystemCallFilter": "needs io_uring_setup",
					"UnknownOpt":       "",
					"bad\nopt":         "x",
					"ProtectSystem":    "writes /usr/share\nsecond line",
				},
			},
			workDir: "/etc/deyroute/backends/x/%i",
			logFile: "/var/log/deyroute/tunnels/t1\n.log",
		},
		{
			name: "dropped-lists",
			spec: backend.UnitSpec{
				ExecStart:       []string{"/var/lib/deyroute/bin/gost/v3.0.0/gost", "-C", "gost.yaml"},
				ExtraCaps:       []string{"CAP_NET_RAW"},
				AddressFamilies: []string{"AF_PACKET"},
				DropHardening: map[string]string{
					"RestrictAddressFamilies": "opens AF_PACKET and AF_NETLINK sockets for interface discovery",
					"CapabilityBoundingSet":   "spawns helpers that need their own capabilities",
				},
			},
		},
		{
			// Non-hardening names in DropHardening must never be reset (an
			// empty User= runs the backend as root, ExecStart= wipes the
			// command), and a trailing backslash must never continue a line
			// into the next directive.
			name: "unsafe-values",
			spec: backend.UnitSpec{
				ExecStart:        []string{"/var/lib/deyroute/bin/xray/v26.3.27/xray", "run", `C:\`},
				WorkingDirectory: `/srv/odd\`,
				Type:             `simple\`,
				DropHardening: map[string]string{
					"User":                   "must never reset the service user",
					"Group":                  "must never reset the service group",
					"ExecStart":              "must never wipe the command",
					"StandardOutput":         "must never redirect the log",
					"AmbientCapabilities":    "must never be reset here",
					"MemoryDenyWriteExecute": `JIT needs W+X pages \`,
					"LockPersonality":        "   ",
				},
			},
			logFile: "/var/log/deyroute/tunnels/odd.log",
		},
	}
}

// directives parses a unit file the way systemd does for line structure:
// a line ending in an unescaped backslash is joined with the next one
// (comment lines included), then comments and section headers are dropped.
func directives(content string) []string {
	var out []string
	cont := ""
	for _, l := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if cont != "" {
			if t := strings.TrimLeft(l, " \t"); strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
				continue
			}
			l = cont + l
			cont = ""
		}
		if continues(l) {
			cont = l[:len(l)-1] + " "
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" || t[0] == '#' || t[0] == ';' || t[0] == '[' {
			continue
		}
		out = append(out, t)
	}
	if cont != "" {
		out = append(out, strings.TrimSpace(cont))
	}
	return out
}

func TestDirectivesHelper(t *testing.T) {
	require.Equal(t, []string{"A=1 B=2", "C=3"}, directives("[Service]\nA=1\\\nB=2\nC=3\n"))
	// A comment ending in a backslash swallows the next directive.
	require.Equal(t, []string{"Z=9"}, directives("# reason \\\nMemoryDenyWriteExecute=false\nZ=9\n"))
	require.Equal(t, []string{`P=a\\`, "Q=1"}, directives("P=a\\\\\nQ=1\n"))
}

// TestRenderDropInLineStructure: every rendered directive line is exactly
// one directive for systemd; nothing is joined or swallowed.
func TestRenderDropInLineStructure(t *testing.T) {
	for _, c := range dropInCases() {
		got := string(RenderDropIn(c.spec, c.workDir, c.logFile))
		var want []string
		for _, l := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
			require.False(t, continues(l), "%s: line continues into the next one: %q", c.name, l)
			if l != "" && l[0] != '#' && l[0] != '[' {
				want = append(want, l)
			}
		}
		require.Equal(t, want, directives(got), c.name)
	}
}

func TestRenderDropInNeverResetsNonHardening(t *testing.T) {
	c := dropInCases()[5]
	require.Equal(t, "unsafe-values", c.name)
	d := directives(string(RenderDropIn(c.spec, c.workDir, c.logFile)))
	for _, forbidden := range []string{"User=", "Group=", "StandardOutput=", "AmbientCapabilities="} {
		require.NotContains(t, d, forbidden)
	}
	require.Equal(t, "ExecStart=", d[2])
	require.Equal(t, `ExecStart=/var/lib/deyroute/bin/xray/v26.3.27/xray run C:\\`, d[3])
	require.Contains(t, d, "MemoryDenyWriteExecute=false")
	require.Contains(t, d, "LockPersonality=false")
	require.Contains(t, d, "WorkingDirectory=/srv/odd_")
	require.False(t, Relaxable("User"))
	require.True(t, Relaxable("MemoryDenyWriteExecute"))
}

func TestRenderDropInGolden(t *testing.T) {
	for _, c := range dropInCases() {
		t.Run(c.name, func(t *testing.T) {
			got := RenderDropIn(c.spec, c.workDir, c.logFile)
			// Deterministic.
			require.Equal(t, string(got), string(RenderDropIn(c.spec, c.workDir, c.logFile)))
			path := filepath.Join("testdata", "dropin-"+c.name+".golden")
			if *update {
				require.NoError(t, os.WriteFile(path, got, 0o600))
			}
			want, err := os.ReadFile(path)
			require.NoError(t, err, "run: go test ./internal/systemd -update")
			require.Equal(t, string(want), string(got))
			// A rendered value can never start a new directive.
			for _, l := range strings.Split(strings.TrimSuffix(string(got), "\n"), "\n") {
				require.True(t, l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "[") || strings.Contains(l, "="), "unexpected line %q", l)
			}
		})
	}
}

func TestRenderDropInSemantics(t *testing.T) {
	cases := dropInCases()
	plain := string(RenderDropIn(cases[0].spec, cases[0].workDir, cases[0].logFile))
	require.True(t, strings.HasPrefix(plain, DropInHeader+"\n[Service]\n"))
	require.Contains(t, plain, "Type=simple\n")
	require.Contains(t, plain, "WorkingDirectory=/etc/deyroute/backends/backhaul/main/de-1/wssmux\n")
	require.Contains(t, plain, "StandardOutput=append:/var/log/deyroute/tunnels/main.log\n")
	require.Contains(t, plain, "StandardError=append:/var/log/deyroute/tunnels/main.log\n")
	require.NotContains(t, plain, "User=")
	require.NotContains(t, plain, "Capabilit")
	require.NotContains(t, plain, "RestrictAddressFamilies")

	wg := string(RenderDropIn(cases[1].spec, cases[1].workDir, cases[1].logFile))
	for _, l := range []string{
		"Type=oneshot", "Restart=no", "RemainAfterExit=yes", "User=root", "Group=root",
		"CapabilityBoundingSet=", "CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN",
		"AmbientCapabilities=", "AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN",
		"RestrictAddressFamilies=", "RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK",
		"ExecStop=/usr/local/bin/deyroute wg down --tunnel main",
	} {
		require.Contains(t, strings.Split(wg, "\n"), l)
	}

	ww := string(RenderDropIn(cases[2].spec, cases[2].workDir, cases[2].logFile))
	require.Contains(t, ww, "WorkingDirectory=/etc/deyroute/backends/waterwall/game/nl-2/reverse-reality\n")
	require.Contains(t, ww, "# Waterwall maps writable+executable memory at start-up and is killed under MemoryDenyWriteExecute\nMemoryDenyWriteExecute=false\n")
	require.NotContains(t, ww, "/ignored/")

	// No log file and no work dir: the template defaults stay.
	bare := string(RenderDropIn(backend.UnitSpec{ExecStart: []string{"/bin/x"}}, "", ""))
	require.NotContains(t, bare, "StandardOutput")
	require.NotContains(t, bare, "WorkingDirectory")
}

func TestQuoteCommand(t *testing.T) {
	require.Equal(t, `/bin/x -c "a b" "" "q\"q" 5%% $$PATH \\ \; "it's" "l\nl"`,
		QuoteCommand([]string{"/bin/x", "-c", "a b", "", `q"q`, "5%", "$PATH", `\`, ";", "it's", "l\nl"}))
	require.Equal(t, `\x01 "\t"`, QuoteCommand([]string{"\x01", "\t"}))
	require.Equal(t, `$X`, escapeQuoted("$X", false))
}

func TestPermissiveValue(t *testing.T) {
	require.Equal(t, "false", PermissiveValue("MemoryDenyWriteExecute"))
	require.Equal(t, "false", PermissiveValue("ProtectSystem"))
	require.Equal(t, "", PermissiveValue("SystemCallFilter"))
	require.Equal(t, "", PermissiveValue("RestrictAddressFamilies"))
	require.Equal(t, "~", PermissiveValue("CapabilityBoundingSet"))
	// Every boolean hardening line of the template has a permissive value.
	for _, l := range HardeningLines {
		k, v, _ := strings.Cut(l, "=")
		if v == "true" || v == "strict" {
			require.Equal(t, "false", PermissiveValue(k), k)
		}
	}
}

func TestValidateUnitSpec(t *testing.T) {
	for _, c := range dropInCases()[:3] {
		require.NoError(t, ValidateUnitSpec(c.spec), c.name)
	}
	err := ValidateUnitSpec(dropInCases()[3].spec)
	require.Error(t, err)
	msg := err.Error()
	for _, want := range []string{"ExecStartPre", "Environment='bad key'", "ExtraCaps='not-a-cap'", "AddressFamilies='bogus'",
		"ReadWritePaths", "DropHardening='UnknownOpt'"} {
		require.Contains(t, msg, want)
	}
	require.True(t, deyerr.HasCode(err, deyerr.X034) || strings.Contains(msg, "DEY-X034"))

	bad := backend.UnitSpec{
		ExecStart:        []string{"relative/bin"},
		ExecStop:         [][]string{{"stop"}},
		Type:             "daemonish",
		RemainAfterExit:  true,
		WorkingDirectory: "rel",
	}
	msg = ValidateUnitSpec(bad).Error()
	for _, want := range []string{"ExecStart='relative/bin'", "ExecStop='stop'", "Type='daemonish'", "RemainAfterExit", "WorkingDirectory='rel'"} {
		require.Contains(t, msg, want)
	}
	require.Error(t, ValidateUnitSpec(backend.UnitSpec{}))
	require.Error(t, ValidateUnitSpec(backend.UnitSpec{ExecStart: []string{"/x"}, WorkingDirectory: "/a\nb"}))

	msg = ValidateUnitSpec(dropInCases()[5].spec).Error()
	for _, want := range []string{
		"DropHardening='User'", "DropHardening='ExecStart'", "DropHardening='StandardOutput'",
		"DropHardening='LockPersonality (reason missing)'", `WorkingDirectory='/srv/odd\'`, `Type='simple\'`,
	} {
		require.Contains(t, msg, want)
	}
	require.NotContains(t, msg, "MemoryDenyWriteExecute")
}

func TestSanitize(t *testing.T) {
	require.Equal(t, "plain", sanitize("plain"))
	require.Equal(t, "a_b", sanitize("a\nb"))
	require.Equal(t, "a_", sanitize(`a\`))
	require.Equal(t, `a\\`, sanitize(`a\\`))
	require.Equal(t, `a\\_`, sanitize(`a\\\`))
	require.Equal(t, "_", sanitize("\x7f"))
	require.True(t, continues(`x\`))
	require.False(t, continues(`x\\`))
	require.False(t, continues(""))
}

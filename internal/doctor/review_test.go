package doctor

// Tests added by the review: each one fails on the code before the fix.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
)

// The log directory is writable by the backend user: an entry swapped for
// a symlink (directory or file) after the walk must not let the root
// collector read files outside /var/log/deyroute.
func TestLogsNeverEscapeTheLogDirectory(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "etc")
	writeFile(t, root, "/etc/shadow", "root:$6$secrethash:19000:0:99999:7:::\n")
	logDir := filepath.Join(root, "var/log/deyroute")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(logDir, "tunnels")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "shadow"), filepath.Join(logDir, "x.log")))

	r, err := os.OpenRoot(logDir)
	require.NoError(t, err)
	defer r.Close()
	for _, rel := range []string{"tunnels/shadow", "x.log", "../../../etc/shadow"} {
		_, err := tailRooted(r, rel, 10, 1<<20)
		require.Error(t, err, rel)
	}
	c := &Collector{Root: root}
	for name, text := range c.Logs() {
		require.NotContains(t, text, "secrethash", name)
	}
}

func TestLogsSkipFIFOAndHardLinks(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "var/log/deyroute")
	require.NoError(t, os.MkdirAll(logDir, 0o755))
	require.NoError(t, syscall.Mkfifo(filepath.Join(logDir, "pipe.log"), 0o600))
	writeFile(t, root, "/secret.txt", "hardlinked secret\n")
	require.NoError(t, os.Link(filepath.Join(root, "secret.txt"), filepath.Join(logDir, "link.log")))

	r, err := os.OpenRoot(logDir)
	require.NoError(t, err)
	defer r.Close()
	done := make(chan error, 1)
	go func() {
		_, err := tailRooted(r, "pipe.log", 10, 1<<20)
		done <- err
	}()
	select {
	case err := <-done:
		require.ErrorContains(t, err, "not a regular file")
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO blocked the collector")
	}
	_, err = tailRooted(r, "link.log", 10, 1<<20)
	require.ErrorContains(t, err, "hard links")

	logs := (&Collector{Root: root}).Logs()
	require.NotContains(t, logs, "logs/pipe.log", "FIFOs are not walked")
	require.Contains(t, logs["logs/link.log"], "cannot read")
	require.NotContains(t, logs["logs/link.log"], "hardlinked secret")
}

func TestLogsTotalBudget(t *testing.T) {
	old := logTotalBudget
	logTotalBudget = 3000
	defer func() { logTotalBudget = old }()
	root := t.TempDir()
	line := strings.Repeat("x", 99) + "\n"
	for _, n := range []string{"a.log", "b.log", "c.log"} {
		writeFile(t, root, "/var/log/deyroute/"+n, strings.Repeat(line, 20)) // 2000 bytes each
	}
	logs := (&Collector{Root: root}).Logs()
	require.Len(t, logs["logs/a.log"], 2000)
	require.Len(t, logs["logs/b.log"], 1000, "the rest of the budget, whole lines only")
	require.Contains(t, logs["logs/c.log"], "budget is used up")
}

func TestCustomTunnelCertificates(t *testing.T) {
	root := t.TempDir()
	ca, err := tlsutil.NewCA("DEYROUTE CA", testNow.AddDate(-1, 0, 0))
	require.NoError(t, err)
	cert, _, err := ca.IssueTunnel("web", nil, []string{"example.org"}, testNow.AddDate(-3, 0, 3))
	require.NoError(t, err)
	writeFile(t, root, "/opt/certs/web.pem", string(cert))
	c := &Collector{Root: root, Now: func() time.Time { return testNow },
		TunnelCertFiles: map[string]string{"web": "/opt/certs/web.pem", "gone": "/opt/certs/missing.pem", "none": ""}}

	// No secrets directory at all: the custom certificate is still checked.
	s, certs := c.certsInfo()
	require.Contains(t, s, "cannot list /etc/deyroute/secrets")
	require.Contains(t, s, "/opt/certs/web.pem  kind=tunnel")
	require.Contains(t, s, "/opt/certs/missing.pem  cannot read")
	require.Len(t, certs, 1)
	require.Equal(t, "web", certs[0].Tunnel)

	fs := Run(Facts{Role: "hub", Now: testNow, Certs: certs})
	require.Equal(t, []string{RuleCertExpiry}, rulesOf(fs))
	require.Contains(t, fs[0].Fix, "--tunnel web")
}

// Section 13 acceptance: "doctor gives the right diagnosis in the node
// offline scenario" — an offline node is always reported.
func TestNodeOfflineScenario(t *testing.T) {
	// Node offline, no tunnel through it: R01 (idle variant).
	f := healthyHub()
	f.Status.Nodes = append(f.Status.Nodes, api.NodeInfo{ID: "nl-1", LastHeartbeat: testNow.Add(-2 * time.Hour)})
	fd := only(t, Run(f), RuleControlOffline, SevWarn)
	require.Contains(t, fd.Message, "Node nl-1 is offline")
	require.Contains(t, fd.Message, "2026-09-30 08:00 UTC")
	require.Contains(t, fd.Fix, "systemctl status deyroute-node")

	f.Status.Nodes[1].LastHeartbeat = time.Time{}
	require.Contains(t, only(t, Run(f), RuleControlOffline, SevWarn).Message, "never")

	// Node offline and its tunnel DOWN: one R02 saying both.
	f = healthyHub()
	f.Status.Nodes[0].Online = false
	f.Status.Tunnels[0].State = state.StateDown
	fd = only(t, Run(f), RuleIPBlocked, SevError)
	require.Contains(t, fd.Message, "offline on the control channel too")
	require.Contains(t, fd.Fix, "deyroute tunnel backup add main")

	// With a backup node still online the classic R02 text is used, and
	// the offline primary is not reported twice.
	f.Status.Tunnels[0].Nodes = []string{"de-1", "nl-1"}
	f.Status.Nodes = append(f.Status.Nodes, api.NodeInfo{ID: "nl-1", Online: true})
	fd = only(t, Run(f), RuleIPBlocked, SevError)
	require.Contains(t, fd.Message, "probably blocked")

	// Node side: a DOWN tunnel never uses the hub-only offline variant.
	n := healthyNode()
	n.Status.Tunnels = []api.TunnelInfo{{ID: "main", Enabled: true, State: state.StateDown, Nodes: []string{"de-1"}}}
	require.Contains(t, only(t, Run(n), RuleIPBlocked, SevError).Message, "probably blocked")
}

// Findings and the summary carry text from other programs and from nodes
// (process names, warnings, versions): terminal escape sequences and
// bidi overrides must not reach the owner's terminal.
func TestTerminalInjectionIsNeutralised(t *testing.T) {
	evil := "nginx\x1b]0;pwned\x07\x1b[2J\u202eevil\u0085x"
	f := healthyHub()
	f.PortConflicts = []string{"443/tcp used by " + evil}
	fs := Run(f)
	require.Len(t, fs, 1)
	for _, r := range fs[0].Message {
		require.False(t, r == 0x1b || r == 0x07 || r == 0x202e || r == 0x85, "control rune %q in %q", r, fs[0].Message)
	}

	st := api.Status{Role: "node\x1b[31m", Version: "1.0\x1b[0m",
		NodeSelf: &api.NodeSelf{ID: "de-1", HubAddr: "5.6.7.8\x1b[2J"}}
	raw := []api.DoctorFinding{{Rule: "R05\x1b[1m", Severity: SevWarn, Message: evil, Fix: "fix " + evil}}
	for _, color := range []bool{false, true} {
		out := StripANSI(Summary(raw, st, true, color))
		require.NotContains(t, out, "\x1b", "color=%v", color)
		require.NotContains(t, out, "\x07")
		require.NotContains(t, out, "\u202e")
		require.NotContains(t, Summary(raw, st, true, false), "\x1b")
	}
	require.NotContains(t, FormatFindings(raw), "\x1b")
}

func TestSectionPartsSanitisesNodeNames(t *testing.T) {
	parts := SectionParts(map[string]string{
		"../../etc/cron.d/x": "a",
		"os":                 "b",
		"os.txt":             "c",
		"logs/a\x1bb.log":    "d",
		"":                   "e",
		`..\..\win`:          "f",
	}, NodePrefix("de-1"))
	require.Equal(t, map[string][]byte{
		"node-de-1/_/_/etc/cron.d/x.txt": []byte("a"),
		"node-de-1/os.txt":               []byte("b"),
		"node-de-1/os-2.txt":             []byte("c"),
		"node-de-1/logs/a_b.log":         []byte("d"),
		"node-de-1/unnamed.txt":          []byte("e"),
		"node-de-1/_/_/win.txt":          []byte("f"),
	}, parts)
	// A hostile node can no longer make the whole bundle fail.
	p, err := WriteBundle(t.TempDir(), testNow, parts, nil, "")
	require.NoError(t, err)
	m := readBundle(t, p)
	require.Contains(t, m, "deyroute-doctor-20260930T100000Z/node-de-1/_/_/etc/cron.d/x.txt")
}

func TestWriteBundleRefusesDuplicateCleanNames(t *testing.T) {
	_, err := WriteBundle(t.TempDir(), testNow, map[string][]byte{"a//b.txt": []byte("1"), "a/b.txt": []byte("2")}, nil, "")
	require.True(t, deyerr.HasCode(err, deyerr.X000))
}

func TestWriteBundleFileExplicitPath(t *testing.T) {
	out := filepath.Join(t.TempDir(), "support.tgz")
	p, err := WriteBundleFile(out, testNow, map[string][]byte{"os.txt": []byte("x\n")}, nil, "OK\n")
	require.NoError(t, err)
	require.Equal(t, out, p)
	st, err := os.Stat(out)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	require.Contains(t, readBundle(t, out), "deyroute-doctor-20260930T100000Z/os.txt")
}

// A part larger than the member limit is cut (after redaction, at a line
// boundary) so the final check scans every byte that is written; before
// the fix the tail of such a member was never verified.
func TestOversizedPartIsCappedAndFullyVerified(t *testing.T) {
	old := maxMemberBytes
	maxMemberBytes = 64 << 10
	defer func() { maxMemberBytes = old }()
	var b bytes.Buffer
	line := strings.Repeat("a", 1023) + "\n"
	for b.Len() <= maxMemberBytes+4096 {
		b.WriteString(line)
	}
	b.WriteString("final line\n")
	p, err := WriteBundle(t.TempDir(), testNow, map[string][]byte{"big.log": b.Bytes()}, nil, "")
	require.NoError(t, err)
	got := readBundle(t, p)["deyroute-doctor-20260930T100000Z/big.log"]
	require.LessOrEqual(t, len(got), maxMemberBytes+len(truncNote)+32)
	require.True(t, strings.HasPrefix(got, "[deyroute doctor: truncated"))
	require.True(t, strings.HasSuffix(got, "final line\n"))
	for _, l := range strings.Split(strings.TrimSuffix(got, "\n"), "\n")[1:] {
		require.True(t, l == "final line" || l == strings.Repeat("a", 1023), "partial line kept")
	}

	// verifyArchive refuses a member it could not scan completely.
	big := bytes.Repeat([]byte("x"), maxMemberBytes+4096)
	archive, err := buildArchive("top", testNow, map[string][]byte{"big": big})
	require.NoError(t, err)
	require.True(t, deyerr.HasCode(verifyArchive(archive), deyerr.X060))

	// A single line without a newline is dropped entirely.
	require.Equal(t, fmt.Sprintf(truncNote, maxMemberBytes+1), string(capMember(bytes.Repeat([]byte("y"), maxMemberBytes+1))))
}

func TestCollectWithCustomCertsEndToEnd(t *testing.T) {
	c, _, root := newCollector(t)
	ca, err := tlsutil.NewCA("DEYROUTE CA", testNow.AddDate(-1, 0, 0))
	require.NoError(t, err)
	cert, _, err := ca.IssueTunnel("web", nil, []string{"example.net"}, testNow.AddDate(-4, 0, 0)) // expired
	require.NoError(t, err)
	writeFile(t, root, "/srv/web.crt", string(cert))
	c.TunnelCertFiles = map[string]string{"web": "/srv/web.crt"}
	col := c.Collect(context.Background())
	f := Facts{Role: "hub", Now: testNow}
	col.Apply(&f)
	var expired bool
	for _, fd := range Run(f) {
		if fd.Rule == RuleCertExpiry && strings.Contains(fd.Message, "/srv/web.crt expired") {
			expired = true
		}
	}
	require.True(t, expired)
}

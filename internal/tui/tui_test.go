package tui

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/state"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetectCaps(t *testing.T) {
	c := DetectCaps(env(map[string]string{"TERM": "xterm-256color", "LANG": "en_US.UTF-8"}))
	require.True(t, c.Unicode && c.Color, "utf8 xterm: %+v", c)
	c = DetectCaps(env(map[string]string{"TERM": "dumb", "LANG": "en_US.UTF-8"}))
	require.False(t, c.Unicode || c.Color, "dumb: %+v", c)
	c = DetectCaps(env(map[string]string{"TERM": "xterm", "LANG": "C"}))
	require.False(t, c.Unicode, "C locale must be ASCII")
	c = DetectCaps(env(map[string]string{"TERM": "xterm", "LC_ALL": "en_US.utf8", "NO_COLOR": "1"}))
	require.True(t, c.Unicode)
	require.False(t, c.Color)
	c = DetectCaps(env(map[string]string{"TERM": "xterm", "LANG": "en_US.UTF-8", "DEYROUTE_ASCII": "1"}))
	require.False(t, c.Unicode)
	require.True(t, Caps{Width: 80}.Narrow())
	require.False(t, Caps{Width: 100}.Narrow())
	require.False(t, Caps{}.Narrow())
}

func TestASCIIBannerIsASCII(t *testing.T) {
	b := Banner(Caps{Unicode: false, Width: 80}, BannerStatus{Role: "hub", Name: "ir-1", PublicIP: "5.6.7.8", Nodes: 2, TunnelsUp: 1})
	for _, r := range b {
		require.LessOrEqual(t, r, rune(127), "non-ASCII rune in ASCII banner:\n%s", b)
	}
	// At 80 columns the product name is shortened; the counts stay.
	require.Contains(t, b, " DEYROUTE dev - Hub: ir-1 (5.6.7.8) - Mode: Simple - 2 nodes - 1 tunnel UP")
	require.Contains(t, b, "#")
	for _, l := range strings.Split(b, "\n") {
		require.LessOrEqual(t, len(l), 80, "line wider than 80 cols: %q", l)
	}
}

func TestUnicodeBannerStatusLine(t *testing.T) {
	b := Banner(Caps{Unicode: true}, BannerStatus{Role: "hub", Name: "ir-1", PublicIP: "5.6.7.8", Nodes: 2, TunnelsUp: 1})
	require.Contains(t, b, "Hub: ir-1 (5.6.7.8)  ·  Mode: Simple  ·  2 nodes  ·  1 tunnel UP")
	require.Contains(t, b, "██████╗ ███████╗")
	n := Banner(Caps{Unicode: true}, BannerStatus{Role: "node", Name: "de-1", HubAddr: "5.6.7.8:44433", Advanced: true})
	require.Contains(t, n, "Node: de-1 → hub 5.6.7.8:44433  ·  Mode: Advanced")
	require.Contains(t, Banner(Caps{Unicode: true}, BannerStatus{}), "not set up")
	one := Banner(Caps{Unicode: true}, BannerStatus{Role: "hub", Name: "ir-1", PublicIP: "5.6.7.8", Nodes: 1, TunnelsUp: 3})
	require.Contains(t, one, "1 node  ·  3 tunnels UP")
	// Narrower: separators, then the mode go before the counts.
	narrow := Banner(Caps{Unicode: true, Width: 60}, BannerStatus{Role: "hub", Name: "ir-1", PublicIP: "5.6.7.8", Nodes: 2, TunnelsUp: 1})
	require.Contains(t, narrow, "\n DEYROUTE dev · Hub: ir-1 (5.6.7.8) · 2 nodes · 1 tunnel UP")
}

func TestMenuFixedNumbers(t *testing.T) {
	out := RenderMenu(Caps{Unicode: true}, MainMenu(), false)
	for _, want := range []string{" 1) Dashboard (live)", " 2) Tunnels", "10) Backup & Restore", "12) Settings", " 0) Exit"} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "thresholds *")
	adv := RenderMenu(Caps{Unicode: true}, MainMenu(), true)
	require.Contains(t, adv, "thresholds *")
	require.Contains(t, adv, "view fingerprints *")
}

func TestMenuASCII80(t *testing.T) {
	out := RenderMenu(Caps{Unicode: false, Width: 80}, MainMenu(), true)
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		require.LessOrEqual(t, len(l), 80, "too wide: %q", l)
		for _, r := range l {
			require.LessOrEqual(t, r, rune(127), "non-ASCII in %q", l)
		}
	}
}

// deyroute menu --once: the first screen renders without Init, a TTY or a daemon.
func TestFirstScreenWithoutDaemon(t *testing.T) {
	v := NewModel(Options{Caps: Caps{Unicode: false, Width: 80}}).View()
	require.Contains(t, v, "x DEY-X003  Daemon not running")
	require.Contains(t, v, "Fix:  systemctl start deyroute-hub")
	require.Contains(t, v, " 1) Dashboard (live)")
	require.Contains(t, v, "Choice: ")
	for _, r := range v {
		require.LessOrEqual(t, r, rune(127))
	}
	for _, l := range strings.Split(v, "\n") {
		require.LessOrEqual(t, len(l), 80, "too wide: %q", l)
	}
	// A dial error is shown instead of the generic one.
	v = NewModel(Options{Caps: Caps{Unicode: true}, LocalErr: deyerr.New(deyerr.X006, deyerr.Params{"status": 500})}).View()
	require.Contains(t, v, "✖ DEY-X006")
}

func TestNoDaemonActionsShowX003(t *testing.T) {
	h := newHarness(t, Options{Caps: Caps{Unicode: true}})
	h.choose("1")
	h.must("✖ DEY-X003  Daemon not running", "Why:  ", "Fix:  systemctl start deyroute-hub", "Log:  ")
	h.mustNot("goroutine", ".go:")
}

func TestMainMenuKeys(t *testing.T) {
	h := newHarness(t, Options{Caps: Caps{Unicode: true}})
	h.press("9", "9", "enter")
	h.must("Invalid choice: 99")
	h.press("x")
	h.press("?")
	h.must("Help", "Type the number of an item")
	h.press("enter")
	h.must("Choice: ")
	h.press("1", "backspace", "2", "esc") // Esc clears the typed number first
	require.Equal(t, 1, h.depth())
	h.must("Choice: \n")
	h.press("r")
	h.choose("0")
	require.True(t, h.quit)
}

func TestQuitWithQ(t *testing.T) {
	h := newHarness(t, Options{Caps: Caps{Unicode: true}})
	h.press("q")
	require.True(t, h.quit)
	require.Empty(t, h.view())
}

// ---- dashboard

func dashApp(c Caps) *app {
	return NewModel(Options{Caps: c, Now: func() time.Time { return testNow }, Location: time.UTC}).a
}

func TestDashboardGolden(t *testing.T) {
	st := sampleStatus()
	uni := renderStatus(dashApp(Caps{Unicode: true, Width: 120}), st, testNow)
	golden(t, "dashboard_unicode_120.golden", uni)
	// The exact lines of the section 6 sample.
	for _, want := range []string{
		" TUNNELS\n",
		"  #  NAME            NODE (active)   TRANSPORT          STATE   RTT    UP-TIME    PORTS\n",
		"  1  Main 443/2053   de-1 Germany 1  backhaul/wssmux    ● UP    41ms   3d 04:12   443,2053\n",
		"◐ DEGR  188ms  00:03:10   27015/udp\n",
		"  de-1  Germany 1      1.2.3.4   ● online   ctl 39ms   v1.0.0   cpu 3%  ram 121MB\n",
		"  nl-1  Netherlands 1  9.8.7.6   ● online   ctl 44ms   v1.0.0   cpu 1%  ram  98MB\n",
		"  12:41:03  main   switch   backhaul/tcpmux → backhaul/wssmux (failback, primary healthy 5m)\n",
		"  12:35:58  main   down     backhaul/tcpmux probe failed 3x (timeout)\n",
		"  ! main: TLS certificate of main expires in 12 days\n",
		"  ! games: rung wireguard/kernel skipped: UDP blocked\n",
	} {
		require.Contains(t, uni, want)
	}

	asc := renderStatus(dashApp(Caps{Unicode: false, Width: 80}), st, testNow)
	asc = asciiOnly(asc)
	golden(t, "dashboard_ascii_80.golden", asc)
	require.NotContains(t, asc, "RTT")
	require.NotContains(t, asc, "UP-TIME")
	require.NotContains(t, asc, "ctl 39ms")
	require.Contains(t, asc, "* UP")
	require.Contains(t, asc, "~ DEGR")
	for _, l := range strings.Split(asc, "\n") {
		require.LessOrEqual(t, len(l), 80, "too wide: %q", l)
	}
}

func TestDashboardNarrowDropsColumns(t *testing.T) {
	st := sampleStatus()
	wide := renderStatus(dashApp(Caps{Unicode: true, Width: 100}), st, testNow)
	require.Contains(t, wide, "RTT")
	require.Contains(t, wide, "UP-TIME")
	narrow := renderStatus(dashApp(Caps{Unicode: true, Width: 99}), st, testNow)
	require.NotContains(t, narrow, "RTT")
	require.NotContains(t, narrow, "3d 04:12")
	require.Contains(t, narrow, "● UP")
	// Very narrow: columns shrink and lines are cut, never wrapped.
	tiny := renderStatus(dashApp(Caps{Unicode: true, Width: 60}), st, testNow)
	for _, l := range strings.Split(tiny, "\n") {
		require.LessOrEqual(t, width(l), 60, "too wide: %q", l)
	}
}

func TestStateWordsAndColors(t *testing.T) {
	a := dashApp(Caps{Unicode: true, Color: true})
	cases := []struct {
		t    api.TunnelInfo
		word string
		col  string
	}{
		{api.TunnelInfo{Enabled: true, State: state.StateUp}, "UP", colGreen},
		{api.TunnelInfo{Enabled: true, State: state.StateDegraded}, "DEGR", colYellow},
		{api.TunnelInfo{Enabled: true, State: state.StateSwitching}, "SWITCHING", colBlue},
		{api.TunnelInfo{Enabled: true, State: state.StateStarting}, "STARTING", colBlue},
		{api.TunnelInfo{Enabled: true, State: state.StateDown}, "DOWN", colRed},
		{api.TunnelInfo{Enabled: false, State: state.StateUp}, "DISABLED", colGray},
		{api.TunnelInfo{Enabled: true, Paused: true, State: state.StateUp}, "PAUSED", colGray},
		{api.TunnelInfo{Enabled: true, State: state.StateInit}, "INIT", colGray},
		{api.TunnelInfo{Enabled: true, State: "ODD"}, "ODD", colGray},
	}
	for _, c := range cases {
		_, w, col := a.stateLook(c.t)
		require.Equal(t, c.word, w)
		require.Equal(t, c.col, col)
	}
	// Colors never replace the word.
	out := renderTunnelTable(a, sampleTunnels(), testNow)
	require.Contains(t, stripANSI(out), "● UP")
	require.Contains(t, stripANSI(out), "◐ DEGR")
}

func TestDashboardRefreshes(t *testing.T) {
	var calls atomic.Int32
	stub := &apitest.Stub{StatusFn: func(context.Context) (api.Status, error) {
		calls.Add(1)
		return sampleStatus(), nil
	}}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: stub})
	require.EqualValues(t, 1, calls.Load()) // banner load
	h.must("Hub: ir-1 (5.6.7.8)", "2 nodes", "1 tunnel UP")
	h.choose("1")
	h.must("Dashboard (live)", "TUNNELS", "LAST EVENTS", "Updated 12:45:00")
	require.EqualValues(t, 2, calls.Load())
	h.mu.Lock()
	require.Len(t, h.ticks, 1)
	tick := h.ticks[0]
	h.mu.Unlock()
	require.Equal(t, 2*time.Second, h.m.(Model).a.opts.Refresh)
	h.handle(tick) // the 2 s timer fires
	h.settle()
	require.EqualValues(t, 3, calls.Load())
	h.handle(tick) // a stale tick is ignored
	h.settle()
	require.EqualValues(t, 3, calls.Load())
	h.press("r")
	require.EqualValues(t, 4, calls.Load())
	h.press("?")
	h.must("refreshes every 2 seconds")
	h.press("q", "q")
	require.Equal(t, 1, h.depth())
}

func TestDashboardNodeRole(t *testing.T) {
	st := api.Status{Role: "node", NodeSelf: &api.NodeSelf{ID: "de-1", HubAddr: "5.6.7.8:44433", Connected: true,
		LastContact: testNow, HubVersion: "1.0.0", Units: []string{"deyroute-tun@main.de-1.backhaul-wssmux.service"}}}
	out := renderStatus(dashApp(Caps{Unicode: true, Width: 120}), st, testNow)
	require.Contains(t, out, " NODE\n")
	require.Contains(t, out, "de-1  hub 5.6.7.8:44433  ● connected  last contact 12:45:00  v1.0.0")
	require.Contains(t, out, "units: deyroute-tun@main.de-1.backhaul-wssmux.service")
	require.NotContains(t, out, "NODES")
	require.NotContains(t, out, "via front")
	// A node behind the front shows the front domain and port plus a marker.
	st.NodeSelf.HubAddr, st.NodeSelf.Front = "front.example.com:2053", true
	out = renderStatus(dashApp(Caps{Unicode: true, Width: 120}), st, testNow)
	require.Contains(t, out, "de-1  hub front.example.com:2053 (via front)  ● connected")
	empty := renderStatus(dashApp(Caps{Unicode: true}), api.Status{Role: "hub"}, testNow)
	require.Contains(t, empty, "No tunnels yet")
	require.Contains(t, empty, "No nodes yet")
	require.Contains(t, empty, "No events yet")
}

// ---- errors

func TestErrorRendering(t *testing.T) {
	a := dashApp(Caps{Unicode: true, Color: false})
	e := deyerr.New(deyerr.P012, deyerr.Params{"port": "443/tcp", "process": "nginx (pid 1234)", "addr": "0.0.0.0:443"}).WithLog("/var/log/deyroute/hub.log")
	out := a.errBlock(e)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 4)
	require.True(t, strings.HasPrefix(lines[0], " ✖ DEY-P012  "))
	require.Contains(t, lines[1], "Why:")
	require.Contains(t, lines[2], "Fix:")
	require.Equal(t, "   Log:  /var/log/deyroute/hub.log (search DEY-P012)", lines[3])
	// Plain errors get a code and keep their text, never a stack trace.
	plain := a.errBlock(context.DeadlineExceeded)
	require.Contains(t, plain, "DEY-X000")
	require.Contains(t, plain, "| context deadline exceeded")
	// Colored: the first line is red.
	ac := dashApp(Caps{Unicode: false, Color: true})
	ac.caps.Color = true
	require.Contains(t, ac.errBlock(e), "x DEY-P012")
	require.Empty(t, a.errBlock(nil))
	require.Equal(t, " "+i18n.T(i18n.TUIRequired)+"\n", a.errBlock(uiErr(i18n.TUIRequired)))
}

func TestHelpers(t *testing.T) {
	require.Equal(t, "3d 04:12", upTime(3*24*time.Hour+4*time.Hour+12*time.Minute))
	require.Equal(t, "00:03:10", upTime(3*time.Minute+10*time.Second))
	require.Equal(t, "00:00:00", upTime(-time.Second))
	require.Equal(t, "ab…", trunc("abcdef", 3, "…"))
	require.Equal(t, "", trunc("abc", 0, "…"))
	require.Equal(t, "abc", trunc("abc", 5, "…"))
	require.Equal(t, "a?b", asciiOnly("a€b"))
	require.Equal(t, "* ~ o OK x ...", asciiOnly("● ◐ ○ ✔ ✖ …"))
	require.Equal(t, " 1) x", numLine(1, "x"))
	require.Equal(t, "10) x", numLine(10, "x"))
	require.Equal(t, "   ", padLeft("", 3))
	require.Equal(t, "plain", stripANSI("\x1b[31mplain\x1b[0m"))
	y, err := parseYes("Y")
	require.NoError(t, err)
	require.True(t, y)
	_, err = parseYes("maybe")
	require.Error(t, err)
	require.Error(t, checkInt(1)("0", nil))
	require.NoError(t, checkInt(1)("5", nil))
	require.NoError(t, optionalInt(1)("", nil))
	require.Error(t, checkOneOf("a", "b")("c", nil))
	require.NoError(t, checkTarget("10.0.0.5:8443", nil))
	require.True(t, deyerr.HasCode(checkTarget("nope", nil), deyerr.C004))
	require.Equal(t, "443,2053,27015/udp", portsText([]api.PortMapDTO{{Listen: 443, Proto: "tcp"}, {Listen: 2053, Proto: "tcp"}, {Listen: 27015, Proto: "udp"}}))
	require.Equal(t, "yes", yesNo(true))
	require.Equal(t, "no", yesNo(false))
}

// ---- navigation to every menu item

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (c *callLog) add(s string) {
	c.mu.Lock()
	c.calls = append(c.calls, s)
	c.mu.Unlock()
}

func (c *callLog) has(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.calls {
		if x == s {
			return true
		}
	}
	return false
}

// fullStub answers every read-only call with sample data.
func fullStub(log *callLog, uiMode string) *apitest.Stub {
	st := sampleStatus()
	st.Hub.UIMode = uiMode
	yes := true
	return &apitest.Stub{
		StatusFn:     func(context.Context) (api.Status, error) { log.add("Status"); return st, nil },
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) { log.add("TunnelList"); return sampleTunnels(), nil },
		NodeListFn:   func(context.Context) ([]api.NodeInfo, error) { log.add("NodeList"); return sampleNodes(), nil },
		TunnelShowFn: func(_ context.Context, id string) (api.TunnelDetail, error) {
			log.add("TunnelShow " + id)
			t := sampleTunnels()[0]
			return api.TunnelDetail{TunnelInfo: t, Failover: defaultFailover(), TLSMode: "auto",
				Rungs: []api.RungStatus{{Node: "de-1", Transport: "backhaul/wssmux", Active: true, Warm: true},
					{Node: "de-1", Transport: "backhaul/tcpmux", Warm: true},
					{Node: "nl-1", Transport: "hysteria2/udp", Skipped: "UDP blocked"},
					{Node: "nl-1", Transport: "rathole/noise", Quarantine: testNow.Add(time.Minute)}},
				Events: st.Events}, nil
		},
		TransportListFn: func(context.Context) ([]api.TransportInfo, error) {
			return []api.TransportInfo{
				{ID: "backhaul/wssmux"}, {ID: "backhaul/tcpmux"}, {ID: "direct/native"},
				{ID: "direct/haproxy", ClientIPPreserved: true}, {ID: "xray/reality"}, {ID: "hysteria2/udp"},
			}, nil
		},
		LadderListFn: func(context.Context) ([]api.Ladder, error) {
			return []api.Ladder{{Name: "default", Rungs: []string{"backhaul/wssmux", "backhaul/tcpmux", "direct/native"}, Builtin: true},
				{Name: "fast", Rungs: []string{"backhaul/tcpmux", "direct/native"}}}, nil
		},
		OptimizeStatusFn: func(context.Context) (api.OptimizeStatus, error) {
			return api.OptimizeStatus{Profile: "balanced", BBRAvailable: true, BBRActive: true,
				Applied: map[string]string{"net.core.rmem_max": "16777216", "net.ipv4.tcp_congestion_control": "bbr"}}, nil
		},
		SecurityTLSShowFn: func(context.Context, string) ([]api.CertInfo, error) {
			return []api.CertInfo{{Kind: "ca", Subject: "deyroute CA", NotAfter: testNow.AddDate(10, 0, 0), DaysLeft: 3650, Fingerprint: "sha256:ca"},
				{Kind: "tunnel", Tunnel: "main", Mode: "auto", Subject: "main", NotAfter: testNow.AddDate(0, 0, 12), DaysLeft: 12, Warning: "renew soon", Fingerprint: "sha256:t1"}}, nil
		},
		SecurityAuditFn: func(context.Context) (api.AuditReport, error) {
			return api.AuditReport{Items: []api.AuditItem{{Check: "secrets", Severity: "ok", Message: "0700"}, {Check: "tokens", Severity: "warn", Message: "old join token"}}}, nil
		},
		SecurityFirewallFn: func(_ context.Context, action string) (api.FirewallInfo, error) {
			log.add("Firewall " + action)
			return api.FirewallInfo{Managed: true, Detected: []string{"nftables", "ufw"}, Ruleset: "table inet deyroute {\n}", Suggested: []string{"ufw allow 443/tcp"}}, nil
		},
		UpdateCheckFn: func(context.Context) (api.UpdateInfo, error) {
			return api.UpdateInfo{Current: "1.0.0", Latest: "1.0.1", Available: true, Changelog: "- fixes"}, nil
		},
		UpdateManifestFn: func(context.Context) (api.ManifestInfo, error) {
			return api.ManifestInfo{Source: "embedded", Versions: map[string]string{"backhaul": "0.6.5", "xray": "25.1.1"}}, nil
		},
		NodeJoinCommandFn: func(_ context.Context, ttl time.Duration) (api.JoinCommand, error) {
			return api.JoinCommand{Command: "bash <(curl -fsSL https://example.invalid/install.sh) join 'dey://TOKEN@5.6.7.8:44433#sha256:ab'",
				ExpiresAt: testNow.Add(ttl)}, nil
		},
		NodeTestFn: func(_ context.Context, id string) (api.NodeTestResult, error) {
			return api.NodeTestResult{Node: id, Online: true, ControlRTTms: 39, UDPOK: true, UDPRTTms: 40, SysInfo: map[string]string{"kernel": "6.1", "arch": "amd64"}}, nil
		},
		DiagProbeFn: func(context.Context, string, bool) ([]api.ProbeReport, error) {
			return []api.ProbeReport{{Port: 443, Proto: "tcp", Kind: "tls", OK: true, RTTms: 41}, {Port: 2053, Proto: "tcp", Kind: "auto", Error: "timeout"}}, nil
		},
		PortCheckFn: func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
			return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true, FirewallOpen: true, FirewallName: "nftables",
				Node: "de-1", NodeReachable: &yes, NodeRTTms: 44}, nil
		},
	}
}

// Every main-menu item and every sub-item opens and closes cleanly with the
// keys of section 6 (numbers + Enter, Esc back), in both UI modes.
func TestNavigateEveryMenuItem(t *testing.T) {
	for _, mode := range []string{"simple", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			log := &callLog{}
			h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 120}, Local: fullStub(log, mode)})
			require.Equal(t, mode == "advanced", h.m.(Model).a.advanced)
			for n := 1; n <= 12; n++ {
				h.choose(itoa(n))
				require.Equal(t, 2, h.depth(), "item %d", n)
				title := i18n.T(MainMenu()[n-1].Title)
				h.must(" " + title + "\n")
				h.press("?")
				h.must("Help")
				h.press("esc")
				if l, ok := h.m.(Model).a.top().(*listScreen); ok {
					items := len(l.choices(h.m.(Model).a))
					for i := 1; i <= items; i++ {
						h.choose(itoa(i))
						require.GreaterOrEqual(t, h.depth(), 2)
						_ = h.view()
						for h.depth() > 2 {
							h.press("esc")
						}
					}
				}
				h.press("esc")
				require.Equal(t, 1, h.depth(), "item %d", n)
			}
		})
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// ---- ASCII / 80 columns (S24)

func TestASCIIScreensAt80(t *testing.T) {
	log := &callLog{}
	h := newHarness(t, Options{Caps: Caps{Unicode: false, Width: 80}, Local: fullStub(log, "advanced")})
	check := func() {
		t.Helper()
		v := h.view()
		for _, r := range v {
			require.LessOrEqual(t, r, rune(127), "non-ASCII rune %q in:\n%s", r, v)
		}
	}
	check()
	for _, path := range [][]string{{"1"}, {"2"}, {"3"}, {"3", "2"}, {"4", "3"}, {"5", "2"}, {"6"}, {"7"}, {"8", "2"}, {"11", "1"}} {
		for _, p := range path {
			h.choose(p)
		}
		check()
		for h.depth() > 1 {
			h.press("esc")
		}
	}
	h.choose("1")
	for _, l := range strings.Split(h.view(), "\n") {
		require.LessOrEqual(t, len(l), 80, "too wide: %q", l)
	}
}

func (c *callLog) count(s string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, x := range c.calls {
		if x == s {
			n++
		}
	}
	return n
}

package tui

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
)

// ---- hub move from the menu (section 5) and the node menu

func nodeStatus() api.Status {
	return api.Status{Schema: 1, Role: "node", Version: "1.0.0", GeneratedAt: testNow,
		NodeSelf: &api.NodeSelf{ID: "de-1", HubAddr: "5.6.7.8:44433", Connected: true, LastContact: testNow, HubVersion: "1.0.0", Compatible: true}}
}

// nodeStub answers like the node agent: Status, Logs and NodeSetHub; the
// tunnel list needs the hub (DEY-X009).
func nodeStub(log *callLog) *apitest.Stub {
	st := nodeStatus()
	return &apitest.Stub{
		StatusFn: func(context.Context) (api.Status, error) { return st, nil },
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) {
			return nil, deyerr.New(deyerr.X009, deyerr.Params{"role": "node", "need": "hub"})
		},
		LogsFn: func(_ context.Context, q api.LogQuery, emit func(api.LogLine) error) error {
			log.add("Logs " + q.Target)
			return emit(api.LogLine{Source: "node", Line: "agent started"})
		},
	}
}

func nodeOptions(log *callLog) Options {
	return Options{
		Caps: Caps{Unicode: true, Width: 100}, Local: nodeStub(log), Service: "deyroute-node",
		Status: BannerStatus{Role: "node", Name: "de-1", HubAddr: "5.6.7.8:44433"},
		Doctor: func(context.Context) (string, string, error) { return "DEYROUTE doctor: node de-1", "", nil },
	}
}

func TestNodeMenuMarksHubOnlyItems(t *testing.T) {
	out := RenderMenu(Caps{Unicode: true}, NodeMenu(), false)
	for _, want := range []string{
		" 1) Dashboard (live)\n",
		" 2) Tunnels        (hub only)\n",
		" 5) Failover       (hub only)\n",
		" 6) Diagnostics    logs · doctor\n",
		"10) Backup & Restore backup · restore · set hub address\n",
		"11) Update         (hub only)\n",
		"12) Settings       uninstall\n",
		" 0) Exit\n",
	} {
		require.Contains(t, out, want)
	}
	// Numbers stay those of section 6; Advanced adds nothing on a node.
	require.Equal(t, out, RenderMenu(Caps{Unicode: true}, NodeMenu(), true))
	for i, it := range NodeMenu() {
		require.Equal(t, MainMenu()[i].Num, it.Num)
	}
	asc := RenderMenu(Caps{Unicode: false, Width: 80}, NodeMenu(), false)
	require.Contains(t, asc, " 6) Diagnostics    logs - doctor")
	for _, l := range strings.Split(strings.TrimRight(asc, "\n"), "\n") {
		require.LessOrEqual(t, len(l), 80, "too wide: %q", l)
		for _, r := range l {
			require.LessOrEqual(t, r, rune(127), "non-ASCII in %q", l)
		}
	}
	// The hub's menu is unchanged.
	require.Contains(t, RenderMenu(Caps{Unicode: true}, MainMenu(), false), " 2) Tunnels        add / edit")
	it, ok := mainItem(MainMenu(), 7)
	require.True(t, ok)
	require.True(t, it.HubOnly)
	_, ok = mainItem(MainMenu(), 13)
	require.False(t, ok)
}

func TestNodeMenuScreens(t *testing.T) {
	log := &callLog{}
	var mu sync.Mutex
	running := true
	var saved []string
	o := nodeOptions(log)
	o.SetHub = func(_ context.Context, addr string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		saved = append(saved, addr)
		return running, nil
	}
	h := newHarness(t, o)
	h.must(" 2) Tunnels        (hub only)\n", "12) Settings       uninstall\n")
	h.mustNot("add / edit")
	// A hub-only item says where it is done, without an error block.
	h.choose("2")
	require.Equal(t, 1, h.depth())
	h.must("Tunnels is managed on the hub (5.6.7.8:44433): open the menu there.", "The hub applies every change to this node.")
	h.mustNot("DEY-X009")
	h.choose("11")
	require.Equal(t, 1, h.depth())
	h.must("Update is managed on the hub")
	h.press("?")
	h.must("items marked (hub only) are managed in the hub's menu")
	h.press("esc")
	h.choose("1")
	h.mustNot("Update is managed on the hub") // the note goes with the next choice
	h.must("de-1  hub 5.6.7.8:44433")
	h.press("esc")

	// Diagnostics: logs and doctor only.
	h.choose("6")
	h.must(" 1) Logs\n", " 2) Doctor\n")
	h.mustNot("Port check", "Tunnel test", "Speed test")
	h.choose("1")
	h.must("1) node (deyroute-node service)")
	h.mustNot("DEY-X009")
	h.choose("1")
	h.must("agent started")
	require.True(t, log.has("Logs node"))
	h.press("esc", "esc", "esc")

	// Settings: uninstall only (UI mode and language are hub settings).
	h.choose("12")
	h.must(" 1) Uninstall\n")
	h.mustNot("UI mode", "Language")
	h.press("esc")

	// Backup & Restore -> Set hub address, through the running agent.
	h.choose("10")
	h.must(" 3) Set hub address\n")
	h.mustNot("Announce hub move")
	h.choose("3")
	h.must("This node connects to hub 5.6.7.8:44433.", "Hub address (IP:port): ")
	h.typeLine("")
	h.must("A value is required.")
	h.typeLine("nope")
	h.must("Enter the address as IP:port")
	h.press("ctrl+u")
	h.typeLine("5.6.7.9:44433")
	h.must("This node now connects to hub 5.6.7.9:44433.", "Node: de-1 -> hub 5.6.7.9:44433")
	h.press("esc")
	// With the agent stopped the address is saved for its next start.
	mu.Lock()
	running = false
	mu.Unlock()
	h.choose("3").typeLine("5.6.7.10:44433")
	h.must("Hub address 5.6.7.10:44433 saved; the node agent is not running: systemctl start deyroute-node")
	mu.Lock()
	require.Equal(t, []string{"5.6.7.9:44433", "5.6.7.10:44433"}, saved)
	mu.Unlock()
}

func TestNodeSetHubErrors(t *testing.T) {
	log := &callLog{}
	o := nodeOptions(log)
	h := newHarness(t, o)
	h.choose("10").choose("3").typeLine("5.6.7.9:44433")
	h.must("Not available here")
	h.press("esc", "esc", "esc")

	o.SetHub = func(context.Context, string) (bool, error) {
		return false, deyerr.New(deyerr.C017, nil)
	}
	h = newHarness(t, o)
	h.choose("10").choose("3").typeLine("5.6.7.9:44433")
	h.must("DEY-C017", "1) Retry")
	h.mustNot("now connects")
}

// Every item of the node menu opens without DEY-X009 (or a missing call).
func TestNavigateNodeMenu(t *testing.T) {
	log := &callLog{}
	h := newHarness(t, nodeOptions(log))
	for _, it := range NodeMenu() {
		if it.Num == 0 {
			continue
		}
		h.choose(itoa(it.Num))
		if it.HubOnly {
			require.Equal(t, 1, h.depth(), "item %d", it.Num)
			note := i18n.T(i18n.TUIHubOnlyNote, i18n.T(it.Title), "5.6.7.8:44433")
			h.must(strings.SplitN(note, "\n", 2)[0])
			continue
		}
		require.Equal(t, 2, h.depth(), "item %d", it.Num)
		if l, ok := h.m.(Model).a.top().(*listScreen); ok {
			for i := 1; i <= len(l.choices(h.m.(Model).a)); i++ {
				h.choose(itoa(i))
				h.mustNot("DEY-X009", "DEY-X008")
				for h.depth() > 2 {
					h.press("esc")
				}
			}
		}
		h.mustNot("DEY-X009", "DEY-X008")
		h.press("esc")
		require.Equal(t, 1, h.depth(), "item %d", it.Num)
	}
}

func TestAnnounceHubMove(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	st := sampleStatus()
	st.Hub.ControlPort = 45001
	stub.StatusFn = func(context.Context) (api.Status, error) { return st, nil }
	var mu sync.Mutex
	res := api.AnnounceResult{Accepted: []string{"de-1"}, Offline: []string{"nl-1"}}
	stub.HubAnnounceMoveFn = func(_ context.Context, addr string) (api.AnnounceResult, error) {
		log.add("announce " + addr)
		mu.Lock()
		defer mu.Unlock()
		return res, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("10")
	h.must(" 3) Announce hub move\n")
	h.mustNot("Set hub address")
	h.choose("3")
	h.must("Run this on the old hub", "e.g. 5.6.7.9:45001 (a restore keeps the port)", "New hub address (IP:port): ")
	h.typeLine("5.6.7.9")
	h.must("Enter the address as IP:port")
	h.press("ctrl+u")
	h.typeLine("5.6.7.9:45001")
	h.must("Every online node saves 5.6.7.9:45001 as its hub address", "1) Continue")
	require.False(t, log.has("announce 5.6.7.9:45001"))
	h.press("enter")
	h.must("New hub address 5.6.7.9:45001 sent to: de-1",
		" ! Offline, not told: nl-1.\n On each of them use 10) Backup & Restore -> 3) Set hub address, or run: deyroute node set-hub 5.6.7.9:45001\n")
	require.True(t, log.has("announce 5.6.7.9:45001"))
	h.press("esc")
	// No node accepted: a dash, and no offline line without offline nodes.
	mu.Lock()
	res = api.AnnounceResult{}
	mu.Unlock()
	h.choose("3").typeLine("5.6.7.9:45001").press("enter")
	h.must("sent to: -")
	h.mustNot("Offline, not told")
	h.press("esc")
	// 0 cancels.
	h.choose("3").typeLine("5.6.7.9:45001").typeLine("0")
	h.must("Aborted")
	require.Equal(t, 2, log.count("announce 5.6.7.9:45001"))

	// Without a daemon the announce fails with the daemon error.
	h = newHarness(t, Options{Caps: Caps{Unicode: true}})
	h.choose("10").choose("3")
	h.must("e.g. 5.6.7.9:44433")
	h.typeLine("5.6.7.9:44433").press("enter")
	h.must("DEY-X003")
}

func TestRestoreAsksMovedHub(t *testing.T) {
	var mu sync.Mutex
	var gotIP []string
	o := Options{
		Caps: Caps{Unicode: true},
		RestoreCheck: func(_ context.Context, path, _ string) (RestorePlan, error) {
			return RestorePlan{Lost: "Restoring " + path + " replaces this server's /etc/deyroute.", MovedIP: "9.9.9.9", OldIP: "5.6.7.8"}, nil
		},
		Restore: func(_ context.Context, _, _, ip string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			gotIP = append(gotIP, ip)
			if ip == "" {
				return "Hub ir-1 restored.", nil
			}
			return "Hub ir-1 restored at " + ip + ".", nil
		},
	}
	h := newHarness(t, o)
	h.choose("10").choose("2").typeLine("/tmp/b1").typeLine("")
	h.must("This server's public IP is 9.9.9.9, but the backup's hub address is 5.6.7.8.",
		"Use this server's address (the hub moved here)? (y/n) [y]: ")
	h.mustNot("Type yes")
	h.typeLine("maybe")
	h.must("Answer y or n.")
	h.press("ctrl+u")
	h.typeLine("") // the default: the hub moved here
	h.must("Restoring /tmp/b1 replaces", "The hub address changes from 5.6.7.8 to 9.9.9.9; the hub certificate is re-issued.", "Type yes to continue: ")
	h.typeLine("yes")
	h.must("Restore complete.", "Hub ir-1 restored at 9.9.9.9.")
	h.press("esc")
	// No: the backup's address stays and the confirmation names no change.
	h.choose("2").typeLine("/tmp/b1").typeLine("").typeLine("n")
	h.must("Restoring /tmp/b1 replaces", "Type yes")
	h.mustNot("address changes")
	h.typeLine("yes")
	h.must("Hub ir-1 restored.")
	mu.Lock()
	require.Equal(t, []string{"9.9.9.9", ""}, gotIP)
	mu.Unlock()

	// A backup that cannot be read stops before any question.
	o.RestoreCheck = func(context.Context, string, string) (RestorePlan, error) {
		return RestorePlan{}, deyerr.New(deyerr.S004, nil)
	}
	h = newHarness(t, o)
	h.choose("10").choose("2").typeLine("/tmp/b1").typeLine("x")
	h.must("DEY-S004")
	h.mustNot("Type yes", "public IP")
}

func TestCheckHostPort(t *testing.T) {
	for _, ok := range []string{"5.6.7.9:44433", "[2001:db8::1]:44433", "hub.example.com:443"} {
		require.NoError(t, checkHostPort(ok, nil), ok)
	}
	for _, bad := range []string{"5.6.7.9", ":44433", "5.6.7.9:0", "5.6.7.9:70000", "5.6.7.9:44433 x"} {
		require.Error(t, checkHostPort(bad, nil), bad)
		require.True(t, strings.Contains(checkHostPort(bad, nil).Error(), "IP:port"))
	}
}

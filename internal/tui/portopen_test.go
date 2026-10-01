package tui

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
)

// fwStub answers PortCheck with "closed by ufw" until PortOpenFirewall ran
// the confirmed command for that port.
type fwStub struct {
	mu     sync.Mutex
	opened map[int]bool
	reqs   []api.PortOpenRequest
	checks int
	fail   error
}

func (f *fwStub) check(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	res := api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true, FirewallOpen: true, FirewallName: "nftables, ufw"}
	if !f.opened[r.Port] {
		res.FirewallOpen, res.FirewallName = false, "ufw"
		res.FirewallCommand = "ufw allow " + itoa(r.Port) + "/" + r.Proto
	}
	return res, nil
}

func (f *fwStub) open(_ context.Context, req api.PortOpenRequest) (api.PortOpenResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if f.fail != nil {
		return api.PortOpenResult{}, f.fail
	}
	if f.opened == nil {
		f.opened = map[int]bool{}
	}
	f.opened[req.Port] = true
	return api.PortOpenResult{Port: req.Port, Proto: req.Proto, Ran: req.Command, By: "ufw", FirewallOpen: true, FirewallName: "nftables, ufw"}, nil
}

func (f *fwStub) requests() []api.PortOpenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]api.PortOpenRequest(nil), f.reqs...)
}

func (f *fwStub) checkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

// Ports -> Check port offers to open a port the firewall closes: the
// confirmation names the exact command (typed yes), the command goes to the
// daemon, and coming back checks the port again.
func TestPortCheckOpenFirewall(t *testing.T) {
	fw := &fwStub{}
	stub := &apitest.Stub{PortCheckFn: fw.check, PortOpenFirewallFn: fw.open}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("4").choose("3").typeLine("443")
	h.must("✖ closed (ufw); open it: ufw allow 443/tcp", " 1) Open it in the firewall: ufw allow 443/tcp", " 0) Back")
	h.mustNot("Press Enter or q to go back.")

	// Declined: nothing runs, the check is not repeated.
	h.choose("1")
	h.must("Open 443/tcp in the firewall", "To open 443/tcp, deyroute runs this command on the hub (ufw):", "   ufw allow 443/tcp",
		"Afterwards anyone on the internet can connect to this port.", "Type yes to continue: ")
	h.typeLine("y")
	h.must("Aborted.", " 1) Open it in the firewall")
	require.Empty(t, fw.requests())
	require.Equal(t, 1, fw.checkCount())

	h.choose("1").typeLine("yes")
	h.must("Ran: ufw allow 443/tcp", "✔ 443/tcp is open in the firewall now (nftables, ufw).", "Go back to check the port again.")
	require.Equal(t, []api.PortOpenRequest{{Port: 443, Proto: "tcp", Command: "ufw allow 443/tcp"}}, fw.requests())
	h.press("enter")
	h.must("✔ open (nftables, ufw)", "Press Enter or q to go back.")
	h.mustNot("Open it in the firewall")
	require.Equal(t, 2, fw.checkCount())

	// A refusal of the daemon is shown with its DEY block and Retry.
	fw2 := &fwStub{fail: deyerr.New(deyerr.P032, deyerr.Params{"port": "8443/tcp", "firewall": "firewalld", "command": "firewall-cmd --permanent --add-port=8443/tcp && firewall-cmd --reload"})}
	stub2 := &apitest.Stub{PortCheckFn: fw2.check, PortOpenFirewallFn: fw2.open}
	h2 := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub2})
	h2.choose("6").choose("1") // Diagnostics -> Port check
	h2.typeLine("8443")
	h2.must(" 1) Open it in the firewall: ufw allow 8443/tcp")
	h2.choose("1").typeLine("yes")
	h2.must("✖ DEY-P032", "deyroute port check 8443/tcp --open", "1) Retry")
}

// The Add tunnel wizard stops at a port the firewall closes: open it (all
// blocked ports, after one confirmation naming every command), keep it,
// change it or skip it.
func TestAddTunnelOpensFirewall(t *testing.T) {
	fw := &fwStub{}
	var got api.TunnelAddRequest
	var mu sync.Mutex
	stub := &apitest.Stub{
		NodeListFn:         func(context.Context) ([]api.NodeInfo, error) { return sampleNodes()[:1], nil },
		PortCheckFn:        fw.check,
		PortOpenFirewallFn: fw.open,
		TunnelAddFn: func(_ context.Context, req api.TunnelAddRequest, _ func(api.Step)) (api.TunnelInfo, error) {
			mu.Lock()
			got = req
			mu.Unlock()
			return api.TunnelInfo{ID: "main", State: state.StateUp, ActiveTransport: "backhaul/wssmux", RTTms: 40}, nil
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 200}, Local: stub})
	h.choose("2").choose("1").typeLine("443,2053")
	h.must("✔ 443/tcp is free", "the firewall (ufw) blocks it", "Open it with: ufw allow 443/tcp",
		" 1) Change port", " 2) Skip", " 3) Open the 2 blocked ports in the firewall",
		" 4) Keep these 2 ports and continue (users may not reach them)")
	h.mustNot("3. Confirm")
	h.choose("3")
	h.must("To open 443,2053, deyroute runs these commands on the hub:", "   ufw allow 443/tcp\n   ufw allow 2053/tcp", "Type yes to continue: ")
	h.typeLine("no")
	require.Empty(t, fw.requests())
	h.must(" 3) Open the 2 blocked ports in the firewall")
	h.choose("3").typeLine("yes")
	require.Equal(t, []api.PortOpenRequest{{Port: 443, Proto: "tcp", Command: "ufw allow 443/tcp"}, {Port: 2053, Proto: "tcp", Command: "ufw allow 2053/tcp"}}, fw.requests())
	h.must("3. Confirm", "Ran 2 firewall command(s).")
	h.mustNot("the firewall (ufw) blocks it")
	h.press("enter")
	h.must("Tunnel main is UP via backhaul/wssmux (40ms)")
	mu.Lock()
	require.Len(t, got.Ports, 2)
	mu.Unlock()
}

// Keep continues with the warning; a node that cannot connect stops the
// wizard too, without an Open item (deyroute cannot open a provider panel).
func TestAddTunnelKeepsClosedPort(t *testing.T) {
	no := false
	fw := &fwStub{}
	stub := &apitest.Stub{
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return sampleNodes()[:1], nil },
		PortCheckFn: func(ctx context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
			if r.Port == 8443 {
				return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true, FirewallOpen: true, FirewallName: "nftables",
					Node: "de-1", NodeReachable: &no}, nil
			}
			return fw.check(ctx, r)
		},
		PortOpenFirewallFn: fw.open,
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 200}, Local: stub})
	h.choose("2").choose("1").typeLine("443")
	h.must(" 3) Open it in the firewall: ufw allow 443/tcp", " 4) Keep it and continue (users may not reach it)")
	h.choose("4")
	h.must("3. Confirm", "the firewall (ufw) blocks it")
	require.Empty(t, fw.requests())

	h.press("esc", "ctrl+u")
	h.typeLine("8443")
	h.must("node de-1 cannot reach it", " 1) Change port", " 4) Keep it and continue")
	h.mustNot(" 3) ")
	h.choose("3")
	h.must("Invalid choice: 3")
	h.choose("2")
	h.must("No ports left. Enter the ports again.")
}

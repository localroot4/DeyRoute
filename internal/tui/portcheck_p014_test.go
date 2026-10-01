package tui

import (
	"context"
	"testing"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
)

// TestPortCheckShowsP014: a node that cannot reach the port gives the
// DEY-P014 block (what, why, fix, log) under the four lines (section 13).
func TestPortCheckShowsP014(t *testing.T) {
	no := false
	stub := &apitest.Stub{PortCheckFn: func(context.Context, api.PortCheckRequest) (api.PortCheckResult, error) {
		return api.PortCheckResult{Port: 2053, Proto: "tcp", BindFree: true, FirewallOpen: true, FirewallName: "nftables",
			Node: "de-1", NodeReachable: &no, NodeError: &api.ErrorDTO{Code: "DEY-P014",
				Message: "Port 2053/tcp is not reachable from node de-1", Why: "the node could not connect",
				Fix: "open the port in your provider's firewall panel", Log: "/var/log/deyroute/hub.log", Detail: "i/o timeout"}}, nil
	}}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("4").choose("3")
	h.typeLine("2053")
	h.must("reachable from node de-1:", "✖ DEY-P014  Port 2053/tcp is not reachable from node de-1",
		"Why:  the node could not connect", "Fix:  open the port in your provider's firewall panel",
		"Log:  /var/log/deyroute/hub.log (search DEY-P014)", "| i/o timeout")
}

// The Add-tunnel wizard stops at a port the node cannot reach and shows
// DEY-P014 in the three-line format above its choices.
func TestAddTunnelShowsP014(t *testing.T) {
	no := false
	stub := &apitest.Stub{
		NodeListFn: func(context.Context) ([]api.NodeInfo, error) { return sampleNodes()[:1], nil },
		PortCheckFn: func(_ context.Context, r api.PortCheckRequest) (api.PortCheckResult, error) {
			return api.PortCheckResult{Port: r.Port, Proto: r.Proto, BindFree: true, FirewallOpen: true, FirewallName: "nftables",
				Node: "de-1", NodeReachable: &no, NodeError: &api.ErrorDTO{Code: "DEY-P014",
					Message: "Port 8443/tcp is not reachable from node de-1", Why: "the node could not connect",
					Fix: "open the port in your provider's firewall panel"}}, nil
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true, Width: 200}, Local: stub})
	h.choose("2").choose("1").typeLine("8443")
	h.must("✖ DEY-P014  Port 8443/tcp is not reachable from node de-1", "Why:  the node could not connect",
		"Fix:  open the port in your provider's firewall panel", " 4) Keep it and continue")
}

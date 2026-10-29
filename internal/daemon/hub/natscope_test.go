package hub

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/systemd"
)

// trHop is a forward test transport whose node side carries a scoped
// port-hopping DNAT like hysteria2/udp with port hopping (DR-18): sources
// the hub's public addresses, destination the node's public address.
const trHop = "tfh/hop"

func init() {
	backend.Register(&hopBackend{testBackend{name: "tfh", version: "1.0", builtin: true, transports: []backend.Transport{
		{Backend: "tfh", Name: "hop", Direction: backend.Forward, Protos: []string{"tcp"}, Stealth: 1},
	}}})
}

type hopBackend struct{ testBackend }

func (b *hopBackend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	r, err := b.testBackend.Render(in, side)
	if err == nil && side == backend.SideNode {
		saddr := []string{in.Hub.PublicIP}
		if in.Hub.PublicIP6 != "" {
			saddr = append(saddr, in.Hub.PublicIP6)
		}
		r.NAT = []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: in.ControlPort,
			SAddr: saddr, DAddr: in.Node.PublicIP}}
	}
	return r, err
}

// hopNAT is the NAT of the hop instance the node holds now.
func (n *tnode) hopNAT(inst string) ([]backend.NATRule, bool) {
	n.tmu.Lock()
	defer n.tmu.Unlock()
	args, ok := n.rendered[inst]
	return args.NAT, ok
}

// TestHubMoveRerendersHoppingNAT: the port-hopping DNAT is scoped to the
// hub's public address, so when that address changes the node side is
// rendered again with the new one (DR-18): at once for a running hub
// whose hub.public_ip changed (config apply re-plans the tunnel and the
// changed node payload is sent), and again when the node reconnects, as
// it does to the moved hub after announce-move (a hub restored on new
// hardware renders every node side from its own config.yaml).
func TestHubMoveRerendersHoppingNAT(t *testing.T) {
	te := startTunnelHub(t)
	fn := te.joinNode("de-1")
	fn.features = []string{api.FeatureNATScope}
	n := te.wireNode(fn)
	port := freePort(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "hop", Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trHop, Failover: fastFailover(false)})
	inst := systemd.InstanceName("hop", "de-1", trHop)
	nat, ok := n.hopNAT(inst)
	require.True(t, ok)
	require.Len(t, nat, 1)
	oldIP := te.h.Config().Hub.PublicIP
	require.Equal(t, []string{oldIP}, nat[0].SAddr)

	cfg := config.Clone(te.h.Config())
	cfg.Hub.PublicIP, cfg.Hub.PublicIP6 = "7.7.7.7", "2001:db8::7"
	require.NoError(t, config.SaveWith(filepath.Join(te.root, config.DefaultPath), cfg, testValidate))
	_, err := te.h.Local().ConfigApply(ctxT(t), nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		nat, _ := n.hopNAT(inst)
		return len(nat) == 1 && len(nat[0].SAddr) == 2 && nat[0].SAddr[0] == "7.7.7.7" && nat[0].SAddr[1] == "2001:db8::7"
	}, testWait, 10*time.Millisecond, "the node side is rendered with the new hub address")

	// The node reconnects (to the moved hub): its side is sent again,
	// with the hub's current address.
	n.tmu.Lock()
	delete(n.rendered, inst)
	n.tmu.Unlock()
	n.stop()
	te.waitOnline("de-1", false)
	n.start()
	te.waitOnline("de-1", true)
	require.Eventually(t, func() bool {
		nat, ok := n.hopNAT(inst)
		return ok && len(nat) == 1 && nat[0].SAddr[0] == "7.7.7.7"
	}, testWait, 10*time.Millisecond, "the node side is sent again after the reconnect")
}

// TestHubSendsNATScopeOnlyToCapableNodes: a node that does not announce
// api.FeatureNATScope (an older agent) gets the port-hopping DNAT without
// SAddr and DAddr, the rule it always had (DR-18); once it announces the
// feature (updated, reconnected) it gets the scoped rule.
func TestHubSendsNATScopeOnlyToCapableNodes(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	port := freePort(t)
	te.addTunnelUp(api.TunnelAddRequest{ID: "hop", Node: "de-1", Ports: []api.PortSpec{{Listen: port}},
		FixedTransport: trHop, Failover: fastFailover(false)})
	inst := systemd.InstanceName("hop", "de-1", trHop)
	nat, ok := n.hopNAT(inst)
	require.True(t, ok)
	require.Equal(t, []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: nat[0].ToPort}}, nat)

	n.stop()
	te.waitOnline("de-1", false)
	n.features = []string{api.FeatureNATScope}
	n.start()
	te.waitOnline("de-1", true)
	require.Eventually(t, func() bool {
		nat, _ := n.hopNAT(inst)
		return len(nat) == 1 && len(nat[0].SAddr) == 1 && nat[0].DAddr != ""
	}, testWait, 10*time.Millisecond, "the updated node gets the scoped rule")
}

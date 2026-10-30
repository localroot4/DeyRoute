package hub

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/backend/frp"
	"github.com/localroot4/deyroute/internal/systemd"
)

// trPP is a test transport whose node side is an frpc.toml with the PROXY
// protocol line of an frp rung with advanced.proxy_protocol (DR-21).
const trPP = "tfp/pp"

const ppConfig = "serverPort = 7000\n\n[[proxies]]\nname = \"tcp-443\"\ntransport.proxyProtocolVersion = \"v1\"\n"

func init() {
	backend.Register(&ppBackend{testBackend{name: "tfp", version: "1.0", builtin: true, transports: []backend.Transport{
		{Backend: "tfp", Name: "pp", Direction: backend.Reverse, Protos: []string{"tcp"}, Stealth: 1},
	}}})
}

type ppBackend struct{ testBackend }

func (b *ppBackend) Render(in backend.RenderInput, side backend.Side) (backend.Rendered, error) {
	r, err := b.testBackend.Render(in, side)
	if err == nil && side == backend.SideNode {
		files := map[string][]byte{frp.ClientFile: []byte(ppConfig)}
		for k, v := range r.Files {
			files[k] = v
		}
		r.Files = files
	}
	return r, err
}

// frpcOf is the frpc.toml of inst the node holds now.
func (n *tnode) frpcOf(inst string) (string, bool) {
	n.tmu.Lock()
	defer n.tmu.Unlock()
	args, ok := n.rendered[inst]
	return string(args.Files[frp.ClientFile]), ok
}

// TestHubSendsProxyHeaderOnlyToCapableNodes: a node that does not announce
// api.FeatureProxyHeader (an older agent, whose echo helpers would read the
// header as data) gets the frp config without the PROXY protocol line;
// once it announces the feature it gets the line (DR-21).
func TestHubSendsProxyHeaderOnlyToCapableNodes(t *testing.T) {
	te := startTunnelHub(t)
	n := te.tunnelNode("de-1")
	te.addTunnelUp(api.TunnelAddRequest{ID: "pp", Node: "de-1", Ports: []api.PortSpec{{Listen: freePort(t)}},
		FixedTransport: trPP, Failover: fastFailover(false)})
	inst := systemd.InstanceName("pp", "de-1", trPP)
	cfg, ok := n.frpcOf(inst)
	require.True(t, ok)
	require.NotContains(t, cfg, "proxyProtocolVersion")
	require.Contains(t, cfg, `name = "tcp-443"`)

	n.stop()
	te.waitOnline("de-1", false)
	n.features = []string{api.FeatureProxyHeader}
	n.start()
	te.waitOnline("de-1", true)
	require.Eventually(t, func() bool {
		cfg, _ := n.frpcOf(inst)
		return cfg == ppConfig
	}, testWait, 10*time.Millisecond, "the updated node gets the PROXY protocol line")
}

package render

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/backend"
	_ "github.com/localroot4/deyroute/internal/backend/all"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// usedPrivileges collects, per allowlist row, what the rendered units asked
// for.
type usedPrivileges map[string]*Privileges

func (up usedPrivileges) add(b, tr string, u backend.UnitSpec) {
	key, _ := privilegeRow(b, tr)
	p := up[key]
	if p == nil {
		p = &Privileges{}
		up[key] = p
	}
	merge := func(list []string, add ...string) []string {
		for _, v := range add {
			if !slices.Contains(list, v) {
				list = append(list, v)
			}
		}
		slices.Sort(list)
		return list
	}
	p.RunAsRoot = p.RunAsRoot || u.RunAsRoot
	p.ExtraCaps = merge(p.ExtraCaps, u.ExtraCaps...)
	p.AddressFamilies = merge(p.AddressFamilies, u.AddressFamilies...)
	p.DropHardening = merge(p.DropHardening, backend.SortedKeys(u.DropHardening)...)
	p.ReadWritePaths = merge(p.ReadWritePaths, u.ReadWritePaths...)
}

// checkCandidate checks both sides of c the way they are checked in
// production: the hub with the candidate's backend and transport before it
// writes its side, the node with what the config directory names (the
// backend, and the transport of a warm unit; none for the canary).
func checkCandidate(t *testing.T, c Candidate, canary bool, used usedPrivileges) {
	t.Helper()
	require.NoError(t, CheckPrivileges(c.Backend, c.Transport.Name, c.Hub.Unit), "%s: hub side", c.TransportID)
	used.add(c.Backend, c.Transport.Name, c.Hub.Unit)

	rel := strings.TrimPrefix(c.NodeSide.ConfigDir, config.BackendsConfDir+"/")
	b := strings.Split(rel, "/")[0]
	tr := ConfigDirTransport(c.NodeSide.ConfigDir)
	require.Equal(t, c.Backend, b, c.NodeSide.ConfigDir)
	if canary {
		require.Empty(t, tr, "a canary directory names no transport: %s", c.NodeSide.ConfigDir)
	} else {
		require.Equal(t, c.Transport.Name, tr, c.NodeSide.ConfigDir)
	}
	require.NoError(t, CheckPrivileges(b, tr, c.NodeSide.Unit), "%s: node side", c.TransportID)
	used.add(b, tr, c.NodeSide.Unit)
}

// Every registered backend/transport renders, for both sides, only units
// the allowlist accepts: as a warm rung (TCP and UDP maps with loopback
// targets, so the UDP companions and wireguard/kernel's route_localnet are
// rendered; a TCP map whose target listens on every interface, the only
// kind direct/haproxy renders; a UDP map) and as the canary. The table
// grants nothing no renderer uses, so it cannot drift either way.
func TestPrivilegeAllowlistCoversRenderers(t *testing.T) {
	variants := []struct {
		name  string
		ports []config.PortMap
	}{
		{"tcp+udp", []config.PortMap{{Listen: 443, Proto: config.ProtoTCP}, {Listen: 443, Proto: config.ProtoUDP}}},
		{"tcp to 0.0.0.0", []config.PortMap{{Listen: 8443, Proto: config.ProtoTCP, Target: "0.0.0.0:8443"}}},
		{"udp", []config.PortMap{{Listen: 27015, Proto: config.ProtoUDP}}},
	}
	e := newEnv(t)
	input := func(id string, ports []config.PortMap) Input {
		tun := &e.cfg.Tunnels[0]
		tun.Nodes = []string{"de-1"}
		tun.Ports = ports
		tun.Ladder = config.LadderRef{Inline: []string{id}}
		in := e.input()
		in.Registry = BackendRegistry{}
		in.LookPath = func(name string) (string, error) { return "/usr/sbin/" + name, nil }
		return in
	}

	// Transports whose backend refuses every input: they produce no unit.
	neverRenders := map[string]bool{
		"frp/wss": true, // frps v0.71.0 cannot accept wss itself (frp.wssUnavailable)
	}

	all := backend.AllTransports()
	require.NotEmpty(t, all)
	used := usedPrivileges{}
	for _, tr := range all {
		id := tr.ID()
		if neverRenders[id] {
			plan, err := Plan(input(id, variants[0].ports))
			require.NoError(t, err)
			require.Empty(t, plan.Candidates, "%s renders now: check its units", id)
			continue
		}
		warm := 0
		for _, v := range variants {
			plan, err := Plan(input(id, v.ports))
			if deyerr.HasCode(err, deyerr.C009) {
				continue // the transport carries none of these protocols
			}
			require.NoError(t, err, "%s (%s)", id, v.name)
			for _, s := range plan.Skipped {
				t.Logf("%s (%s) skipped: %s", id, v.name, s.Reason)
			}
			for _, c := range plan.Candidates {
				checkCandidate(t, c, false, used)
				warm++
			}
		}
		require.Positive(t, warm, "%s was never rendered as a warm rung", id)

		// The canary runs rung 1 of the ladder, which must carry the
		// tunnel's maps; the canary itself carries its echo only.
		ports := variants[2].ports
		if tr.Supports(config.ProtoTCP) {
			ports = []config.PortMap{{Listen: 443, Proto: config.ProtoTCP}}
		}
		cand, err := CanaryPlan(input(id, ports), 40000)
		if id == "direct/haproxy" {
			// HAProxy dials the target on the node's public address; the
			// canary's echo listens on the node's loopback.
			require.Error(t, err)
			continue
		}
		require.NoError(t, err, "%s canary", id)
		checkCandidate(t, *cand, true, used)
	}

	for key, row := range privilegeAllowlist {
		got := used[key]
		require.NotNil(t, got, "allowlist row %s: no renderer uses it", key)
		want := row
		for _, l := range []*[]string{&want.ExtraCaps, &want.AddressFamilies, &want.DropHardening, &want.ReadWritePaths} {
			*l = slices.Sorted(slices.Values(*l))
		}
		require.Equal(t, want, *got, "allowlist row %s grants exactly what its renderers use", key)
	}
	// Every other backend and transport runs with the template alone.
	for key, got := range used {
		if _, ok := privilegeAllowlist[key]; !ok {
			require.Equal(t, Privileges{}, *got, "%s", key)
		}
	}
}

// No row relaxes NoNewPrivileges or the capability bounding set, and those
// two are refused for every backend, listed or not.
func TestPrivilegeAllowlistNeverRelaxesNoNewPrivileges(t *testing.T) {
	for key, row := range privilegeAllowlist {
		for _, opt := range neverRelaxed {
			require.NotContains(t, row.DropHardening, opt, key)
		}
	}
	for _, b := range []string{"wireguard", "awg", "rathole", "direct", "xray", "unknown"} {
		for _, opt := range neverRelaxed {
			err := CheckPrivileges(b, "x", backend.UnitSpec{DropHardening: map[string]string{opt: "because"}})
			require.Error(t, err)
			require.Contains(t, err.Error(), `may not drop "`+opt+`" (DropHardening)`)
		}
	}
}

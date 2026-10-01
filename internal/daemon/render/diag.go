package render

import (
	"strconv"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/systemd"
)

// DiagPlan renders the temporary candidate of `deyroute diag speed` (section
// 14): transport transportID on node, rendered like the canary (Canary=true,
// instance systemd.CanaryInstance(tunnel), config directory
// CanaryConfigDir, control port CanaryCtlKey) with one synthetic TCP port
// map: a loopback listen port on the hub (allocated with
// CanaryLoopbackKey, bound on 127.0.0.1) forwarded to 127.0.0.1:targetPort
// on the node, where the node's built-in traffic generator listens. The
// node agent accepts only warm and canary instance names, so the speed test
// borrows the canary slot: the caller must make sure the canary does not
// run meanwhile. It returns the candidate and the hub loopback port.
func DiagPlan(in Input, node, transportID string, targetPort int) (*Candidate, int, error) {
	if targetPort < 1 || targetPort > 65535 {
		return nil, 0, deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(targetPort)})
	}
	p, _, err := newPlanner(in)
	if err != nil {
		return nil, 0, err
	}
	t := in.Tunnel
	n, ok := in.Nodes[node]
	if !ok {
		return nil, 0, deyerr.New(deyerr.C010, deyerr.Params{"node": node, "tunnel": t.ID})
	}
	if n.ID == "" {
		n.ID = node
	}
	b, tr, err := p.reg.Lookup(transportID)
	if err != nil {
		return nil, 0, err
	}
	if !tr.Supports(config.ProtoTCP) {
		return nil, 0, deyerr.New(deyerr.B010, deyerr.Params{"transport": transportID, "proto": config.ProtoTCP})
	}
	loop, err := in.CtlPort(CanaryLoopbackKey(t.ID))
	if err != nil {
		return nil, 0, err
	}
	ctl, err := in.CtlPort(CanaryCtlKey(t.ID))
	if err != nil {
		return nil, 0, err
	}
	p.in.Tunnel.Ports = []config.PortMap{{
		Listen: loop, Proto: config.ProtoTCP, Target: backend.HostPort("127.0.0.1", targetPort), Probe: config.ProbeTCP,
	}}
	p.in.Tunnel.ProbePort = 0
	ri, err := p.renderInput(b, tr, n, ctl, CanaryConfigDir(b.Name(), t.ID))
	if err != nil {
		return nil, 0, err
	}
	ri.Canary = true
	ri.ListenAddr = "127.0.0.1"
	instance := systemd.CanaryInstance(t.ID)
	ri.FirstRun = in.FirstRun != nil && in.FirstRun(instance)
	c, err := p.renderCandidate(b, tr, ri, instance)
	if err != nil {
		return nil, 0, err
	}
	return &c, loop, nil
}

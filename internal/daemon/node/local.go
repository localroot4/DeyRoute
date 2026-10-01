package node

import (
	"context"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// nodeLocal is the Local API on a node: Status, Logs, NodeSetHub, StopAll
// and DoctorCollect for this node; every other method answers DEY-X009
// through the embedded api.UnimplementedLocal.
type nodeLocal struct {
	api.UnimplementedLocal
	a *agent
}

func (a *agent) localAPI() api.Local {
	return &nodeLocal{UnimplementedLocal: api.UnimplementedLocal{Role: config.RoleNode, Need: config.RoleHub}, a: a}
}

// Status implements api.Local.
func (l *nodeLocal) Status(context.Context) (api.Status, error) { return l.a.status(), nil }

// Logs implements api.Local: target "node" (or empty) or a tunnel id.
func (l *nodeLocal) Logs(ctx context.Context, q api.LogQuery, emit func(api.LogLine) error) error {
	return l.a.localLogs(ctx, q, emit)
}

// NodeSetHub implements api.Local (deyroute node set-hub): the address is
// stored and the control stream reconnects at once.
func (l *nodeLocal) NodeSetHub(ctx context.Context, addr string) error {
	return l.a.setHub(ctx, addr, 0)
}

// StopAll implements api.Local: every deyroute-tun@ unit on this node stops.
func (l *nodeLocal) StopAll(ctx context.Context) error { return l.a.stopAll(ctx) }

// DoctorCollect implements api.Local for this node (node "" or its own
// id); other nodes need the hub.
func (l *nodeLocal) DoctorCollect(ctx context.Context, node string) (api.DoctorData, error) {
	if node != "" && node != l.a.nodeID {
		return api.DoctorData{}, deyerr.New(deyerr.X009, deyerr.Params{"role": config.RoleNode, "need": config.RoleHub})
	}
	return l.a.doctorData(ctx), nil
}

package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/doctor"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// Doctor limits (section 13).
const (
	// doctorNodeTimeout bounds doctor.collect on a node.
	doctorNodeTimeout = 3 * time.Minute
	// doctorPortChecks caps the 4-stage port checks of the bundle.
	doctorPortChecks = 16
	// doctorPortCheckBudget bounds all port checks together.
	doctorPortCheckBudget = 90 * time.Second
)

// DoctorCollect implements api.Local (`deyroute doctor [--node id]`,
// section 13). On the hub: OS, versions, units, the last 200 lines of every
// log, the firewall, sysctl, listening ports, certificates (custom tunnel
// certificates too), the dashboard status, the last 50 events, every rung
// of every ladder (the active one probed, the others validated) and the
// 4-stage check of the tunnel ports; the 15 rules run on facts the hub
// knows (node clock skew, secret permissions, old join tokens, external
// firewall blocks, port conflicts). With node the node's own data is
// collected there (doctor.collect; DEY-N003 when it is offline).
func (l *local) DoctorCollect(ctx context.Context, node string) (api.DoctorData, error) {
	h := l.h
	cfg := h.Config()
	if node = strings.TrimSpace(node); node != "" {
		if _, ok := cfg.NodeByID(node); !ok {
			return api.DoctorData{}, withLog(deyerr.New(deyerr.N008, deyerr.Params{"node": node}))
		}
		var data api.DoctorData
		cctx, cancel := context.WithTimeout(ctx, doctorNodeTimeout)
		defer cancel()
		if err := h.Call(cctx, node, api.CmdDoctorCollect, nil, &data); err != nil {
			return api.DoctorData{}, withLog(err)
		}
		if data.Sections == nil {
			data.Sections = map[string]string{}
		}
		return data, nil
	}
	certFiles := map[string]string{}
	for _, t := range cfg.Tunnels {
		if t.TLS.Mode == config.TLSModeCustom && t.TLS.CertFile != "" {
			certFiles[t.ID] = t.TLS.CertFile
		}
	}
	col := (&doctor.Collector{Root: h.o.Root, Runner: h.o.Runner, Now: h.o.Now, TunnelCertFiles: certFiles}).Collect(ctx)
	st, err := l.Status(ctx)
	if err != nil {
		return api.DoctorData{}, err
	}
	events, err := h.st.Events(state.EventFilter{Limit: doctor.EventCount})
	if err != nil {
		return api.DoctorData{}, withLog(err)
	}
	checks, conflicts := h.doctorPortChecks(ctx, l, cfg)
	f := doctor.Facts{
		Role:                   config.RoleHub,
		Status:                 st,
		Now:                    h.now(),
		ClockSkew:              h.clockSkews(),
		SecretPermProblems:     h.secretProblems(),
		OldJoinTokens:          h.oldJoinTokens(),
		ExternalFirewallBlocks: h.externalBlocks(ctx, cfg),
		PortConflicts:          conflicts,
		Events:                 events,
		Sections: map[string]string{
			doctor.SectionStatus:     doctor.StatusSection(st),
			doctor.SectionEvents:     doctor.EventsSection(events),
			doctor.SectionLadder:     dlog.Redact(h.ladderSection(ctx, l, cfg)),
			doctor.SectionPortChecks: dlog.Redact(checks),
		},
	}
	col.Apply(&f)
	return api.DoctorData{Role: config.RoleHub, Sections: f.Sections, Findings: doctor.Run(f)}, nil
}

// secretProblems returns one line per DEY-S002 problem of secrets/.
func (h *Hub) secretProblems() []string {
	var out []string
	for _, err := range h.secretStore().CheckPerms() {
		e := deyerr.As(err)
		if p, ok := e.Params["path"]; ok {
			out = append(out, fmt.Sprintf("%v (%v)", p, e.Params["mode"]))
			continue
		}
		out = append(out, e.Message())
	}
	return out
}

// oldJoinTokens counts the join tokens created more than 15 minutes ago
// that are still stored (doctor rule R15).
func (h *Hub) oldJoinTokens() int {
	data, err := os.ReadFile(h.joins.Path)
	if err != nil {
		return 0
	}
	var f struct {
		Tokens []struct {
			Created time.Time `json:"created"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &f) != nil {
		return 0
	}
	n := 0
	cutoff := h.now().Add(-oldJoinToken)
	for _, t := range f.Tokens {
		if t.Created.Before(cutoff) {
			n++
		}
	}
	return n
}

// externalBlocks lists the deyroute ports (control port, tunnel ports) an
// external firewall blocks (doctor rule R04).
func (h *Hub) externalBlocks(ctx context.Context, cfg *config.Config) []string {
	var out []string
	for _, pp := range h.deyroutePorts(cfg) {
		v, err := firewall.Check(ctx, h.o.Runner, pp.port, pp.proto)
		if err != nil || !v.Blocked {
			continue
		}
		out = append(out, doctor.FirewallBlock(pp.port, pp.proto, v.By))
	}
	return out
}

// ladderSection lists every rung of every ladder on every node: the active
// one with a fresh path probe, the others validated (warm), skipped (with
// the reason) or in quarantine (section 13).
func (h *Hub) ladderSection(ctx context.Context, l *local, cfg *config.Config) string {
	var b strings.Builder
	if len(cfg.Tunnels) == 0 {
		return "no tunnels\n"
	}
	for _, t := range cfg.Tunnels {
		d, err := l.TunnelShow(ctx, t.ID)
		if err != nil {
			fmt.Fprintf(&b, "tunnel %s: %s\n\n", t.ID, deyerr.As(err).Message())
			continue
		}
		fmt.Fprintf(&b, "tunnel %s (%s) state %s", t.ID, d.Name, d.State)
		if d.ActiveTransport != "" {
			fmt.Fprintf(&b, ", active %s on %s", d.ActiveTransport, d.ActiveNode)
		}
		b.WriteString("\n")
		if d.ActiveTransport != "" && t.Enabled {
			if reps, err := l.DiagProbe(ctx, t.ID, false); err == nil {
				for _, r := range reps {
					res := "ok"
					if !r.OK {
						res = "FAILED " + r.Error
					}
					fmt.Fprintf(&b, "  probe %d/%s (%s): %s %dms\n", r.Port, r.Proto, r.Kind, res, r.RTTms)
				}
			}
		}
		for _, r := range d.Rungs {
			status := "not rendered"
			switch {
			case r.Active:
				status = "ACTIVE"
			case r.Skipped != "":
				status = "skipped: " + r.Skipped
			case r.Warm:
				status = "warm (validated)"
			}
			if !r.Quarantine.IsZero() {
				status += ", quarantined until " + r.Quarantine.UTC().Format(time.RFC3339)
			}
			fmt.Fprintf(&b, "  %-28s %-8s ctl %-5d %-10s %s\n", r.Transport, r.Node, r.ControlPort, r.UnitState, status)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// doctorPortChecks runs the 4-stage port check (section 10) for the tunnel
// ports (at most doctorPortChecks within doctorPortCheckBudget) and
// returns its text and the ports another process holds.
func (h *Hub) doctorPortChecks(ctx context.Context, l *local, cfg *config.Config) (string, []string) {
	var b strings.Builder
	var conflicts []string
	cctx, cancel := context.WithTimeout(ctx, doctorPortCheckBudget)
	defer cancel()
	n := 0
	for _, t := range cfg.Tunnels {
		for _, pm := range t.Ports {
			key := fmt.Sprintf("%d/%s", pm.Listen, pm.Proto)
			if n >= doctorPortChecks || cctx.Err() != nil {
				fmt.Fprintf(&b, "%-10s tunnel %s: not checked (limit reached)\n", key, t.ID)
				continue
			}
			n++
			r, err := l.PortCheck(cctx, api.PortCheckRequest{Port: pm.Listen, Proto: pm.Proto})
			if err != nil {
				fmt.Fprintf(&b, "%-10s tunnel %s: %s\n", key, t.ID, deyerr.As(err).Message())
				continue
			}
			bind := "free"
			if !r.BindFree {
				bind = "used by " + firstNonEmpty(r.BindProcess, "an unknown process")
				if r.BindByDey {
					bind += " (deyroute)"
				} else if t.Enabled {
					conflicts = append(conflicts, key+" of tunnel "+t.ID+" is "+bind)
				}
			}
			fw := "open (" + r.FirewallName + ")"
			if !r.FirewallOpen {
				fw = "BLOCKED by " + r.FirewallName + ": " + r.FirewallCommand
			}
			node := "not tested"
			if r.NodeReachable != nil {
				node = fmt.Sprintf("%s from %s (%dms)", okText(*r.NodeReachable), r.Node, r.NodeRTTms)
			}
			tun := "not tested"
			if r.TunnelOK != nil {
				tun = fmt.Sprintf("%s (%dms)", okText(*r.TunnelOK), r.TunnelRTTms)
			}
			fmt.Fprintf(&b, "%-10s tunnel %s\n  1 bind: %s\n  2 firewall: %s\n  3 from node: %s\n  4 via tunnel: %s\n",
				key, t.ID, bind, fw, node, tun)
		}
	}
	if b.Len() == 0 {
		return "no tunnel ports\n", conflicts
	}
	return b.String(), conflicts
}

// okText is "reachable" or "NOT reachable".
func okText(ok bool) string {
	if ok {
		return "reachable"
	}
	return "NOT reachable"
}

package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
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
	// doctorPortWorkers is the number of 4-stage port checks run at once
	// (the probe worker pool size of section 12).
	doctorPortWorkers = 8
	// doctorPortCheckBudget bounds all port checks together; a port map
	// whose check has not started by then is listed as not checked.
	doctorPortCheckBudget = 2 * time.Minute
)

// DoctorCollect implements api.Local (`deyroute doctor [--node id]`,
// section 13). On the hub: OS, versions, units, the last 200 lines of every
// log, the firewall, sysctl, listening ports, certificates (custom tunnel
// certificates too), the dashboard status, the last 50 events, every rung
// of every ladder (the active one probed, the others validated) and the
// 4-stage check of the tunnel ports; the 15 rules run on facts the hub
// knows (node clock skew, secret permissions, join tokens, external
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
	jt := h.joinTokenFacts()
	f := doctor.Facts{
		Role:                   config.RoleHub,
		Status:                 st,
		Now:                    h.now(),
		ClockSkew:              h.clockSkews(),
		SecretPermProblems:     h.secretProblems(),
		ExpiredJoinTokens:      jt.expired,
		LongJoinTokens:         jt.long,
		LongJoinUntil:          jt.until,
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

// joinTokenState is what doctor rule R15 needs about the join tokens.
type joinTokenState struct {
	expired int       // past their expiry, still stored
	long    int       // valid, created with a TTL over longJoinTTL
	until   time.Time // latest expiry of the long ones
}

// joinTokenFacts removes the expired join tokens (as the hub does when the
// last one expires) and describes what is left for doctor rule R15: tokens
// still stored after their expiry (the prune failed) and the valid tokens
// made with a TTL over 15 minutes, which keep the join window open.
func (h *Hub) joinTokenFacts() joinTokenState {
	if err := h.joins.Prune(); err != nil {
		h.log.Warn("cannot prune the join tokens", dlog.Err(err))
	}
	var out joinTokenState
	data, err := os.ReadFile(h.joins.Path)
	if err != nil {
		return out
	}
	var f struct {
		Tokens []struct {
			Created time.Time `json:"created"`
			Expires time.Time `json:"expires"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &f) != nil {
		return out
	}
	now := h.now()
	for _, t := range f.Tokens {
		switch {
		case !now.Before(t.Expires):
			out.expired++
		case t.Expires.Sub(t.Created) > longJoinTTL:
			out.long++
			if t.Expires.After(out.until) {
				out.until = t.Expires
			}
		}
	}
	return out
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

// portCheckJob is one port map of the doctor's port checks.
type portCheckJob struct {
	tunnel  string
	enabled bool
	pm      config.PortMap
	started bool
	res     api.PortCheckResult
	err     error
}

// doctorPortChecks runs the 4-stage port check (section 10) for every port
// map of every tunnel, doctorPortWorkers at a time within
// doctorPortCheckBudget, and returns its text (in config order) and the
// ports another process holds.
func (h *Hub) doctorPortChecks(ctx context.Context, l *local, cfg *config.Config) (string, []string) {
	var jobs []*portCheckJob
	for _, t := range cfg.Tunnels {
		for _, pm := range t.Ports {
			jobs = append(jobs, &portCheckJob{tunnel: t.ID, enabled: t.Enabled, pm: pm})
		}
	}
	if len(jobs) == 0 {
		return "no tunnel ports\n", nil
	}
	cctx, cancel := context.WithTimeout(ctx, doctorPortCheckBudget)
	defer cancel()
	sem := make(chan struct{}, doctorPortWorkers)
	var wg sync.WaitGroup
	for _, j := range jobs {
		select {
		case sem <- struct{}{}:
		case <-cctx.Done():
		}
		if cctx.Err() != nil {
			break
		}
		j.started = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			j.res, j.err = l.PortCheck(cctx, api.PortCheckRequest{Port: j.pm.Listen, Proto: j.pm.Proto})
		}()
	}
	wg.Wait()

	var b strings.Builder
	var conflicts []string
	for _, j := range jobs {
		key := fmt.Sprintf("%d/%s", j.pm.Listen, j.pm.Proto)
		if !j.started {
			fmt.Fprintf(&b, "%-10s tunnel %s: not checked (time limit of %s reached)\n", key, j.tunnel, doctorPortCheckBudget)
			continue
		}
		if j.err != nil {
			fmt.Fprintf(&b, "%-10s tunnel %s: %s\n", key, j.tunnel, deyerr.As(j.err).Message())
			continue
		}
		r := j.res
		bind := "free"
		if !r.BindFree {
			bind = "used by " + firstNonEmpty(r.BindProcess, "an unknown process")
			if r.BindByDey {
				bind += " (deyroute)"
			} else if j.enabled {
				conflicts = append(conflicts, key+" of tunnel "+j.tunnel+" is "+bind)
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
		if r.NodeError != nil {
			node += " " + r.NodeError.Code
		}
		tun := "not tested"
		if r.TunnelOK != nil {
			tun = fmt.Sprintf("%s (%dms)", okText(*r.TunnelOK), r.TunnelRTTms)
		}
		fmt.Fprintf(&b, "%-10s tunnel %s\n  1 bind: %s\n  2 firewall: %s\n  3 from node: %s\n  4 via tunnel: %s\n",
			key, j.tunnel, bind, fw, node, tun)
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

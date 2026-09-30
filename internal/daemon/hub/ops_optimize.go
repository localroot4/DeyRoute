package hub

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/sysctl"
)

// sysctlManager is the hub's sysctl manager (below Options.Root).
func (h *Hub) sysctlManager() sysctl.Manager { return sysctl.Manager{Root: h.o.Root} }

// optimizeStatus reads the hub's tuning state.
func (h *Hub) optimizeStatus() (api.OptimizeStatus, error) {
	st, err := h.sysctlManager().Status()
	if err != nil {
		return api.OptimizeStatus{}, err
	}
	out := api.OptimizeStatus{Profile: st.Profile, BBRAvailable: st.BBRAvailable, BBRActive: st.BBRActive}
	if len(st.Applied) > 0 {
		out.Applied = make(map[string]string, len(st.Applied))
		for _, kv := range st.Applied {
			out.Applied[kv.Key] = kv.Value
		}
	}
	return out, nil
}

// OptimizeStatus implements api.Local (section 12): the sysctl profile
// applied on the hub, BBR availability and state, and the values deyroute
// set in /etc/sysctl.d/99-deyroute.conf.
func (l *local) OptimizeStatus(context.Context) (api.OptimizeStatus, error) {
	st, err := l.h.optimizeStatus()
	return st, withLog(err)
}

// ipForwardNeeds reports which machines need net.ipv4.ip_forward=1: the hub
// when a planned rung forwards on the hub (WireGuard/AmneziaWG hub side),
// and every node with such a rung (section 12: only when a wireguard/awg
// transport exists).
func (h *Hub) ipForwardNeeds() (hub bool, nodes map[string]bool) {
	nodes = map[string]bool{}
	for _, c := range h.tun.all() {
		_, plan := c.snapshot()
		for i := range plan.Candidates {
			pc := &plan.Candidates[i]
			hub = hub || pc.Hub.IPForward
			if pc.NodeSide.IPForward {
				nodes[pc.Node] = true
			}
		}
	}
	return hub, nodes
}

// OptimizeApply implements api.Local (`deyroute optimize apply --profile`,
// section 12): the profile is applied on the hub (backup of the previous
// values, /etc/sysctl.d/99-deyroute.conf, BBR only when the kernel has it —
// otherwise a warning), tuning.sysctl_profile is saved, and every online
// node applies the same profile (sysctl.apply). "off" reverts everything.
func (l *local) OptimizeApply(ctx context.Context, profile string) (api.OptimizeStatus, error) {
	h := l.h
	profile = strings.TrimSpace(profile)
	if !slices.Contains(sysctl.Profiles, profile) {
		return api.OptimizeStatus{}, withLog(deyerr.New(deyerr.C013, deyerr.Params{
			"field": "tuning.sysctl_profile", "value": profile, "allowed": strings.Join(sysctl.Profiles, ", "),
		}))
	}
	if _, err := h.autoBackup(); err != nil {
		return api.OptimizeStatus{}, withLog(err)
	}
	cfg := h.Config()
	bbr := cfg.Tuning == nil || cfg.Tuning.BBR
	hubFwd, nodeFwd := h.ipForwardNeeds()
	applied, warnings, err := h.sysctlManager().ApplyWith(sysctl.ApplyOptions{Profile: profile, BBR: bbr, IPForward: hubFwd})
	if err != nil {
		return api.OptimizeStatus{}, withLog(err)
	}
	for _, w := range warnings {
		h.log.Warn("sysctl: "+w, slog.String("profile", profile))
	}
	if _, err := h.mutate(func(c *config.Config) error {
		if c.Tuning == nil {
			c.Tuning = config.DefaultTuning()
		}
		c.Tuning.SysctlProfile = profile
		return nil
	}); err != nil {
		return api.OptimizeStatus{}, withLog(err)
	}
	warnings = append(warnings, h.optimizeNodes(ctx, profile, nodeFwd)...)
	st, err := h.optimizeStatus()
	if err != nil {
		return api.OptimizeStatus{}, withLog(err)
	}
	st.Warnings = warnings
	h.log.Info("sysctl profile applied", slog.String("profile", profile), slog.Int("keys", len(applied)),
		slog.Int("warnings", len(warnings)))
	h.opsEvent("", "sysctl profile "+profile+" applied")
	return st, nil
}

// OptimizeRevert implements api.Local (`deyroute optimize revert`): the hub
// returns to the values saved before deyroute's first change (the conf file
// and the backup go), tuning.sysctl_profile becomes off and every online
// node reverts too.
func (l *local) OptimizeRevert(ctx context.Context) (api.OptimizeStatus, error) {
	h := l.h
	if _, err := h.autoBackup(); err != nil {
		return api.OptimizeStatus{}, withLog(err)
	}
	if err := h.sysctlManager().Revert(); err != nil {
		return api.OptimizeStatus{}, withLog(err)
	}
	if _, err := h.mutate(func(c *config.Config) error {
		if c.Tuning == nil {
			c.Tuning = config.DefaultTuning()
		}
		c.Tuning.SysctlProfile = config.SysctlOff
		return nil
	}); err != nil {
		return api.OptimizeStatus{}, withLog(err)
	}
	warnings := h.optimizeNodes(ctx, config.SysctlOff, nil)
	st, err := h.optimizeStatus()
	if err != nil {
		return api.OptimizeStatus{}, withLog(err)
	}
	st.Warnings = warnings
	h.log.Info("sysctl tuning reverted")
	h.opsEvent("", "sysctl tuning reverted")
	return st, nil
}

// optimizeNodes sends sysctl.apply to every node; nodes that are offline
// or refuse are returned as warnings.
func (h *Hub) optimizeNodes(ctx context.Context, profile string, fwd map[string]bool) []string {
	var warnings []string
	for _, n := range h.Config().Nodes {
		if !h.Online(n.ID) {
			warnings = append(warnings, "node "+n.ID+" is offline: run deyroute optimize apply --profile "+profile+" again when it is back")
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, nodeCallTimeout)
		err := h.Call(cctx, n.ID, api.CmdSysctlApply, api.SysctlArgs{Profile: profile, IPForward: fwd[n.ID]}, nil)
		cancel()
		if err != nil {
			e := deyerr.As(err)
			warnings = append(warnings, "node "+n.ID+": "+string(e.Code)+" "+e.Message())
			h.log.Warn("node did not apply the sysctl profile", dlog.Node(n.ID), dlog.Err(err))
		}
	}
	return warnings
}

package node

import (
	"context"
	stderrors "errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	"github.com/localroot4/deyroute/internal/doctor"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/install"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// setHub stores a new hub address (set_hub, node set-hub, section 5 hub
// move) and reconnects after delay.
func (a *agent) setHub(ctx context.Context, addr string, delay time.Duration) error {
	if !config.ValidHostPort(addr) {
		return deyerr.New(deyerr.C013, deyerr.Params{"field": "node.hub_addr", "value": addr,
			"allowed": "host:port of the hub, e.g. 5.6.7.8:44433"})
	}
	set := a.o.SetHubAddr
	if set == nil {
		set = func(_ context.Context, addr string) error { return setup.SetHubAddr(a.o.Root, addr) }
	}
	a.cfgMu.Lock()
	err := set(ctx, addr)
	a.cfgMu.Unlock()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.hubAddr = addr
	a.mu.Unlock()
	a.log.Info("hub address changed; reconnecting", slog.String("hub", addr))
	a.requestReconnect(delay)
	return nil
}

// scheduleUninstall answers uninstall: the removal starts after the answer
// was sent (section 5: the hub uninstalls its online nodes).
func (a *agent) scheduleUninstall() {
	if !a.uninstalling.CompareAndSwap(false, true) {
		a.log.Info("uninstall requested again; the removal is already under way")
		return
	}
	a.log.Warn("uninstall requested by the hub; removing deyroute from this server")
	run := a.o.Uninstall
	if run == nil {
		run = a.uninstall
	}
	a.later(a.o.UninstallDelay, run)
}

// uninstall is the built-in node uninstall (spec section 5): stop and
// remove every tunnel unit, delete table inet deyroute, revert sysctl, disable
// the node service, delete the deyroute paths (backend binaries included) and
// the deyroute system account, and finally stop deyroute-node itself, which ends this process. Every step runs
// even when an earlier one failed; the failures are returned joined.
func (a *agent) uninstall(ctx context.Context) error {
	var errs []error
	step := func(name string, err error) {
		if err != nil {
			a.log.Error("uninstall step failed", slog.String("step", name), dlog.Err(err))
			errs = append(errs, err)
		}
	}
	step("stop units", a.stopAll(ctx))
	if list, err := a.sd.ListInstances(ctx); err == nil {
		for _, u := range list {
			step("remove "+u.Instance, a.sd.RemoveInstance(ctx, u.Instance))
		}
	} else {
		step("list units", err)
	}
	step("firewall", firewall.Remove(ctx, a.o.Runner))
	serr := sysctl.Manager{Root: a.o.Root}.Revert()
	step("sysctl", serr)
	if err := a.sd.Disable(ctx, systemd.NodeUnit); err != nil && !notLoaded(err) {
		step("disable service", err)
	}
	var keep []string
	if serr != nil {
		keep = []string{filepath.Base(config.SysctlBackup)} // a later run can still revert
	}
	step("remove files", install.RemovePaths(a.o.Root, install.UninstallPlanKeeping(a.o.Root, false, keep)))
	_, aerr := setup.RemoveAccount(ctx, a.o.Root, a.o.Runner)
	step("remove account", aerr)
	step("daemon-reload", a.sd.DaemonReload(ctx))
	a.log.Warn("deyroute removed from this server; stopping the node service")
	if _, _, err := a.o.Runner.Run(ctx, "systemctl", []string{"stop", "--no-block", systemd.NodeUnit}, nil); err != nil && !notLoaded(err) {
		step("stop service", err)
	}
	return stderrors.Join(errs...)
}

// sysctlApply answers sysctl.apply (section 12, only after the owner
// agreed on the hub).
func (a *agent) sysctlApply(args api.SysctlArgs) error {
	bbr := true
	if cfg, err := config.Load(a.cfgPath); err == nil && cfg.Tuning != nil {
		bbr = cfg.Tuning.BBR
	}
	applied, warnings, err := sysctl.Manager{Root: a.o.Root}.ApplyWith(sysctl.ApplyOptions{
		Profile: args.Profile, BBR: bbr, IPForward: args.IPForward,
	})
	for _, w := range warnings {
		a.log.Warn("sysctl: "+w, slog.String("profile", args.Profile))
	}
	if err != nil {
		return err
	}
	a.log.Info("sysctl profile applied", slog.String("profile", args.Profile), slog.Int("keys", len(applied)))
	return nil
}

// doctorData collects this node's doctor sections and findings (doctor
// --node, section 13).
func (a *agent) doctorData(ctx context.Context) api.DoctorData {
	col := (&doctor.Collector{Root: a.o.Root, Runner: a.o.Runner, Now: a.o.Now}).Collect(ctx)
	st := a.status()
	f := doctor.Facts{
		Role:     config.RoleNode,
		Status:   st,
		Now:      a.o.Now(),
		Sections: map[string]string{doctor.SectionStatus: doctor.StatusSection(st)},
	}
	for _, err := range tlsutil.CheckSecretPerms(a.path(config.SecretsDir)) {
		e := deyerr.As(err)
		if p, ok := e.Params["path"]; ok {
			f.SecretPermProblems = append(f.SecretPermProblems, fmt.Sprintf("%v (%v)", p, e.Params["mode"]))
			continue
		}
		f.SecretPermProblems = append(f.SecretPermProblems, e.Message())
	}
	col.Apply(&f)
	return api.DoctorData{Role: config.RoleNode, Sections: f.Sections, Findings: doctor.Run(f)}
}

// status is the node's api.Status (Local API Status, doctor).
func (a *agent) status() api.Status {
	now := a.o.Now().UTC()
	a.mu.Lock()
	ns := &api.NodeSelf{
		ID:      a.nodeID,
		HubAddr: a.hubAddr,
		// Connected only once the hub answered the hello on this stream:
		// before that the version check (Compatible) is not known yet.
		Connected:   a.connected && a.helloSeen,
		LastContact: a.lastContact,
		Units:       unitList(a.snap.units),
	}
	if a.hubHello != nil {
		ns.HubVersion = a.hubHello.Version
		ns.Compatible = a.hubHello.Compatible
	}
	a.mu.Unlock()
	if a.o.HubAddrOverride != "" {
		ns.HubAddr = a.o.HubAddrOverride
	}
	st := api.Status{
		Schema:      api.JSONSchemaVersion,
		Role:        config.RoleNode,
		Version:     version.Version,
		GeneratedAt: now,
		NodeSelf:    ns,
		Tunnels:     []api.TunnelInfo{},
		Nodes:       []api.NodeInfo{},
		Events:      []state.Event{},
	}
	if !ns.Connected {
		e := deyerr.New(deyerr.N009, deyerr.Params{"addr": ns.HubAddr})
		st.Warnings = append(st.Warnings, api.Warning{Code: string(e.Code), Message: e.Message(), Node: a.nodeID})
	}
	if a.hubHelloIncompatible() {
		e := deyerr.New(deyerr.N004, deyerr.Params{"node": a.nodeID, "node_version": st.Version, "hub_version": ns.HubVersion})
		st.Warnings = append(st.Warnings, api.Warning{Code: string(e.Code), Message: e.Message(), Node: a.nodeID})
	}
	return st
}

func (a *agent) hubHelloIncompatible() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hubHello != nil && !a.hubHello.Compatible
}

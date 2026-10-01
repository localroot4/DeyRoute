package node

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/firewall"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Limits of one backend.render payload (section 11: the node only writes
// what a backend needs, nothing arbitrary).
const (
	MaxRenderFiles     = 64
	MaxRenderFileBytes = 4 << 20
	MaxRenderBytes     = 16 << 20
	maxNATRules        = 256
)

var backendNameRe = regexp.MustCompile(`^[a-z0-9]+$`)

// secretValueRe finds the values of secret settings in rendered backend
// files (TOML `token = "…"`, `local_private_key = "…"`, JSON
// `"privateKey": "…"`, YAML `password: …`).
var secretValueRe = regexp.MustCompile(`(?im)(?:^|[\s{,"_.])(?:auth\.token|default_token|token|password|private_?key|psk|secret)"?\s*[:=]\s*"?([^"\s,}]{8,})`)

// clientIDRe finds the client UUIDs of an Xray VLESS inbound: the id is the
// credential that lets a client use the node.
var clientIDRe = regexp.MustCompile(`"id"\s*:\s*"([0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})"`)

// secretEnvRe matches unit environment names whose value is a credential
// (chisel passes its client credentials in AUTH).
var secretEnvRe = regexp.MustCompile(`(?i)auth|token|pass|secret|psk|key`)

// minSecretLen is the shortest value registered as a secret: shorter ones
// ("dey", "info") would mask ordinary words in every log line.
const minSecretLen = 8

// registerRenderedSecrets registers the tunnel tokens, passwords, private
// keys and client ids of a rendered instance (files and unit environment)
// with the log redactor, so they are masked in the agent log, in errors and
// in streamed backend logs (section 11). The hub sends no list of secrets,
// so they are recognised by the settings that hold them.
func registerRenderedSecrets(files map[string][]byte, env map[string]string) {
	register := func(v string) {
		if len(v) >= minSecretLen {
			dlog.RegisterSecret(v)
		}
	}
	for _, data := range files {
		for _, m := range secretValueRe.FindAllSubmatch(data, -1) {
			register(string(m[1]))
		}
		for _, m := range clientIDRe.FindAllSubmatch(data, -1) {
			register(string(m[1]))
		}
	}
	for k, v := range env {
		if !secretEnvRe.MatchString(k) {
			continue
		}
		register(v)
		// "user:password" (chisel AUTH): the password alone is secret too.
		if _, pw, ok := strings.Cut(v, ":"); ok {
			register(pw)
		}
	}
}

// deniedEnv reports environment names a unit may not set: they make the
// dynamic loader or the C library load code or data chosen by whoever sets
// them, which would bypass the rule that units only run deyroute's own
// binaries.
func deniedEnv(name string) bool {
	u := strings.ToUpper(name)
	for _, p := range []string{"LD_", "MALLOC_"} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	switch u {
	case "GLIBC_TUNABLES", "GCONV_PATH", "LOCPATH", "NLSPATH", "HOSTALIASES", "RESOLV_HOST_CONF":
		return true
	}
	return false
}

// selfSubcommands are the deyroute subcommands a unit may run: the built-in
// relay (direct/native) and the WireGuard setup (wireguard/kernel).
var selfSubcommands = map[string]bool{"relay": true, "wg": true}

// sshPort is never redirected by the node table (section 11: the node
// table never opens or closes other ports; losing SSH locks the owner out).
const sshPort = 22

// instance is what the node remembers about one rendered deyroute-tun@
// instance: where its files are, which backend it belongs to and the NAT
// rules that exist while it is started.
type instance struct {
	Tunnel    string            `json:"tunnel"`
	ConfigDir string            `json:"config_dir"`
	Backend   string            `json:"backend"`
	NAT       []backend.NATRule `json:"nat,omitempty"`
	IPForward bool              `json:"ip_forward,omitempty"`
}

// nodeState is persisted in StateFile.
type nodeState struct {
	Instances map[string]instance `json:"instances"`
	// HubNAT are NAT rules the hub set with firewall.apply.
	HubNAT []backend.NATRule `json:"hub_nat,omitempty"`
	// Echo are the loopback ports of canary echoes started with echo.start
	// and not stopped yet: they are opened again when the agent restarts
	// (self.update, crash), since the canary unit keeps running.
	Echo []int `json:"echo,omitempty"`
}

// loadState reads StateFile; a missing or damaged file starts empty.
func (a *agent) loadState() {
	data, err := os.ReadFile(a.path(StateFile))
	if err != nil {
		if !stderrors.Is(err, fs.ErrNotExist) {
			a.log.Warn("cannot read the node state file; starting empty", dlog.Err(err))
		}
		return
	}
	var st nodeState
	if err := json.Unmarshal(data, &st); err != nil {
		a.log.Warn("node state file is damaged; starting empty", dlog.Err(err))
		return
	}
	if st.Instances == nil {
		st.Instances = map[string]instance{}
	}
	a.instMu.Lock()
	a.st = st
	a.instMu.Unlock()
}

// saveState writes StateFile atomically (0600); the caller holds instMu.
func (a *agent) saveState() error {
	data, err := json.MarshalIndent(a.st, "", "  ")
	if err != nil {
		return deyerr.Wrap(deyerr.X000, err, nil)
	}
	p := a.path(StateFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	if err := config.WriteFileAtomic(p, append(data, '\n'), 0o600); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	return nil
}

// refuse is the error for a command the node will not execute.
func (a *agent) refuse(command, reason string) error {
	return deyerr.New(deyerr.N050, deyerr.Params{"node": a.nodeID, "command": command, "reason": reason})
}

// checkInstance validates an instance name the hub sent and returns it
// parsed. Instances of other nodes are refused.
func (a *agent) checkInstance(command, inst string) (systemd.Instance, error) {
	if !systemd.ValidInstance(inst) {
		return systemd.Instance{}, a.refuse(command, fmt.Sprintf("invalid instance name %q", inst))
	}
	in, err := systemd.ParseInstance(inst)
	if err != nil {
		return systemd.Instance{}, a.refuse(command, fmt.Sprintf("invalid instance name %q", inst))
	}
	if !in.Canary && in.Node != a.nodeID {
		return systemd.Instance{}, a.refuse(command, fmt.Sprintf("instance %s belongs to node %s, this is %s", inst, in.Node, a.nodeID))
	}
	return in, nil
}

// checkConfigDir validates the config directory of instance in and returns
// the backend name (its first element): /etc/deyroute/backends/<backend>/
// <tunnel>/<node>/<transport> for a warm unit, .../<backend>/<tunnel>/canary
// for the canary.
func (a *agent) checkConfigDir(command, dir string, in systemd.Instance) (string, error) {
	bad := func(why string) (string, error) {
		return "", a.refuse(command, fmt.Sprintf("config directory %q: %s", dir, why))
	}
	if err := render.CheckConfigDir(dir); err != nil {
		return bad("must be a clean absolute path below " + config.BackendsConfDir + "/<backend>/<tunnel>/")
	}
	parts := strings.Split(strings.TrimPrefix(dir, config.BackendsConfDir+"/"), "/")
	b := parts[0]
	if !backendNameRe.MatchString(b) {
		return bad("invalid backend name")
	}
	if parts[1] != in.Tunnel {
		return bad("it does not belong to tunnel " + in.Tunnel)
	}
	if in.Canary {
		if len(parts) != 3 || parts[2] != render.CanaryDirName {
			return bad("the canary directory is " + render.CanaryConfigDir(b, in.Tunnel))
		}
		return b, nil
	}
	ib, it, _ := strings.Cut(in.Transport, "/")
	if want := render.ConfigDir(ib, in.Tunnel, in.Node, it); dir != want {
		return bad("instance " + in.String() + " renders into " + want)
	}
	return b, nil
}

// checkCommands allows only deyroute's own binaries in a unit of backend b:
// that backend's binaries installed by install.Layout
// (/var/lib/deyroute/bin/<b>/<version>/<binary>) and the deyroute binary itself
// with its data-plane subcommands (built-in relay, WireGuard setup). The
// environment may not redirect the dynamic loader.
func (a *agent) checkCommands(command, b string, u backend.UnitSpec) error {
	if len(u.ExecStart) == 0 {
		return a.refuse(command, "the unit has no ExecStart")
	}
	argvs := append([][]string{u.ExecStart}, u.ExecStartPre...)
	argvs = append(argvs, u.ExecStop...)
	for _, argv := range argvs {
		if len(argv) == 0 {
			return a.refuse(command, "empty unit command")
		}
		if !a.allowedCommand(b, argv) {
			// Only the program is named: arguments may carry settings.
			return a.refuse(command, fmt.Sprintf("unit command %q is not a %s binary or a deyroute relay/wg command", argv[0], b))
		}
	}
	for k := range u.Env {
		if deniedEnv(k) {
			return a.refuse(command, fmt.Sprintf("the unit may not set the environment variable %s", k))
		}
	}
	if err := systemd.ValidateUnitSpec(u); err != nil {
		return a.refuse(command, deyerr.As(err).Message())
	}
	return nil
}

// allowedCommand reports whether argv may run in a unit of backend b.
func (a *agent) allowedCommand(b string, argv []string) bool {
	p := argv[0]
	if path.Clean(p) != p {
		return false
	}
	if p == a.o.SelfBinary {
		return len(argv) > 1 && selfSubcommands[argv[1]]
	}
	rel, ok := strings.CutPrefix(p, config.BinDir+"/")
	if !ok {
		return false
	}
	parts := strings.Split(rel, "/")
	return len(parts) == 3 && parts[0] == b && parts[1] != "" && parts[2] != "" &&
		!strings.HasPrefix(parts[1], ".") && !strings.HasPrefix(parts[2], ".")
}

// checkFiles bounds the rendered files.
func (a *agent) checkFiles(command string, files map[string][]byte) error {
	if len(files) > MaxRenderFiles {
		return a.refuse(command, fmt.Sprintf("%d files (at most %d)", len(files), MaxRenderFiles))
	}
	total := 0
	for name, data := range files {
		if name == "" || !filepath.IsLocal(name) || filepath.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
			return a.refuse(command, fmt.Sprintf("file name %q is not a plain relative path", name))
		}
		if len(data) > MaxRenderFileBytes {
			return a.refuse(command, fmt.Sprintf("file %s is %d bytes (at most %d)", name, len(data), MaxRenderFileBytes))
		}
		total += len(data)
	}
	if total > MaxRenderBytes {
		return a.refuse(command, fmt.Sprintf("the files total %d bytes (at most %d)", total, MaxRenderBytes))
	}
	return nil
}

// checkNAT validates NAT rules for the node table.
func (a *agent) checkNAT(command string, rules []backend.NATRule) error {
	if len(rules) > maxNATRules {
		return a.refuse(command, fmt.Sprintf("%d NAT rules (at most %d)", len(rules), maxNATRules))
	}
	if err := (firewall.Spec{NAT: rules}).Validate(); err != nil {
		return a.refuse(command, "invalid NAT rules: "+deyerr.As(err).Why())
	}
	var local []netip.Addr
	for _, r := range rules {
		hi := r.DportHigh
		if hi == 0 {
			hi = r.DportLow
		}
		// A rule bound to a tunnel interface only sees tunnel traffic; one
		// without an interface applies to every packet the node receives.
		if r.Iface == "" && strings.EqualFold(r.Proto, firewall.ProtoTCP) && r.DportLow <= sshPort && sshPort <= hi {
			return a.refuse(command, fmt.Sprintf("a NAT rule for tcp ports %d-%d would redirect SSH (port %d)", r.DportLow, hi, sshPort))
		}
		if r.ToAddr == "" {
			continue // redirect to a local port
		}
		// The node never forwards to another host (section 11: no open
		// proxy): a DNAT may only lead to loopback or an address of this
		// node.
		to, err := netip.ParseAddr(r.ToAddr) // valid: checked by Validate above
		if err != nil {
			return a.refuse(command, fmt.Sprintf("NAT target %q is not an IP address", r.ToAddr))
		}
		to = to.Unmap()
		if to.IsLoopback() {
			continue
		}
		if local == nil {
			if local, err = a.o.LocalAddrs(); err != nil {
				return a.refuse(command, "the addresses of this node cannot be read: "+err.Error())
			}
		}
		if !slices.Contains(local, to) {
			return a.refuse(command, fmt.Sprintf("NAT target %s is not an address of this node; the node never forwards to other hosts", to))
		}
	}
	return nil
}

// interfaceAddrs returns the addresses of this host's interfaces (the
// default Options.LocalAddrs).
func interfaceAddrs() ([]netip.Addr, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if x, ok := netip.AddrFromSlice(ip); ok {
			out = append(out, x.Unmap())
		}
	}
	return out, nil
}

// backendRender writes one instance's files (dirs 0750, files 0640,
// root:deyroute) and its drop-in, reloads systemd when something changed and
// remembers the instance's NAT rules.
func (a *agent) backendRender(ctx context.Context, args api.BackendRenderArgs) error {
	const cmd = api.CmdBackendRender
	if !config.ValidID(args.Tunnel) {
		return a.refuse(cmd, fmt.Sprintf("invalid tunnel id %q", args.Tunnel))
	}
	in, err := a.checkInstance(cmd, args.Instance)
	if err != nil {
		return err
	}
	if in.Tunnel != args.Tunnel {
		return a.refuse(cmd, fmt.Sprintf("instance %s does not belong to tunnel %s", args.Instance, args.Tunnel))
	}
	b, err := a.checkConfigDir(cmd, args.ConfigDir, in)
	if err != nil {
		return err
	}
	if err := a.checkFiles(cmd, args.Files); err != nil {
		return err
	}
	if err := a.checkCommands(cmd, b, args.Unit); err != nil {
		return err
	}
	if wd := args.Unit.WorkingDirectory; wd != "" && wd != args.ConfigDir {
		return a.refuse(cmd, fmt.Sprintf("working directory %q is not the config directory", wd))
	}
	if err := a.checkNAT(cmd, args.NAT); err != nil {
		return err
	}
	registerRenderedSecrets(args.Files, args.Unit.Env)
	dropIn := systemd.RenderDropIn(args.Unit, args.ConfigDir, systemd.TunnelLogFile(args.Tunnel))

	a.instMu.Lock()
	defer a.instMu.Unlock()
	changed, err := a.writer.Write(ctx, render.Side{
		Instance: args.Instance, ConfigDir: args.ConfigDir, Files: args.Files, Unit: args.Unit, DropIn: dropIn,
	})
	if err != nil {
		return err
	}
	if changed {
		if err := a.sd.DaemonReload(ctx); err != nil {
			return err
		}
	}
	if err := a.ensureTunnelLogDir(); err != nil {
		return err
	}
	rec := instance{Tunnel: args.Tunnel, ConfigDir: args.ConfigDir, Backend: b,
		NAT: append([]backend.NATRule(nil), args.NAT...), IPForward: args.IPForward}
	old := a.st.Instances[args.Instance]
	a.st.Instances[args.Instance] = rec
	if err := a.saveState(); err != nil {
		return err
	}
	a.log.Info("instance rendered", dlog.Tunnel(args.Tunnel), slog.String("instance", args.Instance), slog.Bool("changed", changed))
	if a.started[args.Instance] && (natKey(old.NAT) != natKey(rec.NAT) || old.IPForward != rec.IPForward) {
		return a.syncFirewall(ctx, false)
	}
	return nil
}

// backendRemove stops the instance, runs its PostStop hook, removes the
// unit, its drop-in and its config directory, and forgets it.
func (a *agent) backendRemove(ctx context.Context, args api.BackendRemoveArgs) error {
	const cmd = api.CmdBackendRemove
	in, err := a.checkInstance(cmd, args.Instance)
	if err != nil {
		return err
	}
	b, err := a.checkConfigDir(cmd, args.ConfigDir, in)
	if err != nil {
		return err
	}
	a.instMu.Lock()
	defer a.instMu.Unlock()
	unit := systemd.UnitName(args.Instance)
	if err := a.stopUnit(ctx, unit); err != nil {
		return err
	}
	rec, known := a.st.Instances[args.Instance]
	if !known {
		rec = instance{Tunnel: in.Tunnel, ConfigDir: args.ConfigDir, Backend: b}
	}
	hookErr := a.o.Hooks.PostStop(ctx, rec.Backend, rec.ConfigDir)
	if hookErr != nil {
		a.log.Warn("post-stop step failed", slog.String("instance", args.Instance), dlog.Err(hookErr))
	}
	if err := a.writer.Remove(ctx, args.Instance, args.ConfigDir); err != nil {
		return err
	}
	wasStarted := a.started[args.Instance]
	delete(a.started, args.Instance)
	delete(a.pids, args.Instance)
	delete(a.st.Instances, args.Instance)
	if err := a.saveState(); err != nil {
		return err
	}
	a.log.Info("instance removed", dlog.Tunnel(in.Tunnel), slog.String("instance", args.Instance))
	if wasStarted && (len(rec.NAT) > 0 || rec.IPForward) {
		return a.syncFirewall(ctx, false)
	}
	return nil
}

// stopUnit stops a unit; a unit systemd does not know is already stopped.
func (a *agent) stopUnit(ctx context.Context, unit string) error {
	err := a.sd.Stop(ctx, unit)
	if err != nil && notLoaded(err) {
		return nil
	}
	return err
}

// notLoaded reports "no such unit" from systemctl (exit status 5).
func notLoaded(err error) bool {
	var xe *exec.ExitError
	if !stderrors.As(err, &xe) {
		return false
	}
	return xe.Code == 5 || strings.Contains(xe.Stderr, "not loaded") || strings.Contains(xe.Stderr, "not found")
}

// record returns what is known about inst; known is false for an instance
// the state file does not list (it was lost or damaged). A warm unit's
// backend and config directory follow from its name, so the backend hooks
// (awg device setup and cleanup) still run for it; its NAT rules are
// unknown. The caller holds instMu.
func (a *agent) record(inst string, in systemd.Instance) (rec instance, known bool) {
	if rec, ok := a.st.Instances[inst]; ok {
		return rec, true
	}
	rec = instance{Tunnel: in.Tunnel}
	if !in.Canary && in.Transport != "" {
		b, tr, _ := strings.Cut(in.Transport, "/")
		rec.Backend, rec.ConfigDir = b, render.ConfigDir(b, in.Tunnel, in.Node, tr)
	}
	return rec, false
}

// parseInstance parses a listed or remembered instance name; an
// unparsable one yields an empty Instance (no hooks, no NAT).
func parseInstance(inst string) systemd.Instance {
	in, err := systemd.ParseInstance(inst)
	if err != nil {
		return systemd.Instance{}
	}
	return in
}

// unitStart starts one instance: PreStart hook, systemctl start, a short
// state check (DEY-B003 with the last 40 log lines when the unit failed),
// NAT of the node table, then the PostStart hook.
func (a *agent) unitStart(ctx context.Context, args api.UnitArgs, restart bool) (api.UnitStatus, error) {
	cmd := api.CmdUnitStart
	if restart {
		cmd = api.CmdUnitRestart
	}
	in, err := a.checkInstance(cmd, args.Instance)
	if err != nil {
		return api.UnitStatus{}, err
	}
	a.instMu.Lock()
	defer a.instMu.Unlock()
	rec, known := a.record(args.Instance, in)
	unit := systemd.UnitName(args.Instance)
	if rec.Backend != "" {
		if err := a.o.Hooks.PreStart(ctx, rec.Backend, rec.ConfigDir); err != nil {
			return api.UnitStatus{}, err
		}
	}
	if err := a.ensureTunnelLogDir(); err != nil {
		return api.UnitStatus{}, err
	}
	if restart {
		err = a.sd.Restart(ctx, unit)
	} else {
		err = a.sd.Start(ctx, unit)
	}
	if err != nil {
		if ctx.Err() != nil {
			return api.UnitStatus{}, err
		}
		return api.UnitStatus{}, a.startFailed(unit, rec.Tunnel, err)
	}
	if err := sleepCtx(ctx, a.o.StartCheckDelay); err != nil {
		return api.UnitStatus{}, err
	}
	st, err := a.sd.Show(ctx, unit)
	if err != nil {
		return api.UnitStatus{}, err
	}
	if st.Failed() || st.SubState == "auto-restart" {
		return api.UnitStatus{}, a.startFailed(unit, rec.Tunnel, nil)
	}
	wasStarted := a.started[args.Instance]
	a.started[args.Instance] = true
	if known && !wasStarted && (len(rec.NAT) > 0 || rec.IPForward) {
		if err := a.syncFirewall(ctx, false); err != nil {
			return api.UnitStatus{}, err
		}
	}
	if rec.Backend != "" && a.o.Hooks.HasPostStart(rec.Backend) {
		if err := a.o.Hooks.PostStart(ctx, rec.Backend, rec.ConfigDir); err != nil {
			return api.UnitStatus{}, err
		}
		if st2, err := a.sd.Show(ctx, unit); err == nil {
			st = st2
		}
		a.pids[args.Instance] = st.MainPID
	}
	a.log.Info("unit started", dlog.Tunnel(rec.Tunnel), slog.String("unit", unit), slog.Bool("restart", restart))
	return a.unitStatus(st, rec.Tunnel), nil
}

// startFailed builds DEY-B003 with the last 40 (redacted) lines of the
// tunnel log.
func (a *agent) startFailed(unit, tunnel string, cause error) error {
	e := deyerr.New(deyerr.B003, deyerr.Params{"unit": unit})
	if cause != nil {
		e = deyerr.Wrap(deyerr.B003, cause, deyerr.Params{"unit": unit})
	}
	lines := a.logTail(tunnel)
	if len(lines) == 0 && cause != nil {
		lines = []string{dlog.Redact(cause.Error())}
	}
	return e.WithDetail(strings.Join(lines, "\n")).WithLog(systemd.TunnelLogFile(tunnel))
}

// logTail returns the last systemd.FailureLogLines lines of the tunnel log.
func (a *agent) logTail(tunnel string) []string {
	if !config.ValidID(tunnel) {
		return nil
	}
	lines, err := systemd.LogTail(a.path(systemd.TunnelLogFile(tunnel)), systemd.FailureLogLines)
	if err != nil {
		return nil
	}
	return lines
}

// unitStop stops one instance, runs its PostStop hook and removes its NAT.
func (a *agent) unitStop(ctx context.Context, args api.UnitArgs) (api.UnitStatus, error) {
	in, err := a.checkInstance(api.CmdUnitStop, args.Instance)
	if err != nil {
		return api.UnitStatus{}, err
	}
	a.instMu.Lock()
	defer a.instMu.Unlock()
	return a.stopInstance(ctx, args.Instance, in)
}

// stopInstance is unitStop with instMu held.
func (a *agent) stopInstance(ctx context.Context, inst string, in systemd.Instance) (api.UnitStatus, error) {
	rec, known := a.record(inst, in)
	unit := systemd.UnitName(inst)
	if err := a.stopUnit(ctx, unit); err != nil {
		return api.UnitStatus{}, err
	}
	var errs []error
	if rec.Backend != "" {
		if err := a.o.Hooks.PostStop(ctx, rec.Backend, rec.ConfigDir); err != nil {
			a.log.Warn("post-stop step failed", slog.String("instance", inst), dlog.Err(err))
			errs = append(errs, err)
		}
	}
	wasStarted := a.started[inst]
	delete(a.started, inst)
	delete(a.pids, inst)
	if wasStarted && known && (len(rec.NAT) > 0 || rec.IPForward) {
		if err := a.syncFirewall(ctx, false); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return api.UnitStatus{}, errs[0]
	}
	st, err := a.sd.Show(ctx, unit)
	if err != nil {
		return api.UnitStatus{}, err
	}
	a.log.Info("unit stopped", dlog.Tunnel(rec.Tunnel), slog.String("unit", unit))
	return a.unitStatus(st, rec.Tunnel), nil
}

// unitStatusCmd reports one instance; a failed unit carries its log tail.
func (a *agent) unitStatusCmd(ctx context.Context, args api.UnitArgs) (api.UnitStatus, error) {
	in, err := a.checkInstance(api.CmdUnitStatus, args.Instance)
	if err != nil {
		return api.UnitStatus{}, err
	}
	st, err := a.sd.Show(ctx, systemd.UnitName(args.Instance))
	if err != nil {
		return api.UnitStatus{}, err
	}
	a.instMu.Lock()
	rec, _ := a.record(args.Instance, in)
	a.instMu.Unlock()
	return a.unitStatus(st, rec.Tunnel), nil
}

func (a *agent) unitStatus(st systemd.UnitState, tunnel string) api.UnitStatus {
	us := api.UnitStatus{
		Unit: st.Unit, ActiveState: st.ActiveState, SubState: st.SubState,
		MainPID: st.MainPID, NRestarts: st.NRestarts, Since: st.ActiveEnterTimestamp,
	}
	if st.Failed() || st.SubState == "auto-restart" {
		us.LogTail = a.logTail(tunnel)
	}
	return us
}

// stopAll stops every deyroute-tun@ unit on this node (Local API StopAll,
// uninstall).
func (a *agent) stopAll(ctx context.Context) error {
	list, err := a.sd.ListInstances(ctx)
	if err != nil {
		return err
	}
	a.instMu.Lock()
	defer a.instMu.Unlock()
	var errs []error
	for _, u := range list {
		if !isRunning(u.ActiveState) && !a.started[u.Instance] {
			continue
		}
		if _, err := a.stopInstance(ctx, u.Instance, parseInstance(u.Instance)); err != nil {
			errs = append(errs, err)
		}
	}
	return stderrors.Join(errs...)
}

// isRunning reports ActiveStates that hold a process or kernel state.
func isRunning(activeState string) bool {
	switch activeState {
	case "active", "activating", "reloading", "deactivating":
		return true
	}
	return false
}

// nodeFirewall applies the NAT rules the hub set with firewall.apply.
func (a *agent) nodeFirewall(ctx context.Context, args api.NodeFirewallArgs) error {
	if err := a.checkNAT(api.CmdNodeFirewall, args.NAT); err != nil {
		return err
	}
	a.instMu.Lock()
	defer a.instMu.Unlock()
	a.st.HubNAT = append([]backend.NATRule(nil), args.NAT...)
	if err := a.saveState(); err != nil {
		return err
	}
	return a.syncFirewall(ctx, false)
}

// desiredNAT is the union (deduplicated, stable order) of the hub's NAT
// rules and those of every started instance; ipForward reports whether a
// started instance needs forwarding. The caller holds instMu.
func (a *agent) desiredNAT() (rules []backend.NATRule, ipForward bool) {
	seen := map[string]bool{}
	add := func(rs []backend.NATRule) {
		for _, r := range rs {
			k := natRuleKey(r)
			if !seen[k] {
				seen[k] = true
				rules = append(rules, r)
			}
		}
	}
	add(a.st.HubNAT)
	insts := make([]string, 0, len(a.started))
	for inst := range a.started {
		insts = append(insts, inst)
	}
	sort.Strings(insts)
	for _, inst := range insts {
		rec, ok := a.st.Instances[inst]
		if !ok {
			continue
		}
		add(rec.NAT)
		ipForward = ipForward || rec.IPForward
	}
	return rules, ipForward
}

// syncFirewall makes the node's table inet deyroute hold exactly the desired
// NAT rules (and nothing else: no port is opened or closed), or removes the
// table when there are none. force re-applies even when nothing changed.
// The caller holds instMu.
func (a *agent) syncFirewall(ctx context.Context, force bool) error {
	rules, ipForward := a.desiredNAT()
	if ipForward {
		if err := a.ensureIPForward(); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(rules))
	for _, r := range rules {
		keys = append(keys, natRuleKey(r))
	}
	if !force && a.natApply != nil && strings.Join(keys, ";") == strings.Join(a.natApply, ";") {
		return nil
	}
	fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var err error
	if len(rules) == 0 {
		err = firewall.Remove(fctx, a.o.Runner)
	} else {
		err = firewall.Apply(fctx, a.o.Runner, firewall.Spec{NAT: rules})
	}
	if err != nil {
		a.natApply = nil
		return err
	}
	a.natApply = keys
	a.log.Info("node NAT table updated", slog.Int("rules", len(rules)))
	return nil
}

// ensureIPForward sets net.ipv4.ip_forward=1 when an instance needs it
// (runtime only; sysctl.apply persists it). The previous value is kept in
// the sysctl backup, so uninstall restores it.
func (a *agent) ensureIPForward() error {
	return sysctl.Manager{Root: a.o.Root}.Ensure("net.ipv4.ip_forward", "1")
}

func natRuleKey(r backend.NATRule) string {
	return fmt.Sprintf("%s|%d|%d|%s|%d|%s", r.Proto, r.DportLow, r.DportHigh, r.ToAddr, r.ToPort, r.Iface)
}

func natKey(rs []backend.NATRule) string {
	keys := make([]string, 0, len(rs))
	for _, r := range rs {
		keys = append(keys, natRuleKey(r))
	}
	return strings.Join(keys, ";")
}

// ensureTunnelLogDir creates /var/log/deyroute/tunnels (0750): systemd
// refuses to start a unit whose StandardOutput=append: directory is
// missing.
func (a *agent) ensureTunnelLogDir() error {
	p := a.path(systemd.TunnelLogDir)
	if err := os.MkdirAll(p, dlog.DirMode); err != nil {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": p})
	}
	return nil
}

// reconcile runs at start: instances whose unit runs count as started
// (their NAT is kept and their process is watched for PostStart; the
// first monitor refresh records their MainPID) and the node table is
// brought in line.
func (a *agent) reconcile(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := a.sd.ListInstances(rctx)
	if err != nil {
		a.log.Warn("cannot list tunnel units at start", dlog.Err(err))
		return
	}
	a.instMu.Lock()
	defer a.instMu.Unlock()
	for _, u := range list {
		if isRunning(u.ActiveState) {
			a.started[u.Instance] = true
		}
	}
	if err := a.syncFirewall(rctx, true); err != nil {
		a.setLastError(err)
		a.log.Error("cannot bring the node NAT table in line", dlog.Err(err))
	}
}

// sleepCtx waits d or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

package setup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/sysinfo"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Automatic tuning of one host (`deyroute optimize auto`, the setup wizard,
// join and the node agent): the sysctl plan of internal/sysctl plus the
// resource drop-ins of internal/systemd. The hub, the node agent and setup
// share these helpers, so every host computes, shows and applies its plan
// the same way.

// ctlRange is the backend control range every host keeps out of the
// ephemeral ports (net.ipv4.ip_local_reserved_ports).
var ctlRange = strconv.Itoa(config.CtlRangeLow) + "-" + strconv.Itoa(config.CtlRangeHigh)

// NodeReserved are the port entries a node reserves: the backend control
// range (SysctlArgs.Reserved of the hub's tuning).
func NodeReserved() []string { return []string{ctlRange} }

// HubReserved are the port entries the hub reserves: the backend control
// range, the control port, the front port when the front is on, and the
// listen ports of the enabled tunnels (a failover stops one rung and starts
// the next; in between an outgoing connection must not take the port). The
// list is sorted and has no duplicates, so it hashes the same every time.
func HubReserved(cfg *config.Config) []string {
	set := map[int]bool{}
	if cfg != nil && cfg.Hub != nil {
		if cfg.Hub.ControlPort > 0 {
			set[cfg.Hub.ControlPort] = true
		}
		if cfg.Hub.Front.Enabled && cfg.Hub.Front.Port > 0 {
			set[cfg.Hub.Front.Port] = true
		}
	}
	if cfg != nil {
		for _, t := range cfg.Tunnels {
			if !t.Enabled {
				continue
			}
			for _, p := range t.Ports {
				set[p.Listen] = true
			}
		}
	}
	ports := make([]int, 0, len(set))
	for p := range set {
		if p > 0 && (p < config.CtlRangeLow || p > config.CtlRangeHigh) {
			ports = append(ports, p)
		}
	}
	slices.Sort(ports)
	out := []string{ctlRange}
	for _, p := range ports {
		out = append(out, strconv.Itoa(p))
	}
	return out
}

// HostTune is the automatic tuning plan of one host.
type HostTune struct {
	// Role is config.RoleHub or config.RoleNode (which daemon drop-in).
	Role string
	// Facts are the measured host facts the plan was computed from.
	Facts sysinfo.Facts
	// Sysctl is the kernel part (sysctl, sysfs, modules).
	Sysctl sysctl.Plan
	// DropIns are the rendered resource drop-ins (system path → content)
	// and DropInChanges the settings that differ from the files on disk.
	DropIns       map[string][]byte
	DropInChanges []api.TuneChange
	// Hash covers the kernel plan and the drop-ins.
	Hash string
}

// PlanHostTune computes the automatic tuning of the host below root for
// role. It reads the host facts, the live kernel values and the drop-in
// files and changes nothing. o is the sysctl input (BBR, IP forwarding, UDP
// rungs, conntrack, reserved ports, BDP); o.Profile is ignored.
func PlanHostTune(root, role string, o sysctl.ApplyOptions) HostTune {
	m := sysctl.Manager{Root: root}
	f := sysinfo.Collect(root)
	p := sysctl.AutoPlan(f, sysctl.AutoInputs{
		BBR: o.BBR, IPForward: o.IPForward, UDPRungs: o.UDPRungs, Conntrack: o.Conntrack,
		Reserved: o.Reserved, BDPBytes: o.BDPBytes, Live: m.Live,
	})
	p.Changes = withoutPendingBoot(m, p.Changes)
	res := systemd.Resources{MemBytes: f.MemBytes, Role: role, NrOpen: nrOpenAfter(m, p)}
	files := systemd.RenderAutoDropIns(res)
	sd := &systemd.Manager{Root: root}
	pending := sd.PendingAutoDropIns(files)
	t := HostTune{Role: role, Facts: f, Sysctl: p, DropIns: files,
		DropInChanges: dropInChanges(root, systemd.AutoSettings(res), files, pending)}
	lines := []string{"host-tune", role, p.Hash}
	for _, path := range pending {
		sum := sha256.Sum256(files[path])
		lines = append(lines, "dropin|"+path+"|"+hex.EncodeToString(sum[:8]))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	t.Hash = hex.EncodeToString(sum[:16])
	return t
}

// withoutPendingBoot drops the changes of keys the kernel does not have yet
// (conntrack keys before nf_conntrack is loaded: they apply at the next
// boot or when the module loads) that 99-deyroute.conf already sets to the
// planned value: they were applied, so a second plan is empty. Desired
// keeps them, so the conf keeps them too.
func withoutPendingBoot(m sysctl.Manager, changes []sysctl.Change) []sysctl.Change {
	conf, err := m.Applied()
	if err != nil || len(conf) == 0 {
		return changes
	}
	set := map[string]string{}
	for _, kv := range conf {
		set[kv.Key] = kv.Value
	}
	out := changes[:0:0]
	for _, c := range changes {
		if _, live := m.Live(c.Key); !live && c.Kind == api.TuneKindSysctl {
			if v, ok := set[c.Key]; ok && v == strings.Join(strings.Fields(c.To), " ") {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// nrOpenAfter is fs.nr_open once p is applied (the drop-ins clamp
// LimitNOFILE to it when it stays below 1048576, e.g. in a container).
func nrOpenAfter(m sysctl.Manager, p sysctl.Plan) uint64 {
	v, _ := m.Get(sysctl.KeyNROpen)
	for _, c := range p.Desired {
		if c.Key == sysctl.KeyNROpen {
			v = c.To
		}
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	return n
}

// dropInReason is the reason of each drop-in directive.
var dropInReason = map[string]i18n.Key{
	"OOMScoreAdjust": i18n.TuneReasonDropinOOM,
	"Slice":          i18n.TuneReasonDropinSlice,
	"MemoryHigh":     i18n.TuneReasonDropinMemoryHigh,
	"Environment":    i18n.TuneReasonDropinGOMEMLIMIT,
	"LimitNOFILE":    i18n.TuneReasonDropinNofile,
}

// dropInChanges lists the drop-in directives of the pending files whose
// value differs from the file on disk, and the files that go.
func dropInChanges(root string, settings []systemd.AutoSetting, files map[string][]byte, pending []string) []api.TuneChange {
	var out []api.TuneChange
	for _, path := range pending {
		cur, _ := os.ReadFile(filepath.Join(root, path)) //nolint:gosec // G304: fixed drop-in path under root
		if _, want := files[path]; !want {
			out = append(out, api.TuneChange{Kind: api.TuneKindDropin, Key: path, From: "present", To: "",
				Reason: i18n.T(i18n.TuneReasonDropinRemove), Effect: api.TuneEffectNextStart})
			continue
		}
		for _, s := range settings {
			if s.File != path {
				continue
			}
			from := directive(cur, s.Key)
			if from == s.Value {
				continue
			}
			out = append(out, api.TuneChange{Kind: api.TuneKindDropin, Key: s.Unit + " " + s.Key, From: from, To: s.Value,
				Reason: i18n.T(dropInReason[s.Key], s.Value), Effect: s.Effect})
		}
	}
	return out
}

// directive returns the value of the last Key= line of a unit file ("" when
// there is none); a quoted Environment= value is unquoted.
func directive(data []byte, key string) string {
	v := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, key+"="); ok {
			v = strings.Trim(rest, `"`)
		}
	}
	return v
}

// API converts the plan for the Local API (host is "hub" or the node id).
func (t HostTune) API(host string) api.TuneHost {
	f := api.TuneFacts(t.Facts)
	changes := append(t.Sysctl.APIChanges(), t.DropInChanges...)
	return api.TuneHost{Host: host, Role: t.Role, Facts: &f, Changes: changes, Skips: t.Sysctl.APISkips(), Hash: t.Hash}
}

// Changed reports whether applying t changes anything.
func (t HostTune) Changed() bool { return len(t.Sysctl.Changes) > 0 || len(t.DropInChanges) > 0 }

// ApplyHostTune applies a plan computed by PlanHostTune: the kernel part
// through sysctl (backup first, 99-deyroute.conf with header "profile:
// auto", only values that differ are written) and then the resource
// drop-ins with one daemon-reload (no unit is restarted). Applying the same
// plan twice writes nothing the second time. warnings are the skipped items
// and the kernel writes that failed; a drop-in failure is returned as an
// error after the kernel part is in place.
func ApplyHostTune(ctx context.Context, root string, sd *systemd.Manager, t HostTune) (warnings []string, err error) {
	plan := t.Sysctl
	_, warnings, err = sysctl.Manager{Root: root}.ApplyWith(sysctl.ApplyOptions{Profile: config.SysctlAuto, Plan: &plan})
	if err != nil {
		return warnings, err
	}
	if _, err := sd.WriteAutoDropIns(ctx, t.DropIns); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// RemoveAutoTune removes the resource drop-ins of the automatic tuning (one
// daemon-reload when any existed; nothing is restarted). It is called when
// a host leaves the profile auto (another profile, revert).
func RemoveAutoTune(ctx context.Context, sd *systemd.Manager) error {
	_, err := sd.RemoveAutoDropIns(ctx)
	return err
}

// CheckHost is the tuning check of the host below root (`optimize check`,
// the periodic check): the profile in 99-deyroute.conf, the keys whose live
// value drifted or that a later sysctl file overrides at boot, and the
// findings (conntrack or file handles nearly full, BBR or fq not active).
// It changes nothing. Errors: DEY-X000 when the conf cannot be read.
func CheckHost(root, host, role string) (api.TuneHostCheck, error) {
	m := sysctl.Manager{Root: root}
	out := api.TuneHostCheck{Host: host, Role: role, Profile: config.SysctlOff}
	if p, err := m.Current(); err == nil {
		out.Profile = p
	}
	drift, findings, err := m.Check(sysinfo.Collect(root))
	if err != nil {
		return out, err
	}
	for _, d := range drift {
		out.Drift = append(out.Drift, d.API())
	}
	for _, f := range findings {
		out.Findings = append(out.Findings, f.API())
	}
	return out, nil
}

// applyAutoProfile is the sysctl step of setup and join for the profile
// auto: plan (when nil) is computed now for role with reserved ports and
// tuning.bbr, then applied with its drop-ins.
func (e *env) applyAutoProfile(ctx context.Context, role string, plan *HostTune, bbr bool, reserved []string) ([]string, error) {
	t := plan
	if t == nil {
		p := PlanHostTune(e.root, role, sysctl.ApplyOptions{BBR: bbr, Reserved: reserved})
		t = &p
	}
	return ApplyHostTune(ctx, e.root, e.systemd(), *t)
}

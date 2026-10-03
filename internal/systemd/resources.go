package systemd

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Resource drop-ins of the automatic tuning (`deyroute optimize auto`).
//
// They are separate files next to the embedded unit files (which stay
// byte-identical to deploy/systemd): a template drop-in for every
// deyroute-tun@ instance, a slice that bounds the memory of all tunnel
// processes together, and a drop-in for the daemon of the host's role.
// Writing or removing them runs one daemon-reload and never restarts a
// unit.
//
// OOM policy: backends get a POSITIVE OOMScoreAdjust, so on a small VPS a
// runaway backend is the kernel's first victim, not the hub, the node
// agent, journald or sshd; Restart=always and the failover recover it. The
// hub and the node agent keep the kernel default (0), and no TasksMax is
// set (it could only lower systemd's default).
const (
	// AutoDropInName is the file name of the resource drop-ins.
	AutoDropInName = "60-deyroute-auto.conf"
	// TunnelSlice is the slice every deyroute-tun@ instance runs in while
	// the drop-ins are installed (a child of the implicit deyroute.slice).
	TunnelSlice = "deyroute-tunnels.slice"
	// AutoHeader is the first line of every file written by
	// RenderAutoDropIns.
	AutoHeader = "# managed by deyroute optimize auto; removed by optimize revert"

	// TunnelOOMScoreAdjust is the OOMScoreAdjust of the tunnel processes.
	TunnelOOMScoreAdjust = 300
	// TemplateNOFILE is the LimitNOFILE of the embedded unit files.
	TemplateNOFILE = 1048576
	// TunnelMemoryPercent is the share of RAM the tunnel slice may use
	// before the kernel throttles and reclaims it (MemoryHigh; there is no
	// MemoryMax, so nothing is killed by the limit itself).
	TunnelMemoryPercent = 75
)

// Effects of a resource setting, as api.TuneChange.Effect.
const (
	// EffectNow: systemd applies the setting to the running cgroup at the
	// daemon-reload.
	EffectNow = "now"
	// EffectNextStart: the setting applies when the unit starts again.
	EffectNextStart = "next-start"
)

// Resources are the host facts the resource drop-ins are computed from.
type Resources struct {
	// MemBytes is MemTotal (0 = unknown: no memory settings are rendered).
	MemBytes uint64
	// Role is config.RoleHub or config.RoleNode.
	Role string
	// NrOpen is fs.nr_open (0 = unknown). When it is below the units'
	// LimitNOFILE (1048576), systemd cannot raise the limit that far and
	// refuses to start the unit, so the drop-ins lower LimitNOFILE to it
	// (containers, where fs.nr_open cannot be raised).
	NrOpen uint64
}

// AutoSetting is one directive of the resource drop-ins (one line of the
// tuning plan).
type AutoSetting struct {
	// File is the system path of the file (AutoDropInPaths).
	File string
	// Unit is the unit it applies to (deyroute-tun@.service, the slice, the
	// hub or node unit).
	Unit string
	// Section is the unit file section ("Service" or "Slice").
	Section string
	// Key and Value form the directive: Key=Value.
	Key   string
	Value string
	// Effect is EffectNow or EffectNextStart.
	Effect string
}

// Directive returns "Key=Value".
func (s AutoSetting) Directive() string { return s.Key + "=" + s.Value }

// AutoDropInPath returns the system path of the resource drop-in of unit
// (/etc/systemd/system/<unit>.d/60-deyroute-auto.conf).
func AutoDropInPath(unit string) string {
	return filepath.Join(UnitDir, unit+".d", AutoDropInName)
}

// SlicePath returns the system path of the tunnel slice unit.
func SlicePath() string { return filepath.Join(UnitDir, TunnelSlice) }

// AutoDropInPaths lists every file RenderAutoDropIns can produce, for
// either role, sorted. WriteAutoDropIns writes only these and
// RemoveAutoDropIns removes all of them.
func AutoDropInPaths() []string {
	out := []string{
		AutoDropInPath(TunTemplate),
		AutoDropInPath(HubUnit),
		AutoDropInPath(NodeUnit),
		SlicePath(),
	}
	sort.Strings(out)
	return out
}

// HubGOMEMLIMIT returns the hub's Go memory limit for a host with mem bytes
// of RAM: 128 MiB up to 1 GiB, 256 MiB up to 4 GiB, 512 MiB above (the
// embedded unit sets 128 MiB).
func HubGOMEMLIMIT(mem uint64) string {
	switch {
	case mem <= 1<<30:
		return "128MiB"
	case mem <= 4<<30:
		return "256MiB"
	}
	return "512MiB"
}

// TunnelMemoryHigh returns the MemoryHigh value of the tunnel slice:
// TunnelMemoryPercent of mem, rounded down to MiB ("" when mem is unknown
// or below 1 MiB after rounding).
func TunnelMemoryHigh(mem uint64) string {
	mib := (mem >> 20) * TunnelMemoryPercent / 100
	if mib == 0 {
		return ""
	}
	return strconv.FormatUint(mib, 10) + "M"
}

// nofileClamp returns LimitNOFILE for a host whose fs.nr_open is nrOpen:
// "" when the template value is valid (or nr_open is unknown).
func nofileClamp(nrOpen uint64) string {
	if nrOpen == 0 || nrOpen >= TemplateNOFILE {
		return ""
	}
	return strconv.FormatUint(nrOpen, 10)
}

// AutoSettings returns the directives of the resource drop-ins of r, in
// file order. It is pure.
func AutoSettings(r Resources) []AutoSetting {
	tun := AutoDropInPath(TunTemplate)
	set := []AutoSetting{{
		File: tun, Unit: TunTemplate, Section: "Service",
		Key: "OOMScoreAdjust", Value: strconv.Itoa(TunnelOOMScoreAdjust), Effect: EffectNextStart,
	}}
	high := TunnelMemoryHigh(r.MemBytes)
	if high != "" {
		set = append(set, AutoSetting{
			File: tun, Unit: TunTemplate, Section: "Service",
			Key: "Slice", Value: TunnelSlice, Effect: EffectNextStart,
		})
	}
	nofile := nofileClamp(r.NrOpen)
	if nofile != "" {
		set = append(set, AutoSetting{
			File: tun, Unit: TunTemplate, Section: "Service",
			Key: "LimitNOFILE", Value: nofile, Effect: EffectNextStart,
		})
	}
	if high != "" {
		set = append(set, AutoSetting{
			File: SlicePath(), Unit: TunnelSlice, Section: "Slice",
			Key: "MemoryHigh", Value: high, Effect: EffectNow,
		})
	}
	var daemon string
	switch r.Role {
	case config.RoleHub:
		daemon = HubUnit
	case config.RoleNode:
		daemon = NodeUnit
	default:
		return set
	}
	file := AutoDropInPath(daemon)
	if daemon == HubUnit && r.MemBytes > 0 {
		set = append(set, AutoSetting{
			File: file, Unit: daemon, Section: "Service",
			Key: "Environment", Value: "GOMEMLIMIT=" + HubGOMEMLIMIT(r.MemBytes), Effect: EffectNextStart,
		})
	}
	if nofile != "" {
		set = append(set, AutoSetting{
			File: file, Unit: daemon, Section: "Service",
			Key: "LimitNOFILE", Value: nofile, Effect: EffectNextStart,
		})
	}
	return set
}

// RenderAutoDropIns renders the resource drop-ins of r: system path →
// content. Each file starts with AutoHeader. It is pure and deterministic.
//
//   - deyroute-tun@.service.d/60-deyroute-auto.conf: OOMScoreAdjust=300,
//     Slice=deyroute-tunnels.slice (RAM known) and LimitNOFILE=<nr_open>
//     when fs.nr_open is below 1048576;
//   - deyroute-tunnels.slice: MemoryHigh = 75 % of RAM for all tunnel
//     processes together (one aggregate bound, no per-unit cap);
//   - hub role, deyroute-hub.service.d/60-deyroute-auto.conf:
//     GOMEMLIMIT=128MiB/256MiB/512MiB by RAM (and the LimitNOFILE clamp);
//   - node role, deyroute-node.service.d/60-deyroute-auto.conf: only the
//     LimitNOFILE clamp when it is needed.
func RenderAutoDropIns(r Resources) map[string][]byte {
	var order []string
	byFile := map[string][]AutoSetting{}
	for _, s := range AutoSettings(r) {
		if _, ok := byFile[s.File]; !ok {
			order = append(order, s.File)
		}
		byFile[s.File] = append(byFile[s.File], s)
	}
	out := make(map[string][]byte, len(order))
	for _, file := range order {
		var b bytes.Buffer
		fmt.Fprintln(&b, AutoHeader)
		set := byFile[file]
		if set[0].Section == "Slice" {
			fmt.Fprintln(&b, "[Unit]")
			fmt.Fprintln(&b, "Description=DEYROUTE tunnel transports")
			fmt.Fprintln(&b, "Documentation=https://github.com/localroot4/deyroute")
			fmt.Fprintln(&b)
		}
		section := ""
		for _, s := range set {
			if s.Section != section {
				section = s.Section
				fmt.Fprintf(&b, "[%s]\n", section)
				if section == "Slice" {
					fmt.Fprintln(&b, "MemoryAccounting=yes")
				}
			}
			if s.Key == "Environment" {
				fmt.Fprintf(&b, "Environment=%s\n", quoteWord(s.Value, false))
				continue
			}
			fmt.Fprintln(&b, s.Directive())
		}
		out[file] = b.Bytes()
	}
	return out
}

// autoPathSet is AutoDropInPaths as a set.
func autoPathSet() map[string]bool {
	set := map[string]bool{}
	for _, p := range AutoDropInPaths() {
		set[p] = true
	}
	return set
}

// PendingAutoDropIns lists the system paths WriteAutoDropIns(files) would
// write or remove, sorted (empty: the host already matches).
func (m *Manager) PendingAutoDropIns(files map[string][]byte) []string {
	var out []string
	for _, p := range AutoDropInPaths() {
		data, want := files[p]
		cur, err := os.ReadFile(filepath.Join(m.root(), p)) //nolint:gosec // G304: fixed paths under Root
		switch {
		case want && (err != nil || !bytes.Equal(cur, data)):
			out = append(out, p)
		case !want && err == nil:
			out = append(out, p)
		}
	}
	return out
}

// WriteAutoDropIns installs the resource drop-ins rendered by
// RenderAutoDropIns (system path → content; 0644, directories 0755),
// removes the ones files no longer contains, and runs one daemon-reload
// when anything changed. It never starts, stops or restarts a unit. A path
// that is not one of AutoDropInPaths returns DEY-X034 before anything is
// written; write failures return DEY-X032.
func (m *Manager) WriteAutoDropIns(ctx context.Context, files map[string][]byte) (changed bool, err error) {
	known := autoPathSet()
	for p := range files {
		if !known[p] {
			return false, deyerr.New(deyerr.X034, deyerr.Params{"field": "drop-in", "value": p})
		}
	}
	var errs []error
	for _, p := range AutoDropInPaths() {
		var c bool
		var werr error
		if data, ok := files[p]; ok {
			c, werr = writeIfChanged(filepath.Join(m.root(), p), data, 0o644)
		} else {
			c, werr = m.removeAuto(p)
		}
		changed = changed || c
		if werr != nil {
			errs = append(errs, werr)
		}
	}
	if changed {
		if rerr := m.DaemonReload(ctx); rerr != nil {
			errs = append(errs, rerr)
		}
	}
	return changed, stderrors.Join(errs...)
}

// RemoveAutoDropIns removes every resource drop-in (and the drop-in
// directories it leaves empty) and runs one daemon-reload when anything
// was removed. It never restarts a unit; the removed settings stop
// applying when each unit starts again (the slice's limit at once).
// Removing what is not there is not an error.
func (m *Manager) RemoveAutoDropIns(ctx context.Context) (changed bool, err error) {
	return m.WriteAutoDropIns(ctx, nil)
}

// removeAuto deletes one resource drop-in and its drop-in directory when
// that is then empty (the per-unit drop-ins of other tools stay).
func (m *Manager) removeAuto(p string) (bool, error) {
	full := filepath.Join(m.root(), p)
	if err := os.Remove(full); err != nil {
		if stderrors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": full})
	}
	if dir := filepath.Dir(full); filepath.Ext(dir) == ".d" {
		if ents, err := os.ReadDir(dir); err == nil && len(ents) == 0 {
			_ = os.Remove(dir)
		}
	}
	return true, nil
}

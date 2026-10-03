package node

import (
	"context"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/sysinfo"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
)

// monitorLoop refreshes the heartbeat measurements every heartbeat
// interval until ctx ends.
func (a *agent) monitorLoop(ctx context.Context) {
	a.refresh(ctx)
	t := time.NewTicker(a.o.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.refresh(ctx)
		}
	}
}

// unitProps are the `systemctl show` properties the monitor reads for
// running units.
type unitProps struct {
	ActiveState   string
	MainPID       int
	MemoryCurrent uint64
}

// refresh measures CPU, RAM and unit states for the next heartbeats and
// re-runs PostStart for units whose process systemd restarted.
func (a *agent) refresh(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, monitorTimeout)
	defer cancel()
	units := map[string]string{}
	var running []string
	list, err := a.sd.ListInstances(rctx)
	if err != nil {
		a.log.Debug("cannot list tunnel units", dlog.Err(err))
	}
	for _, u := range list {
		units[u.Unit] = u.ActiveState
		if isRunning(u.ActiveState) {
			running = append(running, u.Unit)
		}
	}
	props := a.showUnits(rctx, running)
	ram := sysinfo.SelfRSS(a.o.Root)
	for _, p := range props {
		ram += p.MemoryCurrent
	}
	cpu := a.cpu.Sample()
	a.mu.Lock()
	if err != nil {
		// Not a current list: the heartbeat says so and keeps the last one.
		units = a.snap.units
	}
	a.snap = snapshot{units: units, unitsKnown: err == nil, cpu: cpu, ram: ram}
	a.mu.Unlock()
	a.watchPostStart(ctx, props)
}

// showUnits reads ActiveState, MainPID and MemoryCurrent of the given
// units with a single `systemctl show` (best effort: nil on failure).
func (a *agent) showUnits(ctx context.Context, units []string) map[string]unitProps {
	if len(units) == 0 {
		return nil
	}
	args := append([]string{"show", "--property=Id,ActiveState,MainPID,MemoryCurrent"}, units...)
	out, _, err := a.o.Runner.Run(ctx, "systemctl", args, nil)
	if err != nil {
		a.log.Debug("cannot read unit properties", dlog.Err(err))
		return nil
	}
	return parseShowBlocks(out)
}

// parseShowBlocks parses `systemctl show` output of several units: blocks
// of key=value lines separated by blank lines, keyed by Id.
func parseShowBlocks(out []byte) map[string]unitProps {
	res := map[string]unitProps{}
	var id string
	var cur unitProps
	flush := func() {
		if id != "" {
			res[id] = cur
		}
		id, cur = "", unitProps{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "Id":
			id = v
		case "ActiveState":
			cur.ActiveState = v
		case "MainPID":
			cur.MainPID, _ = strconv.Atoi(v)
		case "MemoryCurrent":
			// "[not set]" or the maximum uint64 mean unknown.
			if n, err := strconv.ParseUint(v, 10, 64); err == nil && n < 1<<62 {
				cur.MemoryCurrent = n
			}
		}
	}
	flush()
	return res
}

// postStartTimeout bounds one PostStart re-run of the monitor.
const postStartTimeout = 30 * time.Second

// watchPostStart re-runs PostStart for started instances whose process was
// restarted by systemd (Restart=always): an amneziawg-go device comes back
// unconfigured. The first MainPID seen for an instance is only recorded.
func (a *agent) watchPostStart(ctx context.Context, props map[string]unitProps) {
	var restarted []string
	a.instMu.Lock()
	for inst := range a.started {
		rec, _ := a.record(inst, parseInstance(inst))
		if rec.Backend == "" || !a.o.Hooks.HasPostStart(rec.Backend) {
			continue
		}
		p, ok := props[systemd.UnitName(inst)]
		if !ok || p.ActiveState != "active" || p.MainPID <= 0 {
			continue
		}
		switch prev := a.pids[inst]; {
		case prev == 0:
			a.pids[inst] = p.MainPID
		case prev != p.MainPID:
			restarted = append(restarted, inst)
		}
	}
	a.instMu.Unlock()
	// A pass got both locks: the watchdog may ping (a deadlocked lock
	// never gets here).
	a.markAlive()
	sort.Strings(restarted)
	for _, inst := range restarted {
		a.rerunPostStart(ctx, inst)
		a.markAlive()
	}
}

// rerunPostStart runs PostStart again for inst. It holds instMu like every
// unit command, so it never overlaps a start, stop or removal of the same
// instance, and it re-reads the unit first: the snapshot of the monitor
// may be older than a unit.restart that already configured the new
// process. A failure is retried on the next refresh.
func (a *agent) rerunPostStart(ctx context.Context, inst string) {
	hctx, cancel := context.WithTimeout(ctx, postStartTimeout)
	defer cancel()
	a.instMu.Lock()
	defer a.instMu.Unlock()
	if !a.started[inst] {
		return
	}
	rec, _ := a.record(inst, parseInstance(inst))
	if rec.Backend == "" || !a.o.Hooks.HasPostStart(rec.Backend) {
		return
	}
	st, err := a.sd.Show(hctx, systemd.UnitName(inst))
	if err != nil || st.ActiveState != "active" || st.MainPID <= 0 || st.MainPID == a.pids[inst] {
		return
	}
	a.log.Info("unit process restarted; running its post-start step again", slog.String("instance", inst))
	if err := a.o.Hooks.PostStart(hctx, rec.Backend, rec.ConfigDir); err != nil {
		a.setLastError(err)
		a.log.Error("post-start step failed", slog.String("instance", inst), dlog.Err(err))
		return
	}
	a.pids[inst] = st.MainPID
}

// readTrim returns the trimmed content of a small file ("" on error).
func readTrim(path string) string {
	data, err := os.ReadFile(path) // #nosec G304 -- system information files below Root
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// sysinfo answers the sysinfo command: hostname, os, kernel, arch, cpus,
// mem_total (bytes), uptime (seconds), version and node id.
func (a *agent) sysinfo() map[string]string {
	host := readTrim(a.path("/proc/sys/kernel/hostname"))
	if host == "" {
		host, _ = os.Hostname()
	}
	info := map[string]string{
		"node_id":   a.nodeID,
		"hostname":  host,
		"os":        a.helloV.OS,
		"kernel":    a.helloV.Kernel,
		"arch":      a.o.Arch,
		"cpus":      strconv.Itoa(sysinfo.CPUs(a.o.Root)),
		"mem_total": strconv.FormatUint(sysinfo.Mem(a.o.Root).Total, 10),
		"version":   version.Version,
		"go":        version.GoVersion(),
	}
	if up := readTrim(a.path("/proc/uptime")); up != "" {
		if f, err := strconv.ParseFloat(strings.Fields(up)[0], 64); err == nil {
			info["uptime"] = strconv.FormatInt(int64(f), 10)
		}
	}
	return info
}

// metrics counts established TCP connections whose local port is one of
// args.Ports (the service side of the tunnel's connections on this node),
// from /proc/net/tcp{,6}, with `ss -Htn state established` as fallback.
// Byte counters are not available on the node and stay zero.
func (a *agent) metrics(ctx context.Context, args api.MetricsArgs) api.MetricsResult {
	want := map[int]bool{}
	for _, p := range args.Ports {
		if p > 0 && p <= 65535 {
			want[p] = true
		}
	}
	if len(want) == 0 {
		return api.MetricsResult{}
	}
	n, ok := countEstablishedProc(a.o.Root, want)
	if !ok {
		n = a.countEstablishedSS(ctx, want)
	}
	return api.MetricsResult{ActiveConns: n}
}

// countEstablishedProc counts ESTABLISHED (state 01) sockets of
// /proc/net/tcp and tcp6 with a wanted local port; ok is false when
// neither file could be read.
func countEstablishedProc(root string, want map[int]bool) (n int, ok bool) {
	for _, f := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(root, "/proc/net", f)) // #nosec G304 -- procfs below Root
		if err != nil {
			continue
		}
		ok = true
		for i, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if i == 0 || len(fields) < 4 || fields[3] != "01" {
				continue
			}
			_, portHex, found := strings.Cut(fields[1], ":")
			if !found {
				continue
			}
			b, err := hex.DecodeString(portHex)
			if err != nil || len(b) != 2 {
				continue
			}
			if want[int(b[0])<<8|int(b[1])] {
				n++
			}
		}
	}
	return n, ok
}

// countEstablishedSS parses `ss -Htn state established` (Recv-Q Send-Q
// Local Peer).
func (a *agent) countEstablishedSS(ctx context.Context, want map[int]bool) int {
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, _, err := a.o.Runner.Run(sctx, "ss", []string{"-Htn", "state", "established"}, nil)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		local := f[len(f)-2]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		if p, err := strconv.Atoi(local[i+1:]); err == nil && want[p] {
			n++
		}
	}
	return n
}

// unitList renders the heartbeat units for NodeSelf.Units:
// "<unit> <ActiveState>", sorted.
func unitList(units map[string]string) []string {
	out := make([]string, 0, len(units))
	for u, st := range units {
		out = append(out, u+" "+st)
	}
	sort.Strings(out)
	return out
}

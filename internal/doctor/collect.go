package doctor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/firewall"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/sysctl"
	"github.com/localroot4/deyroute/internal/sysinfo"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// DefaultCommandTimeout bounds each external command the collector runs.
const DefaultCommandTimeout = 15 * time.Second

// Collection limits: keep the bundle small on a 1 GB server (section 12).
const (
	maxLogFiles      = 200
	maxLogTailBytes  = 1 << 20 // per file; long JSON lines may give fewer than 200 lines
	maxCertFileBytes = 1 << 20
	maxShownUnits    = 300
	maxOwnerLookups  = 64
)

// logTotalBudget bounds all log tails together (the hub runs with
// GOMEMLIMIT 128 MB, section 12); a variable so tests can lower it.
var logTotalBudget int64 = 16 << 20

// DiskPaths are the filesystems whose usage the OS section reports.
var DiskPaths = []string{"/", "/var", "/etc"}

// Collector gathers the doctor sections of this server. Every path is
// relative to Root ("/" when empty) and every program runs through Runner
// (exec.NewRunner() when nil), so tests use a fake tree and exec.Fake.
// Section texts are already redacted.
type Collector struct {
	Root   string
	Runner exec.Runner
	// Now is the clock (time.Now when nil); used for certificate expiry
	// and the collection timestamp.
	Now func() time.Time
	// Statfs reports the total and available bytes of the filesystem
	// holding a path (already joined with Root); nil uses statfs(2).
	Statfs func(path string) (total, avail uint64, err error)
	// Timeout bounds each external command (DefaultCommandTimeout when 0).
	Timeout time.Duration
	// TunnelCertFiles maps a tunnel id to a certificate kept outside the
	// secrets directory (tls mode custom: tls.cert_file, an absolute
	// system path); Certs reports its expiry like the others. The daemon
	// fills it from the config. Only the certificate is parsed and nothing
	// of the file is copied into the section.
	TunnelCertFiles map[string]string
	// Config is this server's configuration (role, monitoring, tunnels);
	// nil reads Root/etc/deyroute/config.yaml (best effort: without one the
	// monitoring checks are skipped).
	Config *config.Config
	// StateSize returns the size of state.db counted against
	// state.SizeBudget. The hub passes Store.LiveSize (the data in use:
	// bbolt never shrinks the file, so its size would stay high after the
	// history is pruned); nil uses the file size.
	StateSize func() (int64, error)
}

// Sections of the tuning and monitoring checks.
const (
	// SectionTuning holds the tuning facts, the drift of 99-deyroute.conf,
	// the open-files limits, the state.db size and the files `optimize
	// auto` writes besides 99-deyroute.conf (resource drop-ins,
	// modules-load.d and modprobe.d).
	SectionTuning = "tuning"
	// SectionTraffic holds the state of `table inet deyroute_stats` and its
	// per-tunnel counters (bytes and packets only).
	SectionTraffic = "traffic"
)

// CertExpiry is one certificate found under /etc/deyroute/secrets.
type CertExpiry struct {
	Name     string    // path relative to the secrets directory, e.g. "tls/main/cert.pem"
	Kind     string    // ca | hub | node | tunnel | other
	Tunnel   string    // tunnel id when Kind is tunnel
	Subject  string    // subject common name
	NotAfter time.Time // UTC
}

// Collection is everything Collect gathered: the sections and the
// measurements the rules need (see Collection.Apply).
type Collection struct {
	Sections      map[string]string
	DiskFreePct   map[string]float64 // mount path → percent free (distinct filesystems only)
	MemAvailPct   float64            // 0 when unknown
	UnitStates    map[string]string  // unit → ActiveState
	UnitRestarts  map[string]int     // unit → NRestarts
	BBRActive     bool
	BBRAvailable  bool   // the kernel has tcp_bbr
	BBRApplied    bool   // 99-deyroute.conf sets tcp_congestion_control = bbr
	SysctlProfile string // off|balanced|aggressive|auto; "" when unknown
	Certs         []CertExpiry
	Tune          TuneFacts // tuning and monitoring measurements (R11, R12)
}

// Apply copies the measurements into f and merges the sections (existing
// entries of f.Sections win, so daemon-supplied sections are kept).
func (col Collection) Apply(f *Facts) {
	if f.Sections == nil {
		f.Sections = map[string]string{}
	}
	for k, v := range col.Sections {
		if _, ok := f.Sections[k]; !ok {
			f.Sections[k] = v
		}
	}
	f.DiskFreePct = col.DiskFreePct
	f.MemAvailPct = col.MemAvailPct
	f.UnitStates = col.UnitStates
	f.UnitRestarts = col.UnitRestarts
	f.BBRActive = col.BBRActive
	f.BBRAvailable = col.BBRAvailable
	f.BBRApplied = col.BBRApplied
	f.SysctlProfile = col.SysctlProfile
	f.Certs = col.Certs
	f.Tune = col.Tune
}

func (c *Collector) root() string {
	if c.Root == "" {
		return "/"
	}
	return c.Root
}

// path joins p (an absolute system path) with Root.
func (c *Collector) path(p string) string { return filepath.Join(c.root(), p) }

func (c *Collector) runner() exec.Runner {
	if c.Runner == nil {
		return exec.NewRunner()
	}
	return c.Runner
}

func (c *Collector) now() time.Time {
	if c.Now == nil {
		return time.Now().UTC()
	}
	return c.Now().UTC()
}

func (c *Collector) timeout() time.Duration {
	if c.Timeout <= 0 {
		return DefaultCommandTimeout
	}
	return c.Timeout
}

func (c *Collector) statfs(p string) (uint64, uint64, error) {
	if c.Statfs != nil {
		return c.Statfs(p)
	}
	return statfs(p)
}

// Collect runs every collector and returns the sections plus measurements.
// It never fails: problems are written into the section they concern.
func (c *Collector) Collect(ctx context.Context) Collection {
	osText, disk, mem := c.osInfo()
	unitsText, states, restarts, pids := c.unitsInfo(ctx)
	sysText, bbr, profile := c.sysctlInfo()
	m := sysctl.Manager{Root: c.Root}
	certText, certs := c.certsInfo()
	tune, tuneText, trafficText := c.tuneInfo(ctx, states, pids)
	sections := map[string]string{
		SectionOS:       osText,
		SectionVersions: c.Versions(),
		SectionUnits:    unitsText,
		SectionFirewall: c.Firewall(ctx),
		SectionSysctl:   sysText,
		SectionPorts:    c.Ports(ctx),
		SectionCerts:    certText,
		SectionTuning:   tuneText,
	}
	if trafficText != "" {
		sections[SectionTraffic] = trafficText
	}
	for k, v := range c.Logs() {
		sections[k] = v
	}
	return Collection{
		Sections: sections, DiskFreePct: disk, MemAvailPct: mem,
		UnitStates: states, UnitRestarts: restarts,
		BBRActive: bbr, BBRAvailable: m.BBRAvailable(), BBRApplied: bbrApplied(m),
		SysctlProfile: profile, Certs: certs, Tune: tune,
	}
}

// bbrApplied reports whether the applied profile sets BBR: a profile
// applied without it (no tcp_bbr in the kernel then, or tuning.bbr false)
// leaves the line out of 99-deyroute.conf.
func bbrApplied(m sysctl.Manager) bool {
	kvs, err := m.Applied()
	if err != nil {
		return false
	}
	for _, kv := range kvs {
		if kv.Key == sysctl.KeyCongestion && kv.Value == "bbr" {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- OS

// OS returns the OS section: os-release, kernel, arch, uptime, memory
// (/proc/meminfo) and disk usage of /, /var and /etc.
func (c *Collector) OS() string {
	s, _, _ := c.osInfo()
	return s
}

// DiskFreePct returns the free space of each distinct filesystem among
// DiskPaths in percent.
func (c *Collector) DiskFreePct() map[string]float64 {
	_, d, _ := c.osInfo()
	return d
}

// MemAvailPct returns MemAvailable/MemTotal in percent (0 when unknown).
func (c *Collector) MemAvailPct() float64 {
	_, _, m := c.osInfo()
	return m
}

func (c *Collector) osInfo() (string, map[string]float64, float64) {
	var b strings.Builder
	fmt.Fprintf(&b, "collected: %s\n", c.now().Format(time.RFC3339))
	fmt.Fprintf(&b, "os: %s\n", c.osRelease())
	fmt.Fprintf(&b, "kernel: %s\n", c.readTrim("/proc/sys/kernel/osrelease"))
	arch := runtime.GOARCH
	if m := c.readTrim("/proc/sys/kernel/arch"); m != "unknown" {
		arch += " (" + m + ")"
	}
	fmt.Fprintf(&b, "arch: %s\n", arch)
	fmt.Fprintf(&b, "uptime: %s\n", c.uptime())

	memPct := 0.0
	total, avail, err := c.meminfo()
	if err != nil {
		fmt.Fprintf(&b, "memory: unknown (%v)\n", err)
	} else {
		memPct = pct(avail, total)
		fmt.Fprintf(&b, "memory: total %s, available %s (%.1f%%)\n", human(total), human(avail), memPct)
	}

	disk := map[string]float64{}
	type fsKey struct{ total, avail uint64 }
	seen := map[fsKey]string{}
	for _, p := range DiskPaths {
		t, a, err := c.statfs(c.path(p))
		if err != nil {
			fmt.Fprintf(&b, "disk %s: unknown (%v)\n", p, err)
			continue
		}
		free := pct(a, t)
		if first, dup := seen[fsKey{t, a}]; dup {
			fmt.Fprintf(&b, "disk %s: same filesystem as %s\n", p, first)
			continue
		}
		seen[fsKey{t, a}] = p
		if t > 0 {
			disk[p] = free
		}
		fmt.Fprintf(&b, "disk %s: total %s, free %s (%.1f%%)\n", p, human(t), human(a), free)
	}
	return redactText(b.String()), disk, memPct
}

func (c *Collector) osRelease() string {
	data, err := os.ReadFile(c.path("/etc/os-release"))
	if err != nil {
		return "unknown"
	}
	vals := map[string]string{}
	for _, l := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		vals[k] = strings.Trim(v, `"'`)
	}
	switch {
	case vals["PRETTY_NAME"] != "":
		return vals["PRETTY_NAME"]
	case vals["NAME"] != "":
		return strings.TrimSpace(vals["NAME"] + " " + vals["VERSION"])
	}
	return "unknown"
}

// readTrim reads a small system file; "unknown" when it cannot be read.
func (c *Collector) readTrim(p string) string {
	data, err := os.ReadFile(c.path(p))
	if err != nil {
		return "unknown"
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "unknown"
	}
	return s
}

func (c *Collector) uptime() string {
	f := strings.Fields(c.readTrim("/proc/uptime"))
	if len(f) == 0 {
		return "unknown"
	}
	secs, err := strconv.ParseFloat(f[0], 64)
	if err != nil || secs < 0 {
		return "unknown"
	}
	d := time.Duration(secs) * time.Second
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	h, m := int(d/time.Hour), int((d%time.Hour)/time.Minute)
	return fmt.Sprintf("%dd %02d:%02d", days, h, m)
}

// meminfo returns MemTotal and MemAvailable in bytes.
func (c *Collector) meminfo() (total, avail uint64, err error) {
	data, err := os.ReadFile(c.path("/proc/meminfo"))
	if err != nil {
		return 0, 0, err
	}
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		v, perr := strconv.ParseUint(f[1], 10, 64)
		if perr != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, haveTotal = v*1024, true
		case "MemAvailable:":
			avail, haveAvail = v*1024, true
		}
	}
	if !haveTotal || !haveAvail || total == 0 {
		return 0, 0, errors.New("MemTotal/MemAvailable missing in /proc/meminfo")
	}
	return total, avail, nil
}

func pct(part, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

// human formats bytes as MiB/GiB.
func human(n uint64) string {
	const mib = 1 << 20
	if n >= 10*1024*mib {
		return fmt.Sprintf("%.1f GiB", float64(n)/(1024*mib))
	}
	return fmt.Sprintf("%d MiB", n/mib)
}

// ---------------------------------------------------------------- versions

// Versions returns the deyroute build and the installed backend versions
// (/var/lib/deyroute/bin/<backend>/<version>/).
func (c *Collector) Versions() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", version.String())
	binDir := c.path(config.BinDir)
	entries, err := os.ReadDir(binDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		b.WriteString("backends: none installed\n")
	case err != nil:
		fmt.Fprintf(&b, "backends: cannot list %s: %v\n", config.BinDir, err)
	default:
		b.WriteString("backends:\n")
		for _, e := range entries {
			if !e.IsDir() {
				if e.Name() == filepath.Base(config.PrevBinaryPath) {
					b.WriteString("  previous deyroute binary kept for rollback\n")
				}
				continue
			}
			vers, _ := os.ReadDir(filepath.Join(binDir, e.Name()))
			var names []string
			for _, v := range vers {
				if !v.IsDir() {
					continue
				}
				name := v.Name()
				if u, err := url.PathUnescape(name); err == nil {
					name = u
				}
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) == 0 {
				names = []string{"(no version directory)"}
			}
			fmt.Fprintf(&b, "  %s %s\n", e.Name(), strings.Join(names, ", "))
		}
	}
	return redactText(b.String())
}

// ---------------------------------------------------------------- units

// Units returns the state of deyroute-hub, deyroute-node and every
// deyroute-tun@ instance (systemctl show / list-units).
func (c *Collector) Units(ctx context.Context) string {
	s, _, _, _ := c.unitsInfo(ctx)
	return s
}

// UnitFacts returns ActiveState and NRestarts of the same units.
func (c *Collector) UnitFacts(ctx context.Context) (states map[string]string, restarts map[string]int) {
	_, states, restarts, _ = c.unitsInfo(ctx)
	return states, restarts
}

// unitsInfo returns the units section, ActiveState and NRestarts of every
// unit and the main PID of the running ones.
func (c *Collector) unitsInfo(ctx context.Context) (string, map[string]string, map[string]int, map[string]int) {
	m := &systemd.Manager{Runner: c.runner(), Root: c.Root, Timeout: c.timeout()}
	states, restarts, pids := map[string]string{}, map[string]int{}, map[string]int{}
	var b strings.Builder
	line := func(st systemd.UnitState) {
		states[st.Unit] = st.ActiveState
		restarts[st.Unit] = st.NRestarts
		if st.MainPID > 0 {
			pids[st.Unit] = st.MainPID
		}
		since := "-"
		if !st.ActiveEnterTimestamp.IsZero() {
			since = st.ActiveEnterTimestamp.Format(time.RFC3339)
		}
		sub := st.SubState
		if sub == "" {
			sub = "-"
		}
		res := st.Result
		if res == "" {
			res = "-"
		}
		fmt.Fprintf(&b, "%s  %s/%s  pid=%d  restarts=%d  since=%s  result=%s\n",
			st.Unit, st.ActiveState, sub, st.MainPID, st.NRestarts, since, res)
	}
	for _, u := range []string{systemd.HubUnit, systemd.NodeUnit} {
		st, err := m.Show(ctx, u)
		if err != nil {
			fmt.Fprintf(&b, "%s  error: %v\n", u, err)
			continue
		}
		line(st)
	}
	list, err := m.ListInstances(ctx)
	if err != nil {
		fmt.Fprintf(&b, "deyroute-tun@*  error: %v\n", err)
	}
	for i, st := range list {
		if i >= maxShownUnits {
			fmt.Fprintf(&b, "... %d more instances\n", len(list)-i)
			break
		}
		if st.LoadState != "" && st.LoadState != "not-found" {
			// Loaded by systemd: ask for restarts, PID and timestamps.
			if full, err := m.Show(ctx, st.Unit); err == nil {
				full.LoadState = st.LoadState
				st = full
			}
		}
		line(st)
	}
	return redactText(b.String()), states, restarts, pids
}

// ---------------------------------------------------------------- logs

// Logs returns the last LogTailLines lines of every log file under
// /var/log/deyroute (tunnels/ included), keyed "logs/<relative path>".
// Rotated (.gz) files, symbolic links and anything but single-link regular
// files are skipped. The directory is writable by the unprivileged backend
// user, so every file is opened through os.Root (no path may resolve
// outside the log directory, even if an entry is swapped for a symlink
// after the walk). At most maxLogFiles files and logTotalBudget bytes in
// total are collected.
func (c *Collector) Logs() map[string]string {
	out := map[string]string{}
	root, err := os.OpenRoot(c.path(config.LogDir))
	if err != nil {
		return out // no log directory: nothing to collect
	}
	defer func() { _ = root.Close() }()
	var files []string
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it, keep walking
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".rotating") {
			return nil
		}
		files = append(files, p)
		if len(files) >= maxLogFiles {
			return fs.SkipAll
		}
		return nil
	})
	budget := logTotalBudget
	for _, rel := range files {
		key := LogSectionPrefix + rel
		if budget <= 0 {
			out[key] = "skipped: the doctor log size budget is used up\n"
			continue
		}
		text, err := tailRooted(root, rel, LogTailLines, min(maxLogTailBytes, budget))
		if err != nil {
			text = fmt.Sprintf("cannot read %s: %v\n", path.Join(config.LogDir, rel), err)
		}
		budget -= int64(len(text))
		out[key] = redactText(text)
	}
	return out
}

// tailRooted opens rel inside root (read-only, never blocking) and returns
// its last n lines (tailFile). Only single-link regular files are read.
func tailRooted(root *os.Root, rel string, n int, maxBytes int64) (string, error) {
	f, err := root.OpenFile(rel, os.O_RDONLY|logOpenFlags, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", errors.New("not a regular file")
	}
	if !singleLink(st) {
		return "", errors.New("the file has several hard links")
	}
	return tailFile(f, st.Size(), n, maxBytes)
}

// tailFile returns the last n lines of r (size bytes long), reading
// backwards at most maxBytes. When the limit cuts a line, the partial line
// is dropped.
func tailFile(r io.ReaderAt, size int64, n int, maxBytes int64) (string, error) {
	const chunk = 32 << 10
	off := size
	var buf []byte
	for off > 0 && int64(len(buf)) < maxBytes {
		sz := min(int64(chunk), off, maxBytes-int64(len(buf)))
		off -= sz
		b := make([]byte, sz)
		if _, err := r.ReadAt(b, off); err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		buf = append(b, buf...)
		if bytes.Count(buf, []byte{'\n'}) > n {
			break
		}
	}
	text := string(buf)
	if off > 0 && !lineStartsAt(r, off) {
		// The first line is partial: drop it.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		} else {
			text = ""
		}
	}
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, ""), nil
}

// lineStartsAt reports whether a line starts at offset off of r (the byte
// before it is a newline).
func lineStartsAt(r io.ReaderAt, off int64) bool {
	var prev [1]byte
	n, _ := r.ReadAt(prev[:], off-1)
	return n == 1 && prev[0] == '\n'
}

// ---------------------------------------------------------------- firewall

// Firewall returns the detected firewalls and `nft list table inet deyroute`.
func (c *Collector) Firewall(ctx context.Context) string {
	r := c.runner()
	var b strings.Builder
	kinds := firewall.Detect(ctx, r)
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}
	if len(names) == 0 {
		names = []string{"none"}
	}
	fmt.Fprintf(&b, "detected: %s\n", strings.Join(names, ", "))
	fmt.Fprintf(&b, "--- nft list table %s\n", firewall.TableRef)
	table, err := firewall.Show(ctx, r)
	switch {
	case err != nil:
		fmt.Fprintf(&b, "error: %v\n", err)
	case strings.TrimSpace(table) == "":
		fmt.Fprintf(&b, "(table %s does not exist)\n", firewall.TableRef)
	default:
		b.WriteString(table)
		if !strings.HasSuffix(table, "\n") {
			b.WriteByte('\n')
		}
	}
	return redactText(b.String())
}

// ---------------------------------------------------------------- sysctl

// Sysctl returns the applied profile, BBR state and the kernel value of
// every section 12 key (read from /proc/sys).
func (c *Collector) Sysctl() string {
	s, _, _ := c.sysctlInfo()
	return s
}

func (c *Collector) sysctlInfo() (string, bool, string) {
	m := sysctl.Manager{Root: c.Root}
	var b strings.Builder
	profile, err := m.Current()
	if err != nil {
		fmt.Fprintf(&b, "profile: unknown (%v)\n", err)
		profile = ""
	} else {
		fmt.Fprintf(&b, "profile: %s\n", profile)
	}
	bbr := m.BBRActive()
	fmt.Fprintf(&b, "bbr available: %s\nbbr active: %s\n", yesNo(m.BBRAvailable()), yesNo(bbr))
	for _, key := range SysctlKeys() {
		v, ok := m.Get(key)
		if !ok {
			v = "(missing)"
		}
		fmt.Fprintf(&b, "%s = %s\n", key, v)
	}
	return redactText(b.String()), bbr, profile
}

// SysctlKeys lists every key of the section 12 profiles (aggressive with
// ip_forward is the superset), every key the automatic profile may own,
// the conntrack and file-handle usage and tcp_available_congestion_control.
func SysctlKeys() []string {
	kvs, _ := sysctl.Profile(config.SysctlAggressive, true) // a valid profile name never fails
	keys := make([]string, 0, len(kvs)+24)
	seen := map[string]bool{}
	add := func(k string) {
		if !seen[k] && !strings.HasPrefix(k, "/") {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for _, kv := range kvs {
		add(kv.Key)
	}
	for _, k := range autoKeys() {
		add(k)
	}
	for _, k := range []string{sysctl.KeyFileMax, keyFileNr, sysctl.KeyConntrackCount, sysctl.KeyConntrackBuckets} {
		add(k)
	}
	add(sysctl.KeyAvailableCC)
	return keys
}

// autoKeys is the union of the keys of the automatic profile: the plan of
// a host where everything applies (BBR, fq, conntrack, UDP rungs, reserved
// ports) and no key has a live value, so every candidate shows up (as a
// skip for a missing key).
func autoKeys() []string {
	p := sysctl.AutoPlan(sysinfo.Facts{MemBytes: 8 << 30, CPUs: 4, BBRAvailable: true, FQAvailable: true, ConntrackLoaded: true},
		sysctl.AutoInputs{BBR: true, IPForward: true, UDPRungs: true, Conntrack: true, Reserved: []string{"30000-31999"}})
	var out []string
	for _, c := range p.Desired {
		out = append(out, c.Key)
	}
	for _, c := range p.NotOwned {
		out = append(out, c.Key)
	}
	for _, sk := range p.Skips {
		out = append(out, sk.Key)
	}
	return out
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// ---------------------------------------------------------------- certs

// Certs returns the expiry of every certificate under
// /etc/deyroute/secrets: ca.crt, hub.crt, node.crt and tls/<tunnel>/cert.pem
// (acme/ included), plus the custom certificates of TunnelCertFiles. Key
// files are never opened.
func (c *Collector) Certs() string {
	s, _ := c.certsInfo()
	return s
}

// CertExpiries returns the certificates Certs describes.
func (c *Collector) CertExpiries() []CertExpiry {
	_, certs := c.certsInfo()
	return certs
}

// isCertFile reports whether a secrets file name is a certificate (never a
// key): *.crt or cert.pem.
func isCertFile(name string) bool {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "key") {
		return false
	}
	return strings.HasSuffix(lower, ".crt") || lower == "cert.pem"
}

func certKind(rel string) (kind, tunnel string) {
	parts := strings.Split(rel, "/")
	switch {
	case rel == "ca.crt":
		return "ca", ""
	case rel == "hub.crt":
		return "hub", ""
	case rel == "node.crt":
		return "node", ""
	case len(parts) >= 3 && parts[0] == "tls":
		return "tunnel", parts[1]
	}
	return "other", ""
}

func (c *Collector) certsInfo() (string, []CertExpiry) {
	dir := c.path(config.SecretsDir)
	now := c.now()
	var b strings.Builder
	var certs []CertExpiry
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if rel, _ := filepath.Rel(dir, p); strings.Count(filepath.ToSlash(rel), "/") >= 3 {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && isCertFile(d.Name()) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(&b, "cannot list %s: %v\n", config.SecretsDir, err)
		files = nil
	}
	sort.Strings(files)
	add := func(name, p, kind, tunnel string) {
		data, err := readLimited(p, maxCertFileBytes)
		if err != nil {
			fmt.Fprintf(&b, "%s  cannot read: %v\n", name, err)
			return
		}
		info, err := tlsutil.CertInfo(data)
		if err != nil {
			fmt.Fprintf(&b, "%s  not a certificate: %v\n", name, err)
			return
		}
		certs = append(certs, CertExpiry{Name: name, Kind: kind, Tunnel: tunnel, Subject: info.Subject, NotAfter: info.NotAfter})
		fmt.Fprintf(&b, "%s  kind=%s  subject=%s  not_after=%s  days_left=%d  fingerprint=%s\n",
			name, kind, info.Subject, info.NotAfter.Format(time.RFC3339), info.DaysLeft(now), info.Fingerprint)
	}
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		kind, tunnel := certKind(rel)
		add(rel, p, kind, tunnel)
	}
	for _, tunnel := range sortedKeys(c.TunnelCertFiles) {
		if p := c.TunnelCertFiles[tunnel]; p != "" {
			add(p, c.path(p), "tunnel", tunnel)
		}
	}
	if len(certs) == 0 && b.Len() == 0 {
		b.WriteString("no certificates found\n")
	}
	return redactText(b.String()), certs
}

// readLimited reads at most limit bytes of a file.
func readLimited(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p) // #nosec G304 -- certificate file under the secrets directory
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, limit))
}

// ---------------------------------------------------------------- tuning

// keyFileNr is the file-handle usage: allocated, free, fs.file-max.
const keyFileNr = "fs.file-nr"

// maxTuneFileBytes bounds each file the tuning section copies.
const maxTuneFileBytes = 64 << 10

// udpBackends are the backends whose rungs need large UDP buffers (QUIC,
// AmneziaWG).
var udpBackends = map[string]bool{"hysteria2": true, "awg": true}

// config returns Collector.Config, or the config file below Root; nil when
// there is none or it cannot be read.
func (c *Collector) config() *config.Config {
	if c.Config != nil {
		return c.Config
	}
	data, err := os.ReadFile(c.path(config.DefaultPath))
	if err != nil {
		return nil
	}
	cfg, err := config.Decode(data)
	if err != nil {
		return nil
	}
	return cfg
}

// Tuning returns the tuning section (see SectionTuning).
func (c *Collector) Tuning(ctx context.Context) string {
	_, states, _, pids := c.unitsInfo(ctx)
	_, s, _ := c.tuneInfo(ctx, states, pids)
	return s
}

// tuneInfo measures what the tuning and monitoring checks need and renders
// the tuning section and, on a hub, the traffic section ("" otherwise).
// states and pids are the units' ActiveState and main PIDs (unitsInfo).
func (c *Collector) tuneInfo(ctx context.Context, states map[string]string, pids map[string]int) (TuneFacts, string, string) {
	m := sysctl.Manager{Root: c.Root}
	facts := sysinfo.Collect(c.Root)
	cfg := c.config()
	t := TuneFacts{Virt: facts.Virt, NIC: facts.NIC, Qdisc: facts.Qdisc, StateDBBudget: state.SizeBudget}
	if facts.ConntrackLoaded {
		t.ConntrackCount, t.ConntrackMax = facts.ConntrackCount, facts.ConntrackMax
	}
	var b strings.Builder
	fmt.Fprintf(&b, "virtualization: %s\n", orNone(facts.Virt))
	fmt.Fprintf(&b, "default route: %s  mtu=%d  qdisc=%s\n", orNone(facts.NIC), facts.NICMTU, orNone(facts.Qdisc))
	if facts.ConntrackLoaded {
		fmt.Fprintf(&b, "conntrack: %d of %d entries\n", facts.ConntrackCount, facts.ConntrackMax)
	} else {
		b.WriteString("conntrack: not loaded\n")
	}
	if v, ok := m.Get(keyFileNr); ok {
		if fl := strings.Fields(v); len(fl) == 3 {
			t.FilesUsed, _ = strconv.ParseUint(fl[0], 10, 64)
			t.FilesMax, _ = strconv.ParseUint(fl[2], 10, 64)
		}
		fmt.Fprintf(&b, "file handles: %d of %d\n", t.FilesUsed, t.FilesMax)
	}
	t.NROpen = sysctlUint(m, sysctl.KeyNROpen)
	t.RmemMax = sysctlUint(m, sysctl.KeyRmemMax)

	// 99-deyroute.conf: profile, drift, boot overrides, other findings.
	if _, err := os.Stat(m.ConfPath()); err != nil {
		fmt.Fprintf(&b, "%s: none (kernel tuning off)\n", config.SysctlConfPath)
	} else {
		t.Profile, _ = m.Current()
		if kvs, err := m.Applied(); err == nil {
			for _, kv := range kvs {
				if kv.Key == sysctl.KeyDefaultQdisc && kv.Value == "fq" {
					t.QdiscFQ = true
				}
			}
		}
		drift, findings, err := m.Check(facts)
		switch {
		case err != nil:
			fmt.Fprintf(&b, "drift: cannot check: %v\n", err)
		case len(drift) == 0:
			b.WriteString("drift: none\n")
		}
		for _, d := range drift {
			t.Drift = append(t.Drift, d.API())
			fmt.Fprintf(&b, "drift: %s want=%q live=%q", d.Key, d.Want, d.Live)
			if d.OverriddenBy != "" {
				fmt.Fprintf(&b, " overridden at boot by %s", d.OverriddenBy)
			}
			b.WriteByte('\n')
		}
		for _, fd := range findings {
			fmt.Fprintf(&b, "check %s (%s): %s\n", fd.Check, fd.Severity, fd.Message())
		}
	}

	// Open-files limits against fs.nr_open.
	fmt.Fprintf(&b, "fs.nr_open: %d\n", t.NROpen)
	for _, unit := range []string{systemd.HubUnit, systemd.NodeUnit, systemd.TunTemplate} {
		if n, ok := c.unitNofile(unit); ok {
			t.Nofile = append(t.Nofile, NofileLimit{Unit: unit, Limit: n})
			fmt.Fprintf(&b, "LimitNOFILE %s: %d\n", unit, n)
		}
	}
	for _, unit := range sortedKeys(pids) {
		if n, ok := c.processNofile(pids[unit]); ok {
			t.Nofile = append(t.Nofile, NofileLimit{Unit: unit, PID: pids[unit], Limit: n})
			fmt.Fprintf(&b, "open files %s (pid %d): %d\n", unit, pids[unit], n)
		}
	}

	// UDP rungs: in the hub's ladders or running here.
	t.UDPRungs = udpRungs(cfg, states)
	fmt.Fprintf(&b, "udp rungs: %s  net.core.rmem_max: %d\n", yesNo(t.UDPRungs), t.RmemMax)

	// state.db against its budget.
	if size, how := c.stateSize(); size > 0 {
		t.StateDBBytes = size
		fmt.Fprintf(&b, "state.db: %s of %s (%s)\n", sizeIEC64(size), sizeIEC64(t.StateDBBudget), how)
	}

	// The files optimize auto writes besides 99-deyroute.conf.
	files := append(systemd.AutoDropInPaths(), sysctl.PathModulesLoad, sysctl.PathModprobe)
	copied := false
	for _, p := range files {
		data, err := readLimited(c.path(p), maxTuneFileBytes)
		if err != nil {
			continue
		}
		copied = true
		fmt.Fprintf(&b, "--- %s\n%s", p, data)
		if len(data) > 0 && data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
	}
	if !copied {
		b.WriteString("no resource drop-ins, modules-load.d or modprobe.d files of deyroute\n")
	}

	traffic := ""
	if cfg != nil && cfg.Role == config.RoleHub {
		traffic = c.trafficInfo(ctx, cfg, &t)
	}
	return t, redactText(b.String()), traffic
}

// sysctlUint reads a numeric sysctl key; 0 when it is missing or not a
// number.
func sysctlUint(m sysctl.Manager, key string) uint64 {
	v, _ := m.Get(key)
	n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	return n
}

// trafficInfo checks table inet deyroute_stats on a hub and renders the
// traffic section: the monitoring state and the per-tunnel counters.
func (c *Collector) trafficInfo(ctx context.Context, cfg *config.Config, t *TuneFacts) string {
	var b strings.Builder
	t.Monitoring = cfg.MonitoringEnabled()
	for _, tn := range cfg.Tunnels {
		if tn.Enabled {
			t.MonitoredTunnels++
		}
	}
	if !t.Monitoring {
		b.WriteString("monitoring: off (monitoring.enabled: false)\n")
		return redactText(b.String())
	}
	fmt.Fprintf(&b, "monitoring: on, %d enabled tunnel(s)\n", t.MonitoredTunnels)
	r := c.runner()
	if err := firewall.StatsSupport(ctx, r); err != nil {
		t.Stats, t.StatsReason = StatsUnavailable, errReason(err)
		fmt.Fprintf(&b, "table %s: unavailable: %s\n", firewall.StatsTable, t.StatsReason)
		return redactText(b.String())
	}
	counters, err := firewall.ReadCounters(ctx, r)
	switch {
	case errors.Is(err, firewall.ErrNoStatsTable):
		t.Stats = StatsMissing
		fmt.Fprintf(&b, "table %s: missing\n", firewall.StatsTable)
	case err != nil:
		t.Stats, t.StatsReason = StatsUnreadable, errReason(err)
		fmt.Fprintf(&b, "table %s: unreadable: %s\n", firewall.StatsTable, t.StatsReason)
	default:
		t.Stats = StatsPresent
		fmt.Fprintf(&b, "--- nft list counters table %s\n", firewall.StatsTable)
		for _, name := range sortedKeys(counters) {
			ct := counters[name]
			fmt.Fprintf(&b, "%s  packets %d  bytes %d (%s)\n", name, ct.Packets, ct.Bytes, sizeIEC(ct.Bytes))
		}
		if len(counters) == 0 {
			b.WriteString("(no counters)\n")
		}
	}
	return redactText(b.String())
}

// errReason is the reason parameter of a DEY error (DEY-X061/X062), else
// the error text.
func errReason(err error) string {
	if e := deyerr.As(err); e != nil {
		if r, ok := e.Params["reason"]; ok {
			return fmt.Sprint(r)
		}
	}
	return err.Error()
}

// udpRungs reports whether Hysteria2 or AmneziaWG rungs are in the ladder
// of an enabled tunnel (hub config) or run here (deyroute-tun@ units).
func udpRungs(cfg *config.Config, states map[string]string) bool {
	if cfg != nil && cfg.Role == config.RoleHub {
		for i := range cfg.Tunnels {
			tn := &cfg.Tunnels[i]
			if !tn.Enabled {
				continue
			}
			rungs, err := cfg.ResolveLadder(tn, nil)
			if err != nil {
				continue
			}
			for _, r := range rungs {
				if b, _, _ := strings.Cut(r, "/"); udpBackends[b] {
					return true
				}
			}
		}
	}
	for unit, st := range states {
		inst, ok := systemd.InstanceOf(unit)
		if !ok || st != "active" {
			continue
		}
		if in, err := systemd.ParseInstance(inst); err == nil {
			if b, _, _ := strings.Cut(in.Transport, "/"); udpBackends[b] {
				return true
			}
		}
	}
	return false
}

// stateSize returns the size of state.db counted against its budget and
// how it was measured; 0 when unknown.
func (c *Collector) stateSize() (int64, string) {
	if c.StateSize != nil {
		if n, err := c.StateSize(); err == nil {
			return n, "data in use"
		}
	}
	st, err := os.Stat(c.path(config.StatePath))
	if err != nil || !st.Mode().IsRegular() {
		return 0, ""
	}
	return st.Size(), "file size"
}

// unitNofile returns the LimitNOFILE (hard limit) of an installed unit:
// the unit file under /etc/systemd/system, then its drop-ins in name order
// (the last setting wins; an empty one resets it). false when the unit is
// not installed or sets none (or "infinity").
func (c *Collector) unitNofile(unit string) (uint64, bool) {
	main := c.path(path.Join(systemd.UnitDir, unit))
	if _, err := os.Stat(main); err != nil {
		return 0, false
	}
	drop, _ := filepath.Glob(filepath.Join(main+".d", "*.conf"))
	sort.Strings(drop)
	var limit uint64
	found := false
	for _, p := range append([]string{main}, drop...) {
		data, err := readLimited(p, maxTuneFileBytes)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || strings.TrimSpace(k) != "LimitNOFILE" {
				continue
			}
			limit, found = parseNofile(v)
		}
	}
	return limit, found
}

// parseNofile parses a LimitNOFILE value ("N" or "soft:hard") and returns
// the hard limit; false for "", "infinity" and anything else.
func parseNofile(v string) (uint64, bool) {
	v = strings.TrimSpace(v)
	if _, hard, ok := strings.Cut(v, ":"); ok {
		v = hard
	}
	n, err := strconv.ParseUint(v, 10, 64)
	return n, err == nil
}

// processNofile returns the hard "Max open files" limit of a running
// process (/proc/<pid>/limits).
func (c *Collector) processNofile(pid int) (uint64, bool) {
	data, err := readLimited(c.path("/proc/"+strconv.Itoa(pid)+"/limits"), maxTuneFileBytes)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "Max open files")
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) >= 2 {
			n, err := strconv.ParseUint(f[1], 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

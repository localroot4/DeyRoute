package sysctl

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

// sysctlDirs are the directories systemd-sysctl reads, in priority order: a
// file name in an earlier directory masks the same name in a later one.
var sysctlDirs = []string{"/etc/sysctl.d", "/run/sysctl.d", "/usr/local/lib/sysctl.d", "/usr/lib/sysctl.d", "/lib/sysctl.d"}

// sysctlConf is read last by `sysctl --system` (and through the
// 99-sysctl.conf link on Debian and Ubuntu by systemd-sysctl).
const sysctlConf = "/etc/sysctl.conf"

// Finding severities.
const (
	SeverityInfo = "info"
	SeverityWarn = "warn"
)

// DriftItem is a key of 99-deyroute.conf whose live value is not what
// deyroute wrote, or that a sysctl file applied after deyroute's sets to
// another value at boot (OverriddenBy names that file).
type DriftItem struct {
	Key          string
	Want         string
	Live         string
	OverriddenBy string
}

// API converts the item for the Local API.
func (d DriftItem) API() api.TuneDrift {
	return api.TuneDrift{Key: d.Key, Want: d.Want, Live: d.Live, OverriddenBy: d.OverriddenBy}
}

// Finding is another result of the tuning check.
type Finding struct {
	Check    string // conntrack_fill | nofile | bbr | qdisc
	Severity string // info | warn
	Reason   i18n.Key
	Args     []string
}

// Message renders the finding in the current language.
func (f Finding) Message() string { return i18n.T(f.Reason, anyArgs(f.Args)...) }

// API converts the finding for the Local API.
func (f Finding) API() api.TuneFinding {
	return api.TuneFinding{Check: f.Check, Severity: f.Severity, Message: f.Message()}
}

// sysctlFile is one file systemd-sysctl applies; Path is absolute on the
// host (without Root).
type sysctlFile struct {
	Name string
	Path string
}

// sysctlFiles returns the sysctl files in the order they are applied at
// boot: the *.conf names of sysctlDirs (an earlier directory masks the same
// name in a later one, a link to /dev/null masks it entirely) sorted by name,
// then /etc/sysctl.conf unless a file above already is (a link to) it.
func (m Manager) sysctlFiles() []sysctlFile {
	byName := map[string]sysctlFile{}
	masked := map[string]bool{}
	for _, dir := range sysctlDirs {
		entries, err := os.ReadDir(m.path(dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".conf") || e.IsDir() {
				continue
			}
			if _, seen := byName[name]; seen || masked[name] {
				continue
			}
			p := path.Join(dir, name)
			if target, err := os.Readlink(m.path(p)); err == nil && target == "/dev/null" {
				masked[name] = true
				continue
			}
			byName[name] = sysctlFile{Name: name, Path: p}
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	slices.Sort(names)
	out := make([]sysctlFile, 0, len(names)+1)
	hasConf := false
	for _, n := range names {
		f := byName[n]
		if m.resolvesTo(f.Path, sysctlConf) {
			f.Path = sysctlConf
			hasConf = true
		}
		out = append(out, f)
	}
	if _, err := os.Stat(m.path(sysctlConf)); err == nil && !hasConf {
		out = append(out, sysctlFile{Name: "", Path: sysctlConf})
	}
	return out
}

// resolvesTo reports whether the file p (host path) is a symbolic link to
// target (absolute or relative link, resolved below Root).
func (m Manager) resolvesTo(p, target string) bool {
	link, err := os.Readlink(m.path(p))
	if err != nil {
		return false
	}
	if !path.IsAbs(link) {
		link = path.Join(path.Dir(p), link)
	}
	return path.Clean(link) == target
}

// parseSysctlFile reads the assignments of a sysctl.d file: '#' and ';'
// comments, a leading '-' (ignore errors) and '/' separators are accepted.
func (m Manager) parseSysctlFile(p string) []KV {
	data, err := os.ReadFile(m.path(p)) // #nosec G304 -- sysctl.d file below Root
	if err != nil {
		return nil
	}
	var out []KV
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimPrefix(strings.TrimSpace(k), "-")
		if slash, dot := strings.IndexByte(k, '/'), strings.IndexByte(k, '.'); slash >= 0 && (dot < 0 || slash < dot) {
			k = strings.Map(func(r rune) rune {
				switch r {
				case '/':
					return '.'
				case '.':
					return '/'
				}
				return r
			}, k)
		}
		out = append(out, KV{k, normalize(v)})
	}
	return out
}

// setter is the file that sets a key last and the value it sets.
type setter struct{ Path, Value string }

// laterSetters returns, per key, the last file applied after
// 99-deyroute.conf that sets it: at boot that file wins.
func (m Manager) laterSetters() map[string]setter {
	own := filepath.Base(config.SysctlConfPath)
	out := map[string]setter{}
	after := false
	for _, f := range m.sysctlFiles() {
		if f.Name == own {
			after = true
			continue
		}
		if !after && f.Name != "" {
			continue
		}
		for _, kv := range m.parseSysctlFile(f.Path) {
			out[kv.Key] = setter{f.Path, kv.Value}
		}
	}
	return out
}

// forwardingUsers names something besides deyroute that needs
// net.ipv4.ip_forward = 1: a container or VM bridge (docker0, br-*, virbr*,
// lxdbr*, lxcbr*, cni*, podman*) or another sysctl file that enables it.
// "" when there is none.
func (m Manager) forwardingUsers() string {
	if entries, err := os.ReadDir(m.path("/sys/class/net")); err == nil {
		for _, e := range entries {
			n := e.Name()
			for _, prefix := range []string{"docker", "br-", "virbr", "lxdbr", "lxcbr", "cni", "podman"} {
				if strings.HasPrefix(n, prefix) {
					return n
				}
			}
		}
	}
	own := filepath.Base(config.SysctlConfPath)
	for _, f := range m.sysctlFiles() {
		if f.Name == own {
			continue
		}
		for _, kv := range m.parseSysctlFile(f.Path) {
			if kv.Key == KeyIPForward && kv.Value == "1" {
				return f.Path
			}
		}
	}
	return ""
}

// Check compares deyroute's tuning with the live system:
//
//   - drift: a key of 99-deyroute.conf whose live value differs, or that a
//     sysctl file applied after it (systemd-sysctl order: by file name,
//     /etc/sysctl.conf last) sets to another value at boot; also the
//     conntrack hashsize of the modprobe.d file. Conntrack keys are not
//     drift while nf_conntrack is not loaded (they apply when it loads).
//   - findings: the conntrack table more than 80 % full, more than 80 % of
//     the file handles in use, BBR applied but not active, and fq as the
//     default qdisc while the live root qdisc of the default-route
//     interface (f.Qdisc) is another one (info: it applies at the next
//     boot).
//
// It only reads files. Errors: DEY-X000 when 99-deyroute.conf cannot be
// read.
func (m Manager) Check(f sysinfo.Facts) (drift []DriftItem, findings []Finding, err error) {
	conf, err := m.readKVFile(m.ConfPath())
	if err != nil {
		return nil, nil, err
	}
	later := m.laterSetters()
	want := map[string]string{}
	for _, kv := range conf {
		want[kv.Key] = kv.Value
		live, ok := m.Get(kv.Key)
		if !ok && strings.HasPrefix(kv.Key, conntrackPrefix) {
			continue
		}
		d := DriftItem{Key: kv.Key, Want: kv.Value, Live: live}
		if s, has := later[kv.Key]; has && !sameValue(kv.Key, s.Value, kv.Value) {
			d.OverriddenBy = s.Path
		}
		if !ok || !sameValue(kv.Key, live, kv.Value) || d.OverriddenBy != "" {
			drift = append(drift, d)
		}
	}
	if hs := m.modprobeHashsize(); hs != "" {
		if live, ok := m.Live(PathHashsize); ok && live != hs {
			drift = append(drift, DriftItem{Key: PathHashsize, Want: hs, Live: live})
		}
	}

	if f.ConntrackLoaded && f.ConntrackMax > 0 && f.ConntrackCount*100 > f.ConntrackMax*80 {
		findings = append(findings, Finding{Check: "conntrack_fill", Severity: SeverityWarn, Reason: i18n.TuneFindConntrackFill,
			Args: []string{strconv.Itoa(f.ConntrackCount * 100 / f.ConntrackMax), strconv.Itoa(f.ConntrackCount), strconv.Itoa(f.ConntrackMax)}})
	}
	if v, ok := m.Get("fs.file-nr"); ok {
		if fl := strings.Fields(v); len(fl) == 3 {
			used, err1 := strconv.ParseUint(fl[0], 10, 64)
			limit, err2 := strconv.ParseUint(fl[2], 10, 64)
			if err1 == nil && err2 == nil && limit > 0 && used > limit/100*80 {
				findings = append(findings, Finding{Check: "nofile", Severity: SeverityWarn, Reason: i18n.TuneFindNofileFill,
					Args: []string{strconv.FormatUint(used*100/limit, 10), fl[0], fl[2]}})
			}
		}
	}
	if want[KeyCongestion] == "bbr" {
		if cc, _ := m.Get(KeyCongestion); cc != "bbr" {
			findings = append(findings, Finding{Check: "bbr", Severity: SeverityWarn, Reason: i18n.TuneFindBBRInactive, Args: []string{cc}})
		}
	}
	if want[KeyDefaultQdisc] == "fq" && f.NIC != "" && f.Qdisc != "" && !slices.Contains([]string{"fq", "mq", "noqueue"}, f.Qdisc) {
		findings = append(findings, Finding{Check: "qdisc", Severity: SeverityInfo, Reason: i18n.TuneFindQdisc, Args: []string{f.NIC, f.Qdisc}})
	}
	return drift, findings, nil
}

// modprobeHashsize is the hashsize deyroute's modprobe.d file sets ("" when
// there is none).
func (m Manager) modprobeHashsize() string {
	v, ok := m.Live(PathModprobe)
	if !ok {
		return ""
	}
	for _, f := range strings.Fields(v) {
		if n, ok := strings.CutPrefix(f, "hashsize="); ok {
			return n
		}
	}
	return ""
}

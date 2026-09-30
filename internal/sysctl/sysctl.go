// Package sysctl manages deyroute's kernel tuning (section 12): the off,
// balanced and aggressive profiles, applying them only through
// /etc/sysctl.d/99-deyroute.conf and /proc/sys, backing up the previous values
// in /var/lib/deyroute/sysctl-before-deyroute.conf, reverting with one call, and
// BBR detection. Callers apply a profile only after the owner confirmed it.
package sysctl

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// KV is one sysctl assignment.
type KV struct{ Key, Value string }

// String renders "key = value" (sysctl.conf syntax).
func (kv KV) String() string { return kv.Key + " = " + kv.Value }

// Well-known keys.
const (
	KeyDefaultQdisc  = "net.core.default_qdisc"
	KeyCongestion    = "net.ipv4.tcp_congestion_control"
	KeyAvailableCC   = "net.ipv4.tcp_available_congestion_control"
	KeyIPForward     = "net.ipv4.ip_forward"
	KeyNotsentLowat  = "net.ipv4.tcp_notsent_lowat"
	keyOSRelease     = "kernel.osrelease"
	confHeaderPrefix = "# managed by deyroute"
)

// AggressiveMinMemory is the RAM from which the aggressive profile is
// recommended (section 12: "RAM ≥ 4GB"). It is 4 GB in SI units so servers
// sold as 4 GB, whose MemTotal is slightly below 4 GiB, qualify.
const AggressiveMinMemory uint64 = 4_000_000_000

// balanced is the exact list of section 12, in order (ip_forward is added
// by Profile only when requested).
var balanced = []KV{
	{KeyDefaultQdisc, "fq"},
	{KeyCongestion, "bbr"},
	{"net.core.somaxconn", "65535"},
	{"net.ipv4.tcp_max_syn_backlog", "65535"},
	{"net.core.netdev_max_backlog", "32768"},
	{"net.ipv4.ip_local_port_range", "10240 65535"},
	{"net.ipv4.tcp_fin_timeout", "15"},
	{"net.ipv4.tcp_tw_reuse", "1"},
	{"net.ipv4.tcp_keepalive_time", "300"},
	{"net.ipv4.tcp_keepalive_intvl", "30"},
	{"net.ipv4.tcp_keepalive_probes", "5"},
	{"net.ipv4.tcp_fastopen", "3"},
	{"net.ipv4.tcp_mtu_probing", "1"},
	{"net.core.rmem_max", "16777216"},
	{"net.core.wmem_max", "16777216"},
	{"net.ipv4.tcp_rmem", "4096 87380 16777216"},
	{"net.ipv4.tcp_wmem", "4096 65536 16777216"},
	{"net.ipv4.udp_rmem_min", "16384"},
	{"net.ipv4.udp_wmem_min", "16384"},
	{"fs.file-max", "2097152"},
}

// aggressiveOverrides turns balanced into aggressive: 64 MB buffers, plus
// net.ipv4.tcp_notsent_lowat inserted after net.ipv4.tcp_wmem.
var aggressiveOverrides = map[string]string{
	"net.core.rmem_max": "67108864",
	"net.core.wmem_max": "67108864",
	"net.ipv4.tcp_rmem": "4096 87380 67108864",
	"net.ipv4.tcp_wmem": "4096 65536 67108864",
}

// aggressiveNotsentLowat is the aggressive profile's tcp_notsent_lowat.
const aggressiveNotsentLowat = "16384"

// Profiles lists the valid profile names.
var Profiles = []string{config.SysctlOff, config.SysctlBalanced, config.SysctlAggressive}

// Profile returns the ordered assignments of a profile. "off" changes
// nothing (not even ip_forward); balanced and aggressive append
// net.ipv4.ip_forward = 1 when ipForward is true (a WireGuard/AmneziaWG
// transport exists). Unknown names return DEY-C013.
func Profile(name string, ipForward bool) ([]KV, error) {
	var out []KV
	switch name {
	case config.SysctlOff:
		return nil, nil
	case config.SysctlBalanced:
		out = append(out, balanced...)
	case config.SysctlAggressive:
		for _, kv := range balanced {
			if v, ok := aggressiveOverrides[kv.Key]; ok {
				kv.Value = v
			}
			out = append(out, kv)
			if kv.Key == "net.ipv4.tcp_wmem" {
				out = append(out, KV{KeyNotsentLowat, aggressiveNotsentLowat})
			}
		}
	default:
		return nil, deyerr.New(deyerr.C013, deyerr.Params{
			"field": "tuning.sysctl_profile", "value": name, "allowed": strings.Join(Profiles, ", "),
		})
	}
	if ipForward {
		out = append(out, KV{KeyIPForward, "1"})
	}
	return out, nil
}

// RecommendAggressive reports whether a server with memBytes of RAM should
// be offered the aggressive profile.
func RecommendAggressive(memBytes uint64) bool { return memBytes >= AggressiveMinMemory }

// ApplyOptions selects what Manager.ApplyWith does.
type ApplyOptions struct {
	Profile   string // off | balanced | aggressive
	BBR       bool   // tuning.bbr: set fq + bbr when the kernel has BBR
	IPForward bool   // a WireGuard/AmneziaWG transport exists
}

// Manager applies and reverts profiles. Root ("/" when empty) prefixes every
// path so tests run against a fake /proc tree. Apply, ApplyWith and Revert
// are serialized process-wide: each one reads, extends and rewrites the
// backup file, and two interleaved calls could otherwise record a value
// deyroute itself had just written as "the value before deyroute".
type Manager struct {
	Root string
}

// applyMu serializes every Apply/ApplyWith/Revert of the process.
var applyMu sync.Mutex

func (m Manager) path(p string) string {
	root := m.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, p)
}

// ConfPath is /etc/sysctl.d/99-deyroute.conf under Root.
func (m Manager) ConfPath() string { return m.path(config.SysctlConfPath) }

// BackupPath is /var/lib/deyroute/sysctl-before-deyroute.conf under Root.
func (m Manager) BackupPath() string { return m.path(config.SysctlBackup) }

var keyRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_-]+)+$`)

// procPath maps "net.core.somaxconn" to <Root>/proc/sys/net/core/somaxconn.
func (m Manager) procPath(key string) (string, bool) {
	if !keyRe.MatchString(key) {
		return "", false
	}
	return m.path(filepath.Join("proc", "sys", strings.ReplaceAll(key, ".", "/"))), true
}

// Get returns the current kernel value of key (whitespace normalised) and
// whether the key exists.
func (m Manager) Get(key string) (string, bool) {
	p, ok := m.procPath(key)
	if !ok {
		return "", false
	}
	b, err := os.ReadFile(p) // #nosec G304 -- validated sysctl key under Root/proc/sys
	if err != nil {
		return "", false
	}
	return normalize(string(b)), true
}

func (m Manager) set(key, value string) error {
	p, ok := m.procPath(key)
	if !ok {
		return deyerr.New(deyerr.X033, deyerr.Params{"key": key, "value": value})
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0) // #nosec G304 -- validated sysctl key under Root/proc/sys
	if err == nil {
		_, err = f.WriteString(value + "\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		return deyerr.Wrap(deyerr.X033, err, deyerr.Params{"key": key, "value": value})
	}
	return nil
}

func normalize(v string) string { return strings.Join(strings.Fields(v), " ") }

// Apply applies a profile with BBR requested (tuning.bbr defaults to true).
// See ApplyWith.
func (m Manager) Apply(profile string, ipForward bool) (applied []KV, warnings []string, err error) {
	return m.ApplyWith(ApplyOptions{Profile: profile, BBR: true, IPForward: ipForward})
}

// ApplyWith makes the kernel match a profile:
//
//  1. "off" reverts everything deyroute changed (Revert) and returns.
//  2. BBR (fq + bbr) is kept only when requested and available; otherwise
//     it is skipped with a warning (section 12). Keys this kernel does not
//     have are skipped with a warning.
//  3. The current value of every key about to change is added to the backup
//     file; existing backup entries are never overwritten, so the file
//     always holds the values from before deyroute's first change.
//  4. /etc/sysctl.d/99-deyroute.conf is rewritten (0644, "managed by deyroute"
//     header naming the profile) so the settings survive a reboot.
//  5. Keys deyroute changed earlier but the profile no longer sets are
//     restored from the backup; profile values are written to /proc/sys.
//
// applied lists the assignments now in effect (profile order); kernel write
// failures are warnings. Errors: DEY-C013 for an unknown profile, DEY-X032
// when the backup or conf file cannot be written.
func (m Manager) ApplyWith(o ApplyOptions) (applied []KV, warnings []string, err error) {
	want, err := Profile(o.Profile, o.IPForward)
	if err != nil {
		return nil, nil, err
	}
	applyMu.Lock()
	defer applyMu.Unlock()
	if o.Profile == config.SysctlOff {
		return nil, nil, m.revert()
	}

	useBBR := o.BBR
	if useBBR && !m.BBRAvailable() {
		useBBR = false
		avail, _ := m.Get(KeyAvailableCC)
		warnings = append(warnings, fmt.Sprintf("skip %s and %s: tcp_bbr is not available on this kernel (%s: %s)",
			KeyDefaultQdisc, KeyCongestion, KeyAvailableCC, avail))
	}
	var keys []KV
	for _, kv := range want {
		if (kv.Key == KeyDefaultQdisc || kv.Key == KeyCongestion) && !useBBR {
			continue
		}
		if _, ok := m.Get(kv.Key); !ok {
			warnings = append(warnings, fmt.Sprintf("skip %s: not available on this kernel", kv.Key))
			continue
		}
		keys = append(keys, kv)
	}

	backup, err := m.readKVFile(m.BackupPath())
	if err != nil {
		return nil, warnings, err
	}
	have := map[string]bool{}
	for _, kv := range backup {
		have[kv.Key] = true
	}
	added := false
	for _, kv := range keys {
		if have[kv.Key] {
			continue
		}
		cur, _ := m.Get(kv.Key)
		backup = append(backup, KV{kv.Key, cur})
		have[kv.Key] = true
		added = true
	}
	if added {
		if err := writeFile(m.BackupPath(), renderBackup(backup), 0o600); err != nil {
			return nil, warnings, err
		}
	}
	if err := writeFile(m.ConfPath(), renderConf(o.Profile, keys), 0o644); err != nil {
		return nil, warnings, err
	}

	wanted := map[string]bool{}
	for _, kv := range keys {
		wanted[kv.Key] = true
	}
	for _, kv := range backup {
		if wanted[kv.Key] {
			continue
		}
		if cur, ok := m.Get(kv.Key); ok && cur != normalize(kv.Value) {
			if err := m.set(kv.Key, kv.Value); err != nil {
				warnings = append(warnings, fmt.Sprintf("restore %s: %v", kv.Key, errors.Unwrap(err)))
			}
		}
	}
	for _, kv := range keys {
		if cur, _ := m.Get(kv.Key); cur == normalize(kv.Value) {
			applied = append(applied, kv)
			continue
		}
		if err := m.set(kv.Key, kv.Value); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed %s: %v", kv, errors.Unwrap(err)))
			continue
		}
		applied = append(applied, kv)
	}
	if useBBR && wanted[KeyCongestion] {
		if cur, _ := m.Get(KeyCongestion); cur != "bbr" {
			warnings = append(warnings, fmt.Sprintf("%s is %q after writing bbr (module did not load)", KeyCongestion, cur))
		}
	}
	return applied, warnings, nil
}

// Revert restores every backed-up value, removes 99-deyroute.conf and, when
// all values were restored, the backup file. It is a no-op without a
// backup. Restore failures return DEY-X033 (joined) and keep the backup so
// the call can be retried.
func (m Manager) Revert() error {
	applyMu.Lock()
	defer applyMu.Unlock()
	return m.revert()
}

// revert is Revert without the lock (ApplyWith("off") already holds it).
func (m Manager) revert() error {
	backup, err := m.readKVFile(m.BackupPath())
	if err != nil {
		return err
	}
	if err := os.Remove(m.ConfPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": m.ConfPath()})
	}
	var errs []error
	for _, kv := range backup {
		cur, ok := m.Get(kv.Key)
		if !ok || cur == normalize(kv.Value) {
			continue
		}
		if err := m.set(kv.Key, kv.Value); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return deyerr.Join(errs...)
	}
	if err := os.Remove(m.BackupPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": m.BackupPath()})
	}
	return nil
}

// Current returns the applied profile named in the 99-deyroute.conf header,
// or "off" when the file does not exist.
func (m Manager) Current() (string, error) {
	data, err := os.ReadFile(m.ConfPath())
	if errors.Is(err, fs.ErrNotExist) {
		return config.SysctlOff, nil
	}
	if err != nil {
		return "", deyerr.Wrap(deyerr.X000, err, nil)
	}
	first, _, _ := strings.Cut(string(data), "\n")
	if rest, ok := strings.CutPrefix(first, confHeaderPrefix+" (profile: "); ok {
		if name, ok := strings.CutSuffix(strings.TrimSpace(rest), ")"); ok {
			return name, nil
		}
	}
	return "", deyerr.Wrap(deyerr.X000, fmt.Errorf("%s has no deyroute header", m.ConfPath()), nil)
}

// Applied returns the assignments listed in 99-deyroute.conf (none when the
// file does not exist).
func (m Manager) Applied() ([]KV, error) { return m.readKVFile(m.ConfPath()) }

// BBRAvailable reports whether the kernel can use BBR: it is listed in
// net.ipv4.tcp_available_congestion_control, or tcp_bbr is a module of the
// running kernel (listed in /lib/modules/<release>/modules.dep or
// modules.builtin) that the kernel loads when bbr is written.
func (m Manager) BBRAvailable() bool {
	if avail, ok := m.Get(KeyAvailableCC); ok {
		for _, f := range strings.Fields(avail) {
			if f == "bbr" {
				return true
			}
		}
	}
	rel, ok := m.Get(keyOSRelease)
	if !ok || rel == "" || strings.ContainsAny(rel, "/ ") {
		return false
	}
	for _, name := range []string{"modules.dep", "modules.builtin"} {
		data, err := os.ReadFile(m.path(filepath.Join("lib", "modules", rel, name))) // #nosec G304 -- fixed file under Root
		if err == nil && bytes.Contains(data, []byte("/tcp_bbr.ko")) {
			return true
		}
	}
	return false
}

// BBRActive reports whether the current congestion control is bbr.
func (m Manager) BBRActive() bool {
	cc, _ := m.Get(KeyCongestion)
	return cc == "bbr"
}

// MemTotal returns MemTotal from /proc/meminfo in bytes.
func (m Manager) MemTotal() (uint64, error) {
	data, err := os.ReadFile(m.path("proc/meminfo"))
	if err != nil {
		return 0, deyerr.Wrap(deyerr.X000, err, nil)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				break
			}
			return kb * 1024, nil
		}
	}
	return 0, deyerr.Wrap(deyerr.X000, errors.New("MemTotal not found in /proc/meminfo"), nil)
}

// Recommended returns the profile to offer by default: aggressive on
// servers with enough RAM, balanced otherwise.
func (m Manager) Recommended() string {
	if mem, err := m.MemTotal(); err == nil && RecommendAggressive(mem) {
		return config.SysctlAggressive
	}
	return config.SysctlBalanced
}

// Status summarises the tuning state (api.OptimizeStatus).
type Status struct {
	Profile      string
	BBRAvailable bool
	BBRActive    bool
	Applied      []KV
}

// Status reads the current profile, BBR state and applied assignments.
func (m Manager) Status() (Status, error) {
	p, err := m.Current()
	if err != nil {
		return Status{}, err
	}
	applied, err := m.Applied()
	if err != nil {
		return Status{}, err
	}
	return Status{Profile: p, BBRAvailable: m.BBRAvailable(), BBRActive: m.BBRActive(), Applied: applied}, nil
}

// readKVFile parses a sysctl.conf style file ("key = value", '#' and ';'
// comments). A missing file yields no entries; invalid keys are ignored.
func (m Manager) readKVFile(path string) ([]KV, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed deyroute file under Root
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, deyerr.Wrap(deyerr.X000, err, nil)
	}
	return parseKV(data), nil
}

func parseKV(data []byte) []KV {
	var out []KV
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if !keyRe.MatchString(k) || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, KV{k, normalize(v)})
	}
	return out
}

func renderConf(profile string, kvs []KV) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s (profile: %s)\n", confHeaderPrefix, profile)
	b.WriteString("# Rewritten by \"deyroute optimize apply\", removed by \"deyroute optimize revert\".\n")
	fmt.Fprintf(&b, "# Values from before deyroute: %s\n", config.SysctlBackup)
	for _, kv := range kvs {
		b.WriteString(kv.String() + "\n")
	}
	return b.Bytes()
}

func renderBackup(kvs []KV) []byte {
	var b bytes.Buffer
	b.WriteString("# Kernel settings before deyroute changed them; restored by \"deyroute optimize revert\".\n")
	for _, kv := range kvs {
		b.WriteString(kv.String() + "\n")
	}
	return b.Bytes()
}

// writeFile atomically replaces path (temp file + fsync + rename + directory
// fsync) unless it already holds data with mode perm, creating missing
// parent directories 0755: they are /etc/sysctl.d and /var/lib/deyroute,
// which the backend user must be able to traverse (/var/lib/deyroute/bin);
// the files themselves carry the restrictive mode. Failures return
// DEY-X032.
func writeFile(path string, data []byte, perm os.FileMode) error {
	wrap := func(err error) error { return deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": path}) }
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) { //nolint:gosec // G304: fixed deyroute file under Root
		if fi, err := os.Lstat(path); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm() == perm {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: must stay traversable, see above
		return wrap(err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return wrap(err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return wrap(err)
	}
	if err := tmp.Chmod(perm); err != nil {
		return wrap(err)
	}
	if err := tmp.Sync(); err != nil {
		return wrap(err)
	}
	if err := tmp.Close(); err != nil {
		return wrap(err)
	}
	if err := os.Rename(name, path); err != nil {
		return wrap(err)
	}
	ok = true
	// Best effort: make the rename durable (not every filesystem supports
	// fsync on a directory; the file itself is already in place).
	if d, err := os.Open(filepath.Dir(path)); err == nil { // #nosec G304 -- parent of a fixed deyroute file
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

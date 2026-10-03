package sysctl

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

// writeKernel writes one /proc/sys or /sys value (a variable so tests can
// make a write fail while the file still reads back).
var writeKernel = func(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0) // #nosec G304 -- validated kernel file under Root
	if err != nil {
		return err
	}
	_, err = f.WriteString(value + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Live returns the current value of a sysctl key, or the whitespace
// normalized content of a plan file (PathHashsize, PathModulesLoad,
// PathModprobe); false when it does not exist. It is AutoInputs.Live.
func (m Manager) Live(key string) (string, bool) {
	if fileKeys[key] {
		b, err := os.ReadFile(m.path(key)) // #nosec G304 -- fixed plan file under Root
		if err != nil {
			return "", false
		}
		return normalize(string(b)), true
	}
	return m.Get(key)
}

// PlanAuto computes the automatic profile of this host from its measured
// facts (internal/sysinfo) and its live values.
func (m Manager) PlanAuto(o ApplyOptions) Plan {
	return AutoPlan(sysinfo.Collect(m.Root), AutoInputs{
		BBR: o.BBR, IPForward: o.IPForward, UDPRungs: o.UDPRungs, Conntrack: o.Conntrack,
		Reserved: o.Reserved, BDPBytes: o.BDPBytes, Live: m.Live,
	})
}

// ---------------------------------------------------------------- backup

// fileOrig is the content a plan file had before deyroute wrote it.
type fileOrig struct {
	Content string
	Exists  bool
}

// backupData is /var/lib/deyroute/sysctl-before-deyroute.conf:
//
//   - "key = value" lines: the sysctl values from before deyroute's first
//     change (the format older versions read);
//   - "#@orig <path> = <quoted content>" or "#@orig <path> absent": the plan
//     files (sysfs hashsize, modules-load.d, modprobe.d) before deyroute;
//   - "#@written <key> = <value>": the value deyroute last wrote, the
//     baseline of compare-and-restore (a key whose live value differs was
//     changed by someone else and is left alone).
//
// The extra lines are comments for older versions, so a rollback still reads
// the file.
type backupData struct {
	KVs     []KV
	Files   map[string]fileOrig
	Written map[string]string
}

const (
	origPrefix    = "#@orig "
	writtenPrefix = "#@written "
)

func (b *backupData) index(key string) int {
	return slices.IndexFunc(b.KVs, func(kv KV) bool { return kv.Key == key })
}

func (b *backupData) original(key string) (string, bool) {
	if i := b.index(key); i >= 0 {
		return b.KVs[i].Value, true
	}
	return "", false
}

// record keeps value as key's original unless one is recorded already.
func (b *backupData) record(key, value string) {
	if b.index(key) < 0 {
		b.KVs = append(b.KVs, KV{key, normalize(value)})
	}
}

func (b *backupData) drop(key string) {
	if i := b.index(key); i >= 0 {
		b.KVs = slices.Delete(b.KVs, i, i+1)
	}
	delete(b.Written, key)
}

func (m Manager) readBackup() (backupData, error) {
	b := backupData{Files: map[string]fileOrig{}, Written: map[string]string{}}
	data, err := os.ReadFile(m.BackupPath())
	if errors.Is(err, fs.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return b, deyerr.Wrap(deyerr.X000, err, nil)
	}
	b.KVs = parseKV(data)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, origPrefix); ok {
			if path, ok := strings.CutSuffix(rest, " absent"); ok && fileKeys[path] {
				b.Files[path] = fileOrig{}
				continue
			}
			path, q, ok := strings.Cut(rest, " = ")
			if !ok || !fileKeys[path] {
				continue
			}
			if content, err := strconv.Unquote(q); err == nil {
				b.Files[path] = fileOrig{Content: content, Exists: true}
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, writtenPrefix); ok {
			k, v, ok := strings.Cut(rest, "=")
			k = strings.TrimSpace(k)
			if ok && (keyRe.MatchString(k) || fileKeys[k]) {
				b.Written[k] = normalize(v)
			}
		}
	}
	return b, nil
}

func renderBackup(b backupData) []byte {
	var buf bytes.Buffer
	buf.WriteString("# Kernel settings before deyroute changed them; restored by \"deyroute optimize revert\".\n")
	for _, kv := range b.KVs {
		buf.WriteString(kv.String() + "\n")
	}
	for _, p := range slices.Sorted(maps.Keys(b.Files)) {
		if o := b.Files[p]; o.Exists {
			fmt.Fprintf(&buf, "%s%s = %s\n", origPrefix, p, strconv.Quote(o.Content))
		} else {
			fmt.Fprintf(&buf, "%s%s absent\n", origPrefix, p)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(b.Written)) {
		fmt.Fprintf(&buf, "%s%s = %s\n", writtenPrefix, k, b.Written[k])
	}
	return buf.Bytes()
}

func (m Manager) writeBackup(b backupData) error {
	return writeFile(m.BackupPath(), renderBackup(b), 0o600)
}

// writtenValues are the values deyroute wrote: 99-deyroute.conf as it was
// before this call, overridden by the ledger of the backup file.
func writtenValues(conf []KV, b backupData) map[string]string {
	w := make(map[string]string, len(conf)+len(b.Written))
	for _, kv := range conf {
		w[kv.Key] = normalize(kv.Value)
	}
	maps.Copy(w, b.Written)
	return w
}

// ---------------------------------------------------------------- apply

// ApplyPlan applies an automatic plan (normally the one the owner saw):
//
//  1. Raise-only items whose live value went above the plan since it was
//     computed are left alone (NotOwned).
//  2. The value before deyroute of every item is kept in the backup file
//     (sysctl keys, the sysfs hashsize, the absence of the modules-load.d
//     and modprobe.d files) together with the value deyroute writes.
//  3. 99-deyroute.conf is written from Desired with the header
//     "profile: auto" and the plan's DesiredHash; the modules files are
//     written.
//  4. Keys deyroute changed earlier that the plan no longer owns are
//     restored by compare-and-restore (NotOwned keys are not touched).
//  5. Live values that differ are written.
//
// A second call with the same plan writes no file and no kernel value.
// applied lists the items now in effect; kernel write failures are
// warnings. Errors: DEY-X032 when a file cannot be written.
func (m Manager) ApplyPlan(p Plan) (applied []Change, warnings []string, err error) {
	applyMu.Lock()
	defer applyMu.Unlock()
	return m.applyPlan(p)
}

func (m Manager) applyPlan(p Plan) (applied []Change, warnings []string, err error) {
	b, err := m.readBackup()
	if err != nil {
		return nil, nil, err
	}
	// What deyroute wrote before this call (best effort: the ledger of the
	// backup file covers it too; an unreadable conf fails below).
	oldConf, _ := m.readKVFile(m.ConfPath())
	written := writtenValues(oldConf, b)

	keep := map[string]bool{}
	for _, c := range p.NotOwned {
		keep[c.Key] = true
	}
	var desired []Change
	for _, c := range p.Desired {
		keep[c.Key] = true
		if c.RaiseOnly {
			if cur, ok := m.Live(c.Key); ok && exceeds(cur, c.To) {
				continue // raised by someone else since the plan: not owned
			}
		}
		if c.Key == KeyReservedPorts {
			// Reservations made since the plan are kept too (a set union).
			if cur, ok := m.Get(c.Key); ok {
				c.To = unionPorts(cur, []string{c.To})
			}
		}
		desired = append(desired, c)
	}

	for _, c := range desired {
		if c.Kind == api.TuneKindSysctl {
			if cur, ok := m.Get(c.Key); ok {
				b.record(c.Key, cur)
			}
		} else if _, ok := b.Files[c.Key]; !ok {
			data, err := os.ReadFile(m.path(c.Key)) // #nosec G304 -- fixed plan file under Root
			b.Files[c.Key] = fileOrig{Content: string(data), Exists: err == nil}
		}
		b.Written[c.Key] = normalize(c.To)
	}
	if err := m.writeBackup(b); err != nil {
		return nil, nil, err
	}
	var kvs []KV
	for _, c := range desired {
		if c.Kind == api.TuneKindSysctl {
			kvs = append(kvs, KV{c.Key, normalize(c.To)})
		}
	}
	if err := writeFile(m.ConfPath(), renderConfPlan(p.Profile, p.DesiredHash(), kvs), 0o644); err != nil {
		return nil, nil, err
	}
	for _, c := range desired {
		if c.Kind == api.TuneKindModules {
			if err := writeFile(m.path(c.Key), []byte(c.To+"\n"), 0o644); err != nil {
				return nil, nil, err
			}
		}
	}

	warnings = m.restoreDropped(&b, keep, written)
	if err := m.writeBackup(b); err != nil {
		return nil, warnings, err
	}

	for _, c := range desired {
		switch c.Kind {
		case api.TuneKindModules:
			applied = append(applied, c)
			continue
		case api.TuneKindSysctl:
			if _, ok := m.procPath(c.Key); !ok {
				continue
			}
		}
		cur, ok := m.Live(c.Key)
		if !ok {
			continue // conntrack not loaded yet: applies at boot (or Reassert)
		}
		if sameValue(c.Key, cur, c.To) {
			applied = append(applied, c)
			continue
		}
		if err := m.writeValue(c.Key, c.To); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed %s = %s: %v", c.Key, c.To, errors.Unwrap(err)))
			continue
		}
		applied = append(applied, c)
		if c.Key == KeyCongestion {
			if cur, _ := m.Get(KeyCongestion); cur != "bbr" {
				warnings = append(warnings, fmt.Sprintf("%s is %q after writing bbr (module did not load)", KeyCongestion, cur))
			}
		}
	}
	return applied, warnings, nil
}

// writeValue writes a sysctl key or the sysfs hashsize. Errors: DEY-X033.
func (m Manager) writeValue(key, value string) error {
	if key == PathHashsize {
		if err := writeKernel(m.path(key), value); err != nil {
			return deyerr.Wrap(deyerr.X033, err, deyerr.Params{"key": key, "value": value})
		}
		return nil
	}
	return m.set(key, value)
}

func renderConfPlan(profile, hash string, kvs []KV) []byte {
	conf := renderConf(profile, kvs)
	header, rest, _ := bytes.Cut(conf, []byte("\n"))
	var b bytes.Buffer
	b.Write(header)
	fmt.Fprintf(&b, "\n# plan %s (written by \"deyroute optimize auto\")\n", hash)
	b.Write(rest)
	return b.Bytes()
}

// ---------------------------------------------------------------- restore

type outcome int

const (
	outNone     outcome = iota // nothing to do (absent, already the original)
	outRestored                // the original value was written back
	outChanged                 // someone else changed it after deyroute: left
	outKept                    // a limit in use or forwarding needed: left
	outFailed                  // the write failed
)

// restoreKey is compare-and-restore for one sysctl key: the original comes
// back only when the live value is still what deyroute wrote (written, when
// known), a limit is never lowered below what is in use, and ip_forward stays
// on while something else forwards. Reserved ports lose only the entries
// deyroute added.
func (m Manager) restoreKey(key, orig, written string, hasWritten bool) (outcome, string, error) {
	cur, ok := m.Get(key)
	orig = normalize(orig)
	if !ok || sameValue(key, cur, orig) {
		return outNone, "", nil
	}
	target := orig
	switch {
	case key == KeyReservedPorts && hasWritten:
		target = subtractPorts(cur, written, orig)
		if sameValue(key, cur, target) {
			return outNone, "", nil
		}
	case hasWritten && !sameValue(key, cur, written):
		return outChanged, i18n.T(i18n.TuneWarnChanged, key, cur), nil
	}
	if w := m.limitKept(key, orig, cur); w != "" {
		return outKept, w, nil
	}
	if key == KeyIPForward && orig == "0" {
		if who := m.forwardingUsers(); who != "" {
			return outKept, i18n.T(i18n.TuneWarnForwardInUse, key, who), nil
		}
	}
	if err := m.set(key, target); err != nil {
		return outFailed, "", err
	}
	return outRestored, "", nil
}

// restoreFile is compare-and-restore for a plan file: the sysfs hashsize
// gets its original back (never below what is in use), a modules file is
// removed (or gets its earlier content back).
func (m Manager) restoreFile(path string, orig fileOrig, written string, hasWritten bool) (outcome, string, error) {
	cur, exists := m.Live(path)
	if !exists && (!orig.Exists || path == PathHashsize) {
		return outNone, "", nil
	}
	if exists && orig.Exists && cur == normalize(orig.Content) {
		return outNone, "", nil
	}
	if exists && hasWritten && cur != written {
		return outChanged, i18n.T(i18n.TuneWarnChanged, path, cur), nil
	}
	if path == PathHashsize {
		if w := m.limitKept(path, orig.Content, cur); w != "" {
			return outKept, w, nil
		}
		if err := writeKernel(m.path(path), normalize(orig.Content)); err != nil {
			return outFailed, "", deyerr.Wrap(deyerr.X033, err, deyerr.Params{"key": path, "value": normalize(orig.Content)})
		}
		return outRestored, "", nil
	}
	if orig.Exists {
		if err := writeFile(m.path(path), []byte(orig.Content), 0o644); err != nil {
			return outFailed, "", err
		}
		return outRestored, "", nil
	}
	if err := os.Remove(m.path(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return outFailed, "", deyerr.Wrap(deyerr.X032, err, deyerr.Params{"path": m.path(path)})
	}
	return outRestored, "", nil
}

// restoreDropped restores what deyroute changed earlier but no longer owns
// (keys and files not in keep). A key someone else changed, or a limit kept
// because it is in use, is given up: its warning is returned once and it
// leaves the backup (the original returns at the next boot, as the conf no
// longer sets it). A failed write keeps the entry for the next attempt.
func (m Manager) restoreDropped(b *backupData, keep map[string]bool, written map[string]string) (warnings []string) {
	for _, kv := range slices.Clone(b.KVs) {
		if keep[kv.Key] {
			continue
		}
		w, has := written[kv.Key]
		out, msg, err := m.restoreKey(kv.Key, kv.Value, w, has)
		switch out {
		case outNone, outRestored:
			delete(b.Written, kv.Key)
		case outChanged, outKept:
			warnings = append(warnings, msg)
			b.drop(kv.Key)
		case outFailed:
			warnings = append(warnings, i18n.T(i18n.TuneWarnRestore, kv.Key, fmt.Sprint(errors.Unwrap(err))))
		}
	}
	for _, p := range slices.Sorted(maps.Keys(b.Files)) {
		if keep[p] {
			continue
		}
		w, has := written[p]
		out, msg, err := m.restoreFile(p, b.Files[p], w, has)
		switch out {
		case outNone, outRestored:
			delete(b.Written, p)
		case outChanged, outKept:
			warnings = append(warnings, msg)
			delete(b.Files, p)
			delete(b.Written, p)
		case outFailed:
			warnings = append(warnings, i18n.T(i18n.TuneWarnRestore, p, fmt.Sprint(errors.Unwrap(err))))
		}
	}
	return warnings
}

// limitKept returns a warning when restoring a limit key from cur down to
// orig would put it below what is in use: a limit is never restored below
// max(orig, usage × 1.25). The live value then stays; the original returns
// at the next boot. "" means the restore may go ahead.
func (m Manager) limitKept(key, orig, cur string) string {
	if !limitKeys[key] {
		return ""
	}
	o, err1 := strconv.ParseUint(normalize(orig), 10, 64)
	c, err2 := strconv.ParseUint(normalize(cur), 10, 64)
	if err1 != nil || err2 != nil || o >= c {
		return ""
	}
	u := m.usage(key)
	floor := u + u/4
	if u%4 != 0 {
		floor++
	}
	if floor <= o {
		return ""
	}
	return i18n.T(i18n.TuneWarnLimitKept, key, normalize(cur), strconv.FormatUint(u, 10), normalize(orig))
}

// usage is what a limit key covers right now: conntrack entries (a quarter
// of them per hash bucket), allocated file handles, the largest open-files
// limit of a running process; 0 when unknown (the backlogs).
func (m Manager) usage(key string) uint64 {
	switch key {
	case KeyConntrackMax:
		return m.uintValue(KeyConntrackCount)
	case KeyConntrackBuckets, PathHashsize:
		n := m.uintValue(KeyConntrackCount)
		return (n + 3) / 4
	case KeyFileMax:
		v, _ := m.Get("fs.file-nr")
		if f := strings.Fields(v); len(f) > 0 {
			n, _ := strconv.ParseUint(f[0], 10, 64)
			return n
		}
	case KeyNROpen:
		return m.maxNofile()
	}
	return 0
}

func (m Manager) uintValue(key string) uint64 {
	v, _ := m.Get(key)
	n, _ := strconv.ParseUint(v, 10, 64)
	return n
}

// maxNofile is the largest hard "Max open files" limit of a running process
// (/proc/<pid>/limits).
func (m Manager) maxNofile() uint64 {
	files, _ := filepath.Glob(m.path("proc/[0-9]*/limits"))
	var top uint64
	for _, p := range files {
		data, err := os.ReadFile(p) // #nosec G304 -- procfs below Root
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			rest, ok := strings.CutPrefix(line, "Max open files")
			if !ok {
				continue
			}
			if f := strings.Fields(rest); len(f) >= 2 {
				if n, err := strconv.ParseUint(f[1], 10, 64); err == nil {
					top = max(top, n)
				}
			}
		}
	}
	return top
}

// ---------------------------------------------------------------- reassert

// Reassert re-applies, at daemon start, conntrack keys of 99-deyroute.conf
// that systemd-sysctl could not set at boot because nf_conntrack was loaded
// later. Only net.netfilter.nf_conntrack_* keys are touched, and only when
// the live value is still the value from before deyroute (the kernel
// default: nobody changed it at runtime), it is not higher for a raise-only
// key, and no sysctl file sorted after deyroute's sets it. The caller
// records one config_applied event per reasserted key.
func (m Manager) Reassert() (reasserted []KV, warnings []string, err error) {
	applyMu.Lock()
	defer applyMu.Unlock()
	conf, err := m.readKVFile(m.ConfPath())
	if err != nil || len(conf) == 0 {
		return nil, nil, err
	}
	b, err := m.readBackup()
	if err != nil {
		return nil, nil, err
	}
	later := m.laterSetters()
	changed := false
	for _, kv := range conf {
		if !strings.HasPrefix(kv.Key, conntrackPrefix) {
			continue
		}
		cur, ok := m.Get(kv.Key)
		if !ok || sameValue(kv.Key, cur, kv.Value) {
			continue
		}
		if _, overridden := later[kv.Key]; overridden {
			continue
		}
		if raiseOnly[kv.Key] && exceeds(cur, kv.Value) {
			continue
		}
		if orig, has := b.original(kv.Key); has && !sameValue(kv.Key, cur, orig) {
			continue
		}
		b.record(kv.Key, cur)
		b.Written[kv.Key] = kv.Value
		changed = true
		if err := m.set(kv.Key, kv.Value); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed %s: %v", kv, errors.Unwrap(err)))
			continue
		}
		reasserted = append(reasserted, kv)
	}
	if changed {
		if err := m.writeBackup(b); err != nil {
			return reasserted, warnings, err
		}
	}
	return reasserted, warnings, nil
}

// applyAuto is ApplyWith for the profile auto (lock held): o.Plan, or the
// plan computed now from the live facts.
func (m Manager) applyAuto(o ApplyOptions) ([]KV, []string, error) {
	p := m.PlanAuto(o)
	if o.Plan != nil {
		p = *o.Plan
	}
	if p.Profile == "" {
		p.Profile = config.SysctlAuto
	}
	changes, warnings, err := m.applyPlan(p)
	// One warning per reason ("skip a, b: reason"): in a container every
	// item has the same one.
	var reasons []string
	keys := map[string][]string{}
	for _, s := range p.Skips {
		r := i18n.T(s.Reason, anyArgs(s.Args)...)
		if _, seen := keys[r]; !seen {
			reasons = append(reasons, r)
		}
		keys[r] = append(keys[r], s.Key)
	}
	for _, r := range reasons {
		warnings = append(warnings, "skip "+strings.Join(keys[r], ", ")+": "+r)
	}
	var applied []KV
	for _, c := range changes {
		if c.Kind == api.TuneKindSysctl {
			applied = append(applied, KV{c.Key, normalize(c.To)})
		}
	}
	return applied, warnings, err
}

package sysctl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/bits"
	"slices"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

// PlanVersion is the version of the automatic plan algorithm. The hub sends
// it in SysctlArgs.PlanVersion (part of the inputs hash), so a node re-applies
// when the algorithm changes.
const PlanVersion = 1

// Keys the automatic profile adds to balanced.
const (
	KeyReservedPorts      = "net.ipv4.ip_local_reserved_ports"
	KeySlowStartAfterIdle = "net.ipv4.tcp_slow_start_after_idle"
	KeyRmemDefault        = "net.core.rmem_default"
	KeyWmemDefault        = "net.core.wmem_default"
	KeyRmemMax            = "net.core.rmem_max"
	KeyWmemMax            = "net.core.wmem_max"
	KeyTCPRmem            = "net.ipv4.tcp_rmem"
	KeyTCPWmem            = "net.ipv4.tcp_wmem" //nolint:gosec // G101: a sysctl key name, not a credential
	KeySomaxconn          = "net.core.somaxconn"
	KeySynBacklog         = "net.ipv4.tcp_max_syn_backlog"
	KeyNetdevBacklog      = "net.core.netdev_max_backlog"
	KeyFileMax            = "fs.file-max"
	KeyNROpen             = "fs.nr_open"
	KeyConntrackMax       = "net.netfilter.nf_conntrack_max"
	KeyConntrackBuckets   = "net.netfilter.nf_conntrack_buckets"
	KeyConntrackCount     = "net.netfilter.nf_conntrack_count"
	KeyConntrackEstab     = "net.netfilter.nf_conntrack_tcp_timeout_established"

	// conntrackPrefix is the prefix of every conntrack key: they exist only
	// while the nf_conntrack module is loaded.
	conntrackPrefix = "net.netfilter.nf_conntrack_"
)

// Files the automatic profile writes besides 99-deyroute.conf. Their
// previous content (or their absence) is kept in the backup file.
const (
	// PathHashsize is the conntrack hash table size (sysfs, runtime).
	PathHashsize = "/sys/module/nf_conntrack/parameters/hashsize"
	// PathModulesLoad loads nf_conntrack at boot, before systemd-sysctl
	// applies the conntrack keys of 99-deyroute.conf.
	PathModulesLoad = "/etc/modules-load.d/deyroute.conf"
	// PathModprobe keeps the hash table size across reboots.
	PathModprobe = "/etc/modprobe.d/deyroute.conf"
)

// Values of the computed layer.
const (
	mib                    = uint64(1) << 20
	minBuffer              = 8 * mib
	udpDefaultBuffer       = "1048576"
	nrOpenMin              = "1048576"
	notsentLowat           = "16384"
	conntrackMin           = 65536
	conntrackMaxCap        = 1048576
	conntrackPerMiB        = 64
	conntrackEstabDefault  = "432000" // kernel default (5 days)
	conntrackEstabAuto     = "86400"
	conntrackModuleContent = "nf_conntrack"
)

// fileKeys are the non-sysctl items the plan may own (Change.Key of the
// kinds sysfs and modules).
var fileKeys = map[string]bool{PathHashsize: true, PathModulesLoad: true, PathModprobe: true}

// raiseOnly are the keys whose live value is never lowered: a host already
// above deyroute's value keeps it (the key is then NotOwned).
var raiseOnly = map[string]bool{
	KeySomaxconn: true, KeySynBacklog: true, KeyNetdevBacklog: true,
	KeyRmemMax: true, KeyWmemMax: true, KeyTCPRmem: true, KeyTCPWmem: true,
	KeyRmemDefault: true, KeyWmemDefault: true,
	KeyFileMax: true, KeyNROpen: true,
	KeyConntrackMax: true, PathHashsize: true,
}

// limitKeys are capacity limits: a revert never lowers them below what is
// in use (DECISIONS item 9, see Manager.limitKept).
var limitKeys = map[string]bool{
	KeyConntrackMax: true, KeyConntrackBuckets: true, PathHashsize: true,
	KeyFileMax: true, KeyNROpen: true,
	KeySomaxconn: true, KeySynBacklog: true, KeyNetdevBacklog: true,
}

// AutoInputs are what the plan needs besides the measured facts.
type AutoInputs struct {
	// BBR is tuning.bbr: use BBR when the kernel has it.
	BBR bool
	// IPForward: a WireGuard/AmneziaWG transport forwards on this host.
	IPForward bool
	// UDPRungs: Hysteria2 or AmneziaWG rungs run here (UDP buffers).
	UDPRungs bool
	// Conntrack: something here needs connection tracking (NAT rungs, the
	// firewall), so the table is sized even when nf_conntrack is not loaded
	// yet.
	Conntrack bool
	// Reserved are port entries ("30000-31999", "8443") added to
	// net.ipv4.ip_local_reserved_ports (a set union with the live value).
	Reserved []string
	// BDPBytes is the measured bandwidth-delay product (0 = not measured).
	BDPBytes uint64
	// Live returns the current value of a sysctl key or a plan file
	// (Manager.Live); nil means nothing is known.
	Live func(key string) (string, bool)
}

// Change is one item of a plan. From is the live value ("" = absent).
type Change struct {
	Kind      string // api.TuneKind*: sysctl | sysfs | modules
	Key       string // sysctl key or file path (PathHashsize, ...)
	From      string
	To        string
	Reason    i18n.Key
	Args      []string
	Effect    string // api.TuneEffect*
	RaiseOnly bool
}

// ReasonText renders the reason in the current language.
func (c Change) ReasonText() string { return i18n.T(c.Reason, anyArgs(c.Args)...) }

// API converts the change for the Local API.
func (c Change) API() api.TuneChange {
	return api.TuneChange{Kind: c.Kind, Key: c.Key, From: c.From, To: c.To, Reason: c.ReasonText(),
		Effect: c.Effect, RaiseOnly: c.RaiseOnly}
}

// Skip is an item the plan leaves out, with its reason.
type Skip struct {
	Key    string
	Reason i18n.Key
	Args   []string
	Code   deyerr.Code // DEY-X064 in a container, "" otherwise
}

// API converts the skip for the Local API.
func (s Skip) API() api.TuneSkip {
	return api.TuneSkip{Key: s.Key, Reason: i18n.T(s.Reason, anyArgs(s.Args)...), Code: string(s.Code)}
}

// Plan is the automatic profile computed for one host.
//
//   - Desired is every item the profile owns, including items already at
//     their value: 99-deyroute.conf is always written from it.
//   - Changes is the part of Desired whose live value differs: what is
//     shown to the owner.
//   - NotOwned are raise-only keys whose live value is already higher: they
//     are neither written nor reverted (To is deyroute's value, From the
//     live one).
//   - Skips are items left out (no BBR, a container, ...).
//
// Hash covers all of it; it changes when a fact or a live value changes, so
// an apply can refuse a plan other than the one shown (DEY-X065).
type Plan struct {
	Profile  string
	Desired  []Change
	Changes  []Change
	NotOwned []Change
	Skips    []Skip
	Hash     string
}

// APIChanges converts Changes for the Local API.
func (p Plan) APIChanges() []api.TuneChange {
	out := make([]api.TuneChange, 0, len(p.Changes))
	for _, c := range p.Changes {
		out = append(out, c.API())
	}
	return out
}

// APISkips converts Skips for the Local API.
func (p Plan) APISkips() []api.TuneSkip {
	out := make([]api.TuneSkip, 0, len(p.Skips))
	for _, s := range p.Skips {
		out = append(out, s.API())
	}
	return out
}

// DesiredHash is the hash of what the profile owns (kind, key, value): it
// is the same before and after an apply, so 99-deyroute.conf, which records
// it, does not change when the plan is computed again.
func (p Plan) DesiredHash() string {
	lines := []string{"desired", strconv.Itoa(PlanVersion), p.Profile}
	for _, c := range p.Desired {
		lines = append(lines, c.Kind+"|"+c.Key+"|"+c.To)
	}
	return hashLines(lines)
}

func (p Plan) computeHash() string {
	var lines []string
	for _, c := range p.Desired {
		lines = append(lines, "D|"+c.Kind+"|"+c.Key+"|"+c.To)
	}
	for _, c := range p.Changes {
		lines = append(lines, "C|"+c.Kind+"|"+c.Key+"|"+c.From+"|"+c.To+"|"+c.Effect)
	}
	for _, c := range p.NotOwned {
		lines = append(lines, "N|"+c.Kind+"|"+c.Key+"|"+c.From+"|"+c.To)
	}
	for _, s := range p.Skips {
		lines = append(lines, "S|"+s.Key+"|"+string(s.Code)+"|"+string(s.Reason)+"|"+strings.Join(s.Args, ","))
	}
	slices.Sort(lines)
	return hashLines(append([]string{"plan", strconv.Itoa(PlanVersion), p.Profile}, lines...))
}

func hashLines(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:16])
}

func anyArgs(args []string) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a
	}
	return out
}

// candidate is an item before it is checked against the live system.
type candidate struct {
	Change
	skip        *Skip // left out before looking at the live value
	allowAbsent bool  // a conntrack key while the module is not loaded yet
}

// AutoPlan computes the automatic profile of a host. It is pure: the facts,
// the inputs and in.Live are all it reads, so the same arguments always give
// the same plan.
//
// The base is the balanced profile (section 12) unchanged; the computed
// layer then sizes the buffers by RAM (or by the measured BDP), adds the
// keys for long-lived tunnel connections and UDP rungs, keeps limit keys
// raise-only, reserves ports, and sizes conntrack when it is loaded or
// needed. In a container every item is skipped with DEY-X064.
func AutoPlan(f sysinfo.Facts, in AutoInputs) Plan {
	live := in.Live
	if live == nil {
		live = func(string) (string, bool) { return "", false }
	}
	cands := autoCandidates(f, in)
	p := Plan{Profile: config.SysctlAuto}
	if f.Virt != sysinfo.VirtNone {
		for _, c := range cands {
			p.Skips = append(p.Skips, Skip{Key: c.Key, Reason: i18n.TuneSkipContainer, Args: []string{f.Virt}, Code: deyerr.X064})
		}
		p.Hash = p.computeHash()
		return p
	}
	ownsConntrack := false
	hashsizeOwned := false
	for _, c := range cands {
		if c.skip != nil {
			p.Skips = append(p.Skips, *c.skip)
			continue
		}
		if c.Key == PathModprobe && !hashsizeOwned {
			continue
		}
		if c.Key == PathModulesLoad && !ownsConntrack {
			continue
		}
		cur, ok := live(c.Key)
		switch {
		case c.Kind == api.TuneKindModules:
			// A file deyroute owns: absent is "".
		case !ok && c.allowAbsent:
			if c.Kind == api.TuneKindSysfs {
				// Not loaded yet: the modprobe option sizes it at load.
				hashsizeOwned = true
				continue
			}
			c.Effect = api.TuneEffectReboot
		case !ok:
			p.Skips = append(p.Skips, Skip{Key: c.Key, Reason: i18n.TuneSkipMissing})
			continue
		}
		c.From = cur
		if c.Key == KeyConntrackEstab && ok && !sameValue(c.Key, cur, conntrackEstabDefault) && !sameValue(c.Key, cur, c.To) {
			p.Skips = append(p.Skips, Skip{Key: c.Key, Reason: i18n.TuneSkipTimeoutAdmin, Args: []string{cur}})
			continue
		}
		if c.Key == KeyReservedPorts {
			c.To = unionPorts(cur, in.Reserved)
		}
		if c.RaiseOnly && ok {
			if exceeds(cur, c.To) {
				p.NotOwned = append(p.NotOwned, Change{Kind: c.Kind, Key: c.Key, From: cur, To: c.To,
					Reason: i18n.TuneReasonNotOwned, Args: []string{cur, c.To}, Effect: c.Effect, RaiseOnly: true})
				continue
			}
			c.To = raiseValue(cur, c.To)
		}
		if c.Key == PathHashsize {
			hashsizeOwned = true
		}
		if strings.HasPrefix(c.Key, conntrackPrefix) || c.Key == PathHashsize {
			ownsConntrack = true
		}
		p.Desired = append(p.Desired, c.Change)
		if !ok || !sameValue(c.Key, cur, c.To) {
			p.Changes = append(p.Changes, c.Change)
		}
	}
	p.Hash = p.computeHash()
	return p
}

// autoCandidates lists every item of the automatic profile in order, with
// the values computed from the facts (before the live values are known).
func autoCandidates(f sysinfo.Facts, in AutoInputs) []candidate {
	sysctlItem := func(key, to string, reason i18n.Key, args ...string) candidate {
		return candidate{Change: Change{Kind: api.TuneKindSysctl, Key: key, To: to, Reason: reason, Args: args,
			Effect: api.TuneEffectNow, RaiseOnly: raiseOnly[key]}}
	}
	skipped := func(key string, reason i18n.Key) candidate {
		return candidate{Change: Change{Kind: api.TuneKindSysctl, Key: key}, skip: &Skip{Key: key, Reason: reason}}
	}
	buf, bufReason, bufArgs := bufferPlan(f.MemBytes, in.BDPBytes)
	bufStr := strconv.FormatUint(buf, 10)
	useBBR := in.BBR && f.BBRAvailable

	base, _ := Profile(config.SysctlBalanced, in.IPForward) // a fixed valid name
	var out []candidate
	for _, kv := range base {
		switch kv.Key {
		case KeyDefaultQdisc:
			if !f.FQAvailable {
				out = append(out, skipped(kv.Key, i18n.TuneSkipNoFQ))
				continue
			}
			c := sysctlItem(kv.Key, kv.Value, i18n.TuneReasonFQ)
			c.Effect = api.TuneEffectReboot
			out = append(out, c)
		case KeyCongestion:
			switch {
			case !in.BBR:
				out = append(out, skipped(kv.Key, i18n.TuneSkipBBROff))
			case !f.BBRAvailable:
				out = append(out, skipped(kv.Key, i18n.TuneSkipNoBBR))
			default:
				out = append(out, sysctlItem(kv.Key, kv.Value, i18n.TuneReasonBBR))
			}
		case KeyRmemMax, KeyWmemMax:
			out = append(out, sysctlItem(kv.Key, bufStr, bufReason, bufArgs...))
		case KeyTCPRmem, KeyTCPWmem:
			fields := strings.Fields(kv.Value)
			out = append(out, sysctlItem(kv.Key, fields[0]+" "+fields[1]+" "+bufStr, bufReason, bufArgs...))
			if kv.Key == KeyTCPWmem && useBBR {
				out = append(out, sysctlItem(KeyNotsentLowat, notsentLowat, i18n.TuneReasonNotsentLowat))
			}
		default:
			out = append(out, sysctlItem(kv.Key, kv.Value, baseReason(kv.Key)))
		}
	}

	out = append(out, sysctlItem(KeySlowStartAfterIdle, "0", i18n.TuneReasonSlowStart))
	if in.UDPRungs {
		out = append(out,
			sysctlItem(KeyRmemDefault, udpDefaultBuffer, i18n.TuneReasonUDPDefault),
			sysctlItem(KeyWmemDefault, udpDefaultBuffer, i18n.TuneReasonUDPDefault))
	}
	out = append(out, sysctlItem(KeyNROpen, nrOpenMin, i18n.TuneReasonNROpen))

	var valid []string
	for _, e := range in.Reserved {
		if _, ok := parsePorts(e); !ok || strings.TrimSpace(e) == "" {
			out = append(out, candidate{Change: Change{Kind: api.TuneKindSysctl, Key: KeyReservedPorts},
				skip: &Skip{Key: KeyReservedPorts, Reason: i18n.TuneSkipReservedInvalid, Args: []string{e}}})
			continue
		}
		valid = append(valid, strings.TrimSpace(e))
	}
	if len(valid) > 0 {
		out = append(out, sysctlItem(KeyReservedPorts, strings.Join(valid, ","), i18n.TuneReasonReserved, strings.Join(valid, ",")))
	}

	if !f.ConntrackLoaded && !in.Conntrack {
		out = append(out, candidate{Change: Change{Kind: api.TuneKindSysctl, Key: KeyConntrackMax},
			skip: &Skip{Key: KeyConntrackMax, Reason: i18n.TuneSkipNoConntrack}})
		return out
	}
	ctMax := conntrackSize(f.MemBytes)
	absent := !f.ConntrackLoaded
	ctStr, hsStr := strconv.FormatUint(ctMax, 10), strconv.FormatUint(ctMax/4, 10)
	ct := sysctlItem(KeyConntrackMax, ctStr, i18n.TuneReasonConntrackMax, ctStr, sizeText(f.MemBytes))
	ct.allowAbsent = absent
	hs := candidate{Change: Change{Kind: api.TuneKindSysfs, Key: PathHashsize, To: hsStr,
		Reason: i18n.TuneReasonConntrackBuckets, Effect: api.TuneEffectNow, RaiseOnly: true}, allowAbsent: absent}
	mp := candidate{Change: Change{Kind: api.TuneKindModules, Key: PathModprobe,
		To:     "options nf_conntrack hashsize=" + hsStr,
		Reason: i18n.TuneReasonConntrackPersist, Effect: api.TuneEffectReboot}}
	to := sysctlItem(KeyConntrackEstab, conntrackEstabAuto, i18n.TuneReasonConntrackTimeout)
	to.allowAbsent = absent
	ml := candidate{Change: Change{Kind: api.TuneKindModules, Key: PathModulesLoad, To: conntrackModuleContent,
		Reason: i18n.TuneReasonConntrackModule, Effect: api.TuneEffectReboot}}
	return append(out, ct, hs, mp, to, ml)
}

// baseReason is the reason of a balanced key the computed layer keeps.
func baseReason(key string) i18n.Key {
	switch key {
	case KeySomaxconn, KeySynBacklog, KeyNetdevBacklog:
		return i18n.TuneReasonBacklog
	case "net.ipv4.ip_local_port_range":
		return i18n.TuneReasonPortRange
	case "net.ipv4.tcp_fin_timeout", "net.ipv4.tcp_tw_reuse":
		return i18n.TuneReasonTimeWait
	case "net.ipv4.tcp_keepalive_time", "net.ipv4.tcp_keepalive_intvl", "net.ipv4.tcp_keepalive_probes":
		return i18n.TuneReasonKeepalive
	case "net.ipv4.tcp_fastopen":
		return i18n.TuneReasonFastOpen
	case "net.ipv4.tcp_mtu_probing":
		return i18n.TuneReasonMTUProbing
	case "net.ipv4.udp_rmem_min", "net.ipv4.udp_wmem_min":
		return i18n.TuneReasonUDPMin
	case KeyFileMax:
		return i18n.TuneReasonFileMax
	case KeyIPForward:
		return i18n.TuneReasonIPForward
	}
	return i18n.TuneReasonBacklog
}

// bufferCap is the largest socket buffer for the RAM: 16 MiB below 2 GB,
// 32 MiB below 4 GB, 64 MiB from 4 GB (SI units, like AggressiveMinMemory,
// so a server sold as 4 GB qualifies). Unknown RAM gets the smallest.
func bufferCap(mem uint64) uint64 {
	switch {
	case mem >= 4_000_000_000:
		return 64 * mib
	case mem >= 2_000_000_000:
		return 32 * mib
	}
	return 16 * mib
}

// bufferPlan returns the socket buffer maximum and its reason: the RAM cap,
// or with a measured BDP the next power of two of twice the BDP within
// [8 MiB, RAM cap].
func bufferPlan(mem, bdp uint64) (uint64, i18n.Key, []string) {
	limit := bufferCap(mem)
	if bdp == 0 {
		return limit, i18n.TuneReasonBuffersRAM, []string{sizeText(limit), sizeText(mem)}
	}
	v := nextPow2(2 * bdp)
	v = max(v, minBuffer)
	v = min(v, limit)
	return v, i18n.TuneReasonBuffersBDP, []string{sizeText(v), sizeText(bdp), sizeText(mem)}
}

// nextPow2 is the smallest power of two >= v (v > 0), saturating.
func nextPow2(v uint64) uint64 {
	if v <= 1 {
		return 1
	}
	if v > 1<<62 {
		return 1 << 62
	}
	return uint64(1) << bits.Len64(v-1)
}

// conntrackSize is nf_conntrack_max for the RAM: 64 entries per MiB within
// [65536, 1048576].
func conntrackSize(mem uint64) uint64 {
	n := min(mem>>20, conntrackMaxCap) * conntrackPerMiB
	return min(max(n, conntrackMin), conntrackMaxCap)
}

// sizeText renders a byte count in IEC units ("16 MiB", "1.5 GiB").
func sizeText(b uint64) string {
	switch {
	case b == 0:
		return "?"
	case b >= 1<<30:
		s := strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64)
		return strings.TrimSuffix(s, ".0") + " GiB"
	case b >= 1<<20:
		return strconv.FormatUint(b>>20, 10) + " MiB"
	}
	return strconv.FormatUint(b, 10) + " B"
}

// raiseValue is the value a raise-only key gets: the larger of live and
// target, per field for the tcp_rmem/tcp_wmem triples. A value that does
// not parse gives the target.
func raiseValue(live, target string) string {
	lf, tf := strings.Fields(live), strings.Fields(target)
	if len(lf) != len(tf) || len(tf) == 0 {
		return target
	}
	out := make([]string, len(tf))
	for i := range tf {
		l, err1 := strconv.ParseUint(lf[i], 10, 64)
		t, err2 := strconv.ParseUint(tf[i], 10, 64)
		if err1 != nil || err2 != nil {
			return target
		}
		out[i] = strconv.FormatUint(max(l, t), 10)
	}
	return strings.Join(out, " ")
}

// exceeds reports whether the live value of a raise-only key is above the
// target: the key is then not deyroute's. For the tcp_rmem/tcp_wmem triples
// the maximum (last field) decides; their other fields are only never
// lowered (raiseValue). A value deyroute wrote itself is never above.
func exceeds(live, target string) bool {
	lf, tf := strings.Fields(live), strings.Fields(target)
	if len(lf) != len(tf) || len(tf) == 0 {
		return false
	}
	l, err1 := strconv.ParseUint(lf[len(lf)-1], 10, 64)
	t, err2 := strconv.ParseUint(tf[len(tf)-1], 10, 64)
	return err1 == nil && err2 == nil && l > t
}

// sameValue compares two values of key: reserved ports as port sets, every
// other key with whitespace normalized.
func sameValue(key, a, b string) bool {
	if key == KeyReservedPorts {
		pa, ok1 := parsePorts(a)
		pb, ok2 := parsePorts(b)
		if ok1 && ok2 {
			return pa == pb
		}
	}
	return normalize(a) == normalize(b)
}

// portSet is a set of ports 0-65535.
type portSet [1024]uint64

func (s *portSet) add(lo, hi int) {
	for p := lo; p <= hi; p++ {
		s[p>>6] |= 1 << (p & 63)
	}
}

func (s *portSet) has(p int) bool { return s[p>>6]&(1<<(p&63)) != 0 }

// parsePorts parses "8080,30000-31999" (the kernel's format; spaces and an
// empty value allowed).
func parsePorts(v string) (portSet, bool) {
	var s portSet
	for _, e := range strings.Split(v, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		a, b, isRange := strings.Cut(e, "-")
		lo, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil || lo < 0 || lo > 65535 {
			return portSet{}, false
		}
		hi := lo
		if isRange {
			hi, err = strconv.Atoi(strings.TrimSpace(b))
			if err != nil || hi < lo || hi > 65535 {
				return portSet{}, false
			}
		}
		s.add(lo, hi)
	}
	return s, true
}

// renderPorts renders a set as the kernel does: ascending, merged ranges.
func renderPorts(s portSet) string {
	var parts []string
	for p := 0; p <= 65535; p++ {
		if !s.has(p) {
			continue
		}
		q := p
		for q < 65535 && s.has(q+1) {
			q++
		}
		if q == p {
			parts = append(parts, strconv.Itoa(p))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", p, q))
		}
		p = q
	}
	return strings.Join(parts, ",")
}

// unionPorts adds the entries to the live reservation (never removes one).
func unionPorts(live string, entries []string) string {
	s, ok := parsePorts(live)
	if !ok {
		s = portSet{}
	}
	for _, e := range entries {
		if add, ok := parsePorts(e); ok {
			for i := range s {
				s[i] |= add[i]
			}
		}
	}
	return renderPorts(s)
}

// subtractPorts removes from cur the ports deyroute added (in written but
// not in orig), keeping every reservation someone else made.
func subtractPorts(cur, written, orig string) string {
	c, _ := parsePorts(cur)
	w, _ := parsePorts(written)
	o, _ := parsePorts(orig)
	for i := range c {
		c[i] &^= w[i] &^ o[i]
	}
	return renderPorts(c)
}

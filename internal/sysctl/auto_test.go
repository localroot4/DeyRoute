package sysctl

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

const (
	gib     = uint64(1) << 30
	longMax = "9223372036854775807"
)

// autoOriginals are the keys the automatic profile adds, with typical
// kernel defaults (conntrack loaded).
var autoOriginals = map[string]string{
	KeySlowStartAfterIdle: "1",
	KeyRmemDefault:        "212992",
	KeyWmemDefault:        "212992",
	KeyNROpen:             "1048576",
	KeyReservedPorts:      "",
	KeyConntrackMax:       "65536",
	KeyConntrackCount:     "100",
	KeyConntrackBuckets:   "16384",
	KeyConntrackEstab:     "432000",
	"fs.file-nr":          "1024\t0\t" + longMax,
}

// liveMap is a fake AutoInputs.Live: the originals of both tests plus the
// hashsize; overrides replace values, "-" removes a key.
func liveMap(overrides map[string]string) func(string) (string, bool) {
	vals := map[string]string{PathHashsize: "16384"}
	maps.Copy(vals, originals)
	maps.Copy(vals, autoOriginals)
	for k, v := range overrides {
		if v == "-" {
			delete(vals, k)
			continue
		}
		vals[k] = v
	}
	return func(k string) (string, bool) {
		v, ok := vals[k]
		return normalize(v), ok
	}
}

func facts(mem uint64, cpus int) sysinfo.Facts {
	return sysinfo.Facts{MemBytes: mem, CPUs: cpus, Kernel: "6.8.0-45-generic", BBRAvailable: true, FQAvailable: true,
		ConntrackLoaded: true, ConntrackMax: 65536, ConntrackCount: 100, NIC: "eth0", NICMTU: 1500}
}

func byKey(cs []Change) map[string]Change {
	out := map[string]Change{}
	for _, c := range cs {
		out[c.Key] = c
	}
	return out
}

func skipKeys(p Plan) map[string]Skip {
	out := map[string]Skip{}
	for _, s := range p.Skips {
		out[s.Key] = s
	}
	return out
}

// checkPlanShape holds for every plan: Changes are the Desired items whose
// live value differs, every item has a reason and an effect, NotOwned and
// Desired are disjoint.
func checkPlanShape(t *testing.T, p Plan) {
	t.Helper()
	require.Equal(t, config.SysctlAuto, p.Profile)
	require.Len(t, p.Hash, 32)
	des := byKey(p.Desired)
	require.Len(t, des, len(p.Desired), "a key twice in Desired")
	for _, c := range p.Desired {
		require.NotEmpty(t, c.Reason, c.Key)
		require.NotEqual(t, string(c.Reason), c.ReasonText(), "reason %s has no English text", c.Reason)
		require.NotContains(t, c.ReasonText(), "%!", c.Key)
		require.Contains(t, []string{api.TuneEffectNow, api.TuneEffectReboot}, c.Effect, c.Key)
		require.Contains(t, []string{api.TuneKindSysctl, api.TuneKindSysfs, api.TuneKindModules}, c.Kind, c.Key)
	}
	for _, c := range p.Changes {
		d, ok := des[c.Key]
		require.True(t, ok, "change %s not desired", c.Key)
		require.Equal(t, d, c)
		require.False(t, sameValue(c.Key, c.From, c.To) && c.From != "", c.Key)
	}
	for _, c := range p.NotOwned {
		_, ok := des[c.Key]
		require.False(t, ok, c.Key)
		require.True(t, c.RaiseOnly)
	}
	for _, s := range p.Skips {
		require.NotEqual(t, string(s.Reason), i18n.T(s.Reason, anyArgs(s.Args)...), s.Key)
	}
}

func TestProfileAutoIsBalancedBase(t *testing.T) {
	bal, err := Profile(config.SysctlBalanced, true)
	require.NoError(t, err)
	auto, err := Profile(config.SysctlAuto, true)
	require.NoError(t, err)
	require.Equal(t, bal, auto)
	require.Contains(t, Profiles, config.SysctlAuto)
}

func TestAutoPlanBySize(t *testing.T) {
	cases := []struct {
		name        string
		mem         uint64
		cpus        int
		buf         uint64
		ctMax, hash string
	}{
		{"512MiB-1cpu", 512 << 20, 1, 16 * mib, "65536", "16384"},
		{"2GiB-2cpu", 2 * gib, 2, 32 * mib, "131072", "32768"},
		{"8GiB-8cpu", 8 * gib, 8, 64 * mib, "524288", "131072"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := AutoPlan(facts(tc.mem, tc.cpus), AutoInputs{BBR: true, IPForward: true, UDPRungs: true,
				Reserved: []string{"30000-31999", "8443"}, Live: liveMap(nil)})
			checkPlanShape(t, p)
			des := byKey(p.Desired)
			buf := strconv.FormatUint(tc.buf, 10)
			require.Equal(t, buf, des[KeyRmemMax].To)
			require.Equal(t, buf, des[KeyWmemMax].To)
			require.Equal(t, "4096 131072 "+buf, des[KeyTCPRmem].To, "the live middle value is never lowered")
			require.Equal(t, "4096 65536 "+buf, des[KeyTCPWmem].To)
			require.Equal(t, notsentLowat, des[KeyNotsentLowat].To)
			require.Equal(t, "0", des[KeySlowStartAfterIdle].To)
			require.Equal(t, udpDefaultBuffer, des[KeyRmemDefault].To)
			require.Equal(t, "1", des[KeyIPForward].To)
			require.Equal(t, "bbr", des[KeyCongestion].To)
			require.Equal(t, api.TuneEffectReboot, des[KeyDefaultQdisc].Effect)
			require.Equal(t, "8443,30000-31999", des[KeyReservedPorts].To)
			require.Equal(t, "65535", des[KeySomaxconn].To)
			require.Equal(t, "4096", des[KeySomaxconn].From)
			require.True(t, des[KeySomaxconn].RaiseOnly)
			require.Equal(t, conntrackEstabAuto, des[KeyConntrackEstab].To)
			require.Equal(t, conntrackModuleContent, des[PathModulesLoad].To)
			require.Equal(t, "", des[PathModulesLoad].From)
			require.Equal(t, api.TuneKindModules, des[PathModulesLoad].Kind)
			// nr_open is already 1048576: owned (in the conf) but no change.
			require.Equal(t, nrOpenMin, des[KeyNROpen].To)
			require.NotContains(t, byKey(p.Changes), KeyNROpen)
			// file-max LONG_MAX is never lowered.
			require.NotContains(t, des, KeyFileMax)
			no := byKey(p.NotOwned)
			require.Equal(t, longMax, no[KeyFileMax].From)
			require.Equal(t, "2097152", no[KeyFileMax].To)

			if tc.ctMax == "65536" {
				// Already at the target: owned, no change; hashsize as well,
				// so the modprobe option persists it.
				require.Equal(t, "65536", des[KeyConntrackMax].To)
				require.NotContains(t, byKey(p.Changes), KeyConntrackMax)
			} else {
				require.Equal(t, tc.ctMax, byKey(p.Changes)[KeyConntrackMax].To)
			}
			require.Equal(t, tc.hash, des[PathHashsize].To)
			require.Equal(t, api.TuneKindSysfs, des[PathHashsize].Kind)
			require.Equal(t, "options nf_conntrack hashsize="+tc.hash, des[PathModprobe].To)
			require.Empty(t, p.Skips)
		})
	}
}

func TestAutoPlanContainerSkipsEverything(t *testing.T) {
	f := facts(2*gib, 2)
	f.Virt = sysinfo.VirtOpenVZ
	p := AutoPlan(f, AutoInputs{BBR: true, Reserved: []string{"30000-31999"}, Live: liveMap(nil)})
	checkPlanShape(t, p)
	require.Empty(t, p.Desired)
	require.Empty(t, p.Changes)
	require.Empty(t, p.NotOwned)
	require.NotEmpty(t, p.Skips)
	for _, s := range p.Skips {
		require.Equal(t, deyerr.X064, s.Code, s.Key)
		require.Equal(t, []string{"openvz"}, s.Args)
		require.Contains(t, s.API().Reason, "openvz container")
		require.Equal(t, "DEY-X064", s.API().Code)
	}
	sk := skipKeys(p)
	for _, k := range []string{KeyCongestion, KeySomaxconn, KeyReservedPorts, KeyConntrackMax, PathModulesLoad} {
		require.Contains(t, sk, k)
	}
}

func TestAutoPlanBBRAndFQ(t *testing.T) {
	f := facts(2*gib, 2)
	f.BBRAvailable = false
	p := AutoPlan(f, AutoInputs{BBR: true, Live: liveMap(nil)})
	checkPlanShape(t, p)
	require.Equal(t, i18n.TuneSkipNoBBR, skipKeys(p)[KeyCongestion].Reason)
	require.NotContains(t, byKey(p.Desired), KeyCongestion)
	require.NotContains(t, byKey(p.Desired), KeyNotsentLowat, "notsent_lowat only with BBR")

	p = AutoPlan(facts(2*gib, 2), AutoInputs{BBR: false, Live: liveMap(nil)})
	require.Equal(t, i18n.TuneSkipBBROff, skipKeys(p)[KeyCongestion].Reason)
	require.NotContains(t, byKey(p.Desired), KeyNotsentLowat)

	f = facts(2*gib, 2)
	f.FQAvailable = false
	p = AutoPlan(f, AutoInputs{BBR: true, Live: liveMap(nil)})
	checkPlanShape(t, p)
	require.Equal(t, i18n.TuneSkipNoFQ, skipKeys(p)[KeyDefaultQdisc].Reason)
	require.NotContains(t, byKey(p.Desired), KeyDefaultQdisc)
	require.Contains(t, byKey(p.Desired), KeyCongestion, "BBR paces without fq")
}

func TestAutoPlanConntrackNotLoaded(t *testing.T) {
	f := facts(2*gib, 2)
	f.ConntrackLoaded = false
	gone := map[string]string{KeyConntrackMax: "-", KeyConntrackCount: "-", KeyConntrackBuckets: "-",
		KeyConntrackEstab: "-", PathHashsize: "-"}
	p := AutoPlan(f, AutoInputs{Live: liveMap(gone)})
	checkPlanShape(t, p)
	require.Equal(t, i18n.TuneSkipNoConntrack, skipKeys(p)[KeyConntrackMax].Reason)
	for _, k := range []string{KeyConntrackMax, KeyConntrackEstab, PathHashsize, PathModprobe, PathModulesLoad} {
		require.NotContains(t, byKey(p.Desired), k)
	}

	// Needed (NAT rungs): sized for the boot, applied when it loads.
	p = AutoPlan(f, AutoInputs{Conntrack: true, Live: liveMap(gone)})
	checkPlanShape(t, p)
	des := byKey(p.Desired)
	require.Equal(t, "131072", des[KeyConntrackMax].To)
	require.Equal(t, "", des[KeyConntrackMax].From)
	require.Equal(t, api.TuneEffectReboot, des[KeyConntrackMax].Effect)
	require.Equal(t, conntrackEstabAuto, des[KeyConntrackEstab].To)
	require.NotContains(t, des, PathHashsize, "no sysfs file before the module loads")
	require.Equal(t, "options nf_conntrack hashsize=32768", des[PathModprobe].To)
	require.Equal(t, conntrackModuleContent, des[PathModulesLoad].To)
	require.Contains(t, byKey(p.Changes), KeyConntrackMax)
}

func TestAutoPlanRaiseOnly(t *testing.T) {
	live := liveMap(map[string]string{
		KeySomaxconn:      "131072",
		KeyTCPRmem:        "8192 262144 134217728",
		KeyConntrackMax:   "2000000",
		PathHashsize:      "500000",
		KeyRmemMax:        "1048576",
		KeyConntrackEstab: "3600",
	})
	p := AutoPlan(facts(8*gib, 8), AutoInputs{UDPRungs: true, Live: live})
	checkPlanShape(t, p)
	no := byKey(p.NotOwned)
	require.Equal(t, "131072", no[KeySomaxconn].From)
	require.Equal(t, "65535", no[KeySomaxconn].To)
	require.Contains(t, no[KeySomaxconn].API().Reason, "already 131072")
	require.Contains(t, no, KeyTCPRmem, "every field of the live triple is higher")
	require.Contains(t, no, KeyConntrackMax)
	require.Contains(t, no, PathHashsize)
	des := byKey(p.Desired)
	require.NotContains(t, des, KeySomaxconn)
	require.NotContains(t, des, PathModprobe, "hashsize not owned: no modprobe option")
	require.NotContains(t, des, PathModulesLoad, "no conntrack item owned: no modules-load file")
	require.Equal(t, strconv.FormatUint(64*mib, 10), des[KeyRmemMax].To, "a lower live value is raised")
	require.Equal(t, i18n.TuneSkipTimeoutAdmin, skipKeys(p)[KeyConntrackEstab].Reason)
	require.Equal(t, []string{"3600"}, skipKeys(p)[KeyConntrackEstab].Args)

	// Mixed triple: the larger value of every field.
	p = AutoPlan(facts(8*gib, 8), AutoInputs{Live: liveMap(map[string]string{KeyTCPWmem: "4096 262144 4194304"})})
	require.Equal(t, "4096 262144 67108864", byKey(p.Changes)[KeyTCPWmem].To)

	// somaxconn 4096 is raised; file-max LONG_MAX stays.
	p = AutoPlan(facts(8*gib, 8), AutoInputs{Live: liveMap(nil)})
	require.Equal(t, "4096", byKey(p.Changes)[KeySomaxconn].From)
	require.NotContains(t, byKey(p.Changes), KeyFileMax)
	for _, c := range p.Changes {
		if c.RaiseOnly && c.From != "" {
			require.Equal(t, c.To, raiseValue(c.From, c.To), "%s lowered", c.Key)
		}
	}
}

func TestAutoPlanBuffersFromBDP(t *testing.T) {
	bdp20 := uint64(20_000_000)
	p := AutoPlan(facts(8*gib, 8), AutoInputs{BDPBytes: bdp20, Live: liveMap(nil)})
	c := byKey(p.Desired)[KeyRmemMax]
	require.Equal(t, strconv.FormatUint(64*mib, 10), c.To)
	require.Equal(t, i18n.TuneReasonBuffersBDP, c.Reason)
	require.Contains(t, c.ReasonText(), "64 MiB")

	// Clamped by RAM.
	p = AutoPlan(facts(2*gib, 2), AutoInputs{BDPBytes: bdp20, Live: liveMap(nil)})
	require.Equal(t, strconv.FormatUint(32*mib, 10), byKey(p.Desired)[KeyRmemMax].To)
	// At least 8 MiB.
	p = AutoPlan(facts(8*gib, 8), AutoInputs{BDPBytes: 100_000, Live: liveMap(nil)})
	require.Equal(t, strconv.FormatUint(8*mib, 10), byKey(p.Desired)[KeyRmemMax].To)
	// Unknown RAM: the smallest cap.
	p = AutoPlan(facts(0, 1), AutoInputs{Live: liveMap(nil)})
	require.Equal(t, strconv.FormatUint(16*mib, 10), byKey(p.Desired)[KeyRmemMax].To)

	require.Equal(t, uint64(1), nextPow2(0))
	require.Equal(t, 64*mib, nextPow2(40_000_000))
	require.Equal(t, uint64(1)<<62, nextPow2(1<<62+1))
	require.Equal(t, "1.5 GiB", sizeText(3*gib/2))
	require.Equal(t, "2 GiB", sizeText(2*gib))
	require.Equal(t, "100 B", sizeText(100))
	require.Equal(t, uint64(1048576), conntrackSize(64*gib))
}

func TestAutoPlanReservedPorts(t *testing.T) {
	p := AutoPlan(facts(2*gib, 2), AutoInputs{Reserved: []string{"30000-31999", " 44433", "bad", "70000"},
		Live: liveMap(map[string]string{KeyReservedPorts: "8080,30000-30010"})})
	checkPlanShape(t, p)
	c := byKey(p.Changes)[KeyReservedPorts]
	require.Equal(t, "8080,30000-31999,44433", c.To, "a set union: the live entries stay")
	require.Equal(t, "8080,30000-30010", c.From)
	var invalid []string
	for _, s := range p.Skips {
		if s.Reason == i18n.TuneSkipReservedInvalid {
			invalid = append(invalid, s.Args[0])
		}
	}
	require.Equal(t, []string{"bad", "70000"}, invalid)

	// Already reserved: owned, no change.
	p = AutoPlan(facts(2*gib, 2), AutoInputs{Reserved: []string{"8080"}, Live: liveMap(map[string]string{KeyReservedPorts: "8080, 9000"})})
	require.Contains(t, byKey(p.Desired), KeyReservedPorts)
	require.NotContains(t, byKey(p.Changes), KeyReservedPorts)

	require.Equal(t, "1-3,5", renderPorts(must(parsePorts("3,1-2,5"))))
	require.Equal(t, "", renderPorts(must(parsePorts(""))))
	require.Equal(t, "0,65535", renderPorts(must(parsePorts("65535,0"))))
	_, ok := parsePorts("5-1")
	require.False(t, ok)
	require.Equal(t, "8080,9090", subtractPorts("8080,9090,30000-31999", "8080,30000-31999", "8080"))
}

func must(s portSet, ok bool) portSet {
	if !ok {
		panic("parse")
	}
	return s
}

func TestAutoPlanMissingKeyAndNilLive(t *testing.T) {
	p := AutoPlan(facts(2*gib, 2), AutoInputs{Live: liveMap(map[string]string{"net.ipv4.tcp_fastopen": "-"})})
	require.Equal(t, i18n.TuneSkipMissing, skipKeys(p)["net.ipv4.tcp_fastopen"].Reason)

	p = AutoPlan(facts(2*gib, 2), AutoInputs{})
	require.Empty(t, p.Desired, "nothing is known: every sysctl key is missing")
	require.NotEmpty(t, p.Skips)
}

func TestAutoPlanHash(t *testing.T) {
	in := AutoInputs{BBR: true, Reserved: []string{"30000-31999"}, Live: liveMap(nil)}
	a := AutoPlan(facts(2*gib, 2), in)
	for i := 0; i < 20; i++ {
		// Fresh maps each time: map iteration order never matters.
		b := AutoPlan(facts(2*gib, 2), AutoInputs{BBR: true, Reserved: []string{"30000-31999"}, Live: liveMap(nil)})
		require.Equal(t, a.Hash, b.Hash)
		require.Equal(t, a.DesiredHash(), b.DesiredHash())
	}
	require.NotEqual(t, a.Hash, AutoPlan(facts(8*gib, 2), in).Hash, "RAM changed")
	f := facts(2*gib, 2)
	f.BBRAvailable = false
	require.NotEqual(t, a.Hash, AutoPlan(f, in).Hash, "BBR availability changed")
	in2 := in
	in2.Live = liveMap(map[string]string{KeySomaxconn: "1024"})
	c := AutoPlan(facts(2*gib, 2), in2)
	require.NotEqual(t, a.Hash, c.Hash, "a live value changed the diff")
	require.Equal(t, a.DesiredHash(), c.DesiredHash(), "but not what auto owns")

	require.Len(t, a.APIChanges(), len(a.Changes))
	require.Len(t, a.APISkips(), len(a.Skips))
	require.Equal(t, a.Changes[0].Key, a.APIChanges()[0].Key)
}

// ---------------------------------------------------------------- apply

// autoRoot is fakeRoot plus the keys and files of the automatic profile,
// 8 GiB of RAM and sch_fq as a module.
func autoRoot(t *testing.T) (Manager, string) {
	t.Helper()
	m, root := fakeRoot(t)
	for k, v := range autoOriginals {
		writeProc(t, root, k, v)
	}
	writeHostFile(t, root, PathHashsize, "16384\n")
	writeHostFile(t, root, "proc/meminfo", fmt.Sprintf("MemTotal: %d kB\nMemAvailable: %d kB\n", 8*gib>>10, 4*gib>>10))
	writeHostFile(t, root, "lib/modules/6.8.0-45-generic/modules.dep", "kernel/net/sched/sch_fq.ko.zst:\n")
	return m, root
}

func writeProc(t *testing.T, root, key, value string) {
	t.Helper()
	writeHostFile(t, root, filepath.Join("proc", "sys", strings.ReplaceAll(key, ".", "/")), value+"\n")
}

func writeHostFile(t *testing.T, root, p, content string) {
	t.Helper()
	full := filepath.Join(root, p)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
}

func readHostFile(t *testing.T, root, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, p))
	require.NoError(t, err)
	return string(b)
}

func planFor(m Manager, mem uint64, in AutoInputs) Plan {
	f := facts(mem, 4)
	in.Live = m.Live
	return AutoPlan(f, in)
}

// snapshot records every file below root.
func snapshot(t *testing.T, root string) map[string]os.FileInfo {
	t.Helper()
	out := map[string]os.FileInfo{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := os.Lstat(p)
		out[p] = fi
		return err
	}))
	return out
}

// requireUnchanged fails when a file was added, removed, replaced or
// modified since the snapshot.
func requireUnchanged(t *testing.T, before map[string]os.FileInfo, root string) {
	t.Helper()
	after := snapshot(t, root)
	require.Equal(t, len(before), len(after))
	for p, a := range after {
		b, ok := before[p]
		require.True(t, ok, "new file %s", p)
		require.True(t, os.SameFile(a, b), "%s replaced", p)
		require.Equal(t, b.ModTime(), a.ModTime(), "%s modified", p)
	}
}

// countWrites counts kernel writes until the test ends.
func countWrites(t *testing.T) *int {
	t.Helper()
	orig := writeKernel
	n := new(int)
	writeKernel = func(p, v string) error {
		*n++
		return orig(p, v)
	}
	t.Cleanup(func() { writeKernel = orig })
	return n
}

func TestApplyPlanBackupIdempotenceAndRevert(t *testing.T) {
	m, root := autoRoot(t)
	in := AutoInputs{BBR: true, UDPRungs: true, Reserved: []string{"30000-31999", "8443"}}
	p := planFor(m, 8*gib, in)
	applied, warnings, err := m.ApplyPlan(p)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, applied, len(p.Desired))

	conf := readHostFile(t, root, config.SysctlConfPath)
	require.True(t, strings.HasPrefix(conf, "# managed by deyroute (profile: auto)\n# plan "+p.DesiredHash()+" "), conf)
	require.Contains(t, conf, "net.core.rmem_max = 67108864\n")
	require.Contains(t, conf, "net.netfilter.nf_conntrack_max = 524288\n")
	require.Contains(t, conf, KeyReservedPorts+" = 8443,30000-31999\n")
	require.NotContains(t, conf, KeyFileMax, "not owned: LONG_MAX stays")
	cur, err := m.Current()
	require.NoError(t, err)
	require.Equal(t, config.SysctlAuto, cur)

	require.Equal(t, "131072", get2(t, m, PathHashsize))
	require.Equal(t, "524288", get(t, m, KeyConntrackMax))
	require.Equal(t, "86400", get(t, m, KeyConntrackEstab))
	require.Equal(t, "8443,30000-31999", get(t, m, KeyReservedPorts))
	require.Equal(t, "nf_conntrack\n", readHostFile(t, root, PathModulesLoad))
	require.Equal(t, "options nf_conntrack hashsize=131072\n", readHostFile(t, root, PathModprobe))

	backup := readHostFile(t, root, config.SysctlBackup)
	require.Contains(t, backup, "net.netfilter.nf_conntrack_max = 65536\n")
	require.Contains(t, backup, "#@orig "+PathHashsize+" = \"16384\\n\"\n")
	require.Contains(t, backup, "#@orig "+PathModulesLoad+" absent\n")
	require.Contains(t, backup, "#@written net.netfilter.nf_conntrack_max = 524288\n")
	require.NotContains(t, backup, KeyFileMax)

	// Computed again after the apply: nothing to change, nothing written.
	p2 := planFor(m, 8*gib, in)
	require.Empty(t, p2.Changes)
	require.Equal(t, p.DesiredHash(), p2.DesiredHash())
	before := snapshot(t, root)
	writes := countWrites(t)
	time.Sleep(10 * time.Millisecond)
	_, warnings, err = m.ApplyPlan(p2)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Zero(t, *writes)
	requireUnchanged(t, before, root)
	// The same plan again (stale Changes) writes nothing either.
	_, _, err = m.ApplyPlan(p)
	require.NoError(t, err)
	require.Zero(t, *writes)
	requireUnchanged(t, before, root)

	warnings, err = m.RevertWithWarnings()
	require.NoError(t, err)
	require.Empty(t, warnings)
	for k, v := range originals {
		require.Equal(t, normalize(v), get(t, m, k), k)
	}
	for k, v := range autoOriginals {
		require.Equal(t, normalize(v), get(t, m, k), k)
	}
	require.Equal(t, "16384", get2(t, m, PathHashsize))
	for _, p := range []string{PathModulesLoad, PathModprobe, config.SysctlConfPath, config.SysctlBackup} {
		_, err := os.Stat(filepath.Join(root, p))
		require.True(t, os.IsNotExist(err), p)
	}
}

func get2(t *testing.T, m Manager, key string) string {
	t.Helper()
	v, ok := m.Live(key)
	require.True(t, ok, key)
	return v
}

// balanced → auto → auto: the conf stays the same between the auto runs, no
// value goes down, the third run writes nothing, and a key balanced had
// raised that auto keeps (somaxconn at its target) stays in the conf.
func TestBalancedThenAutoNeverLowers(t *testing.T) {
	m, root := autoRoot(t)
	_, _, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	after := map[string]string{}
	for k := range originals {
		after[k] = get(t, m, k)
	}
	in := AutoInputs{BBR: true}
	_, warnings, err := m.ApplyPlan(planFor(m, 8*gib, in))
	require.NoError(t, err)
	require.Empty(t, warnings)
	conf := readHostFile(t, root, config.SysctlConfPath)
	require.Contains(t, conf, "net.core.somaxconn = 65535\n")
	for k, v := range after {
		if !raiseOnly[k] {
			continue
		}
		cur := get(t, m, k)
		if a, err := strconv.ParseUint(v, 10, 64); err == nil {
			c, err := strconv.ParseUint(cur, 10, 64)
			require.NoError(t, err, k)
			require.GreaterOrEqual(t, c, a, "%s went down", k)
		}
	}

	before := snapshot(t, root)
	writes := countWrites(t)
	time.Sleep(10 * time.Millisecond)
	p3 := planFor(m, 8*gib, in)
	require.Empty(t, p3.Changes)
	_, warnings, err = m.ApplyPlan(p3)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Zero(t, *writes)
	requireUnchanged(t, before, root)
	require.Equal(t, conf, readHostFile(t, root, config.SysctlConfPath))
}

func TestApplyPlanSkipsKeyRaisedSincePlan(t *testing.T) {
	m, root := autoRoot(t)
	p := planFor(m, 8*gib, AutoInputs{})
	require.Equal(t, "65535", byKey(p.Changes)[KeySomaxconn].To)
	writeProc(t, root, KeySomaxconn, "200000")
	_, _, err := m.ApplyPlan(p)
	require.NoError(t, err)
	require.Equal(t, "200000", get(t, m, KeySomaxconn))
	require.NotContains(t, readHostFile(t, root, config.SysctlConfPath), KeySomaxconn)
	// Reserved ports added after the plan stay.
	writeProc(t, root, KeyReservedPorts, "9000")
	p = planFor(m, 8*gib, AutoInputs{Reserved: []string{"30000-31999"}})
	writeProc(t, root, KeyReservedPorts, "9000,9100")
	_, _, err = m.ApplyPlan(p)
	require.NoError(t, err)
	require.Equal(t, "9000,9100,30000-31999", get(t, m, KeyReservedPorts))
}

func TestRevertCompareAndRestore(t *testing.T) {
	m, root := autoRoot(t)
	_, _, err := m.ApplyPlan(planFor(m, 8*gib, AutoInputs{BBR: true, Reserved: []string{"30000-31999"}}))
	require.NoError(t, err)
	// Another program changes keys after deyroute.
	writeProc(t, root, "net.ipv4.tcp_fin_timeout", "30")
	writeProc(t, root, KeyReservedPorts, "9090,30000-31999")
	writeHostFile(t, root, PathModprobe, "options nf_conntrack hashsize=1\n")

	warnings, err := m.RevertWithWarnings()
	require.NoError(t, err)
	require.Equal(t, []string{
		"net.ipv4.tcp_fin_timeout changed by another program after deyroute; left at 30",
		PathModprobe + " changed by another program after deyroute; left at options nf_conntrack hashsize=1",
	}, warnings)
	require.Equal(t, "30", get(t, m, "net.ipv4.tcp_fin_timeout"))
	require.Equal(t, "9090", get(t, m, KeyReservedPorts), "only deyroute's entries are removed")
	require.Equal(t, "75", get(t, m, "net.ipv4.tcp_keepalive_intvl"), "untouched keys restored")
	require.Equal(t, "65536", get(t, m, KeyConntrackMax))
	_, err = os.Stat(m.BackupPath())
	require.True(t, os.IsNotExist(err), "warnings are not failures: the backup goes")
	_, err = os.Stat(filepath.Join(root, PathModulesLoad))
	require.True(t, os.IsNotExist(err))
}

func TestRevertFailedWriteKeepsBackupAuto(t *testing.T) {
	m, root := autoRoot(t)
	_, _, err := m.ApplyPlan(planFor(m, 8*gib, AutoInputs{}))
	require.NoError(t, err)
	undo := failWrites(t, root, KeyConntrackEstab, PathHashsize)
	_, err = m.RevertWithWarnings()
	require.True(t, deyerr.HasCode(err, deyerr.X033), "%v", err)
	require.Contains(t, deyerr.As(err).Message(), KeyConntrackEstab)
	_, statErr := os.Stat(m.BackupPath())
	require.NoError(t, statErr, "backup kept for a retry")
	require.Equal(t, "65536", get(t, m, KeyConntrackMax), "the rest is restored")
	undo()
	warnings, err := m.RevertWithWarnings()
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, "432000", get(t, m, KeyConntrackEstab))
	require.Equal(t, "16384", get2(t, m, PathHashsize))
}

// DECISIONS item 9: limit keys are never restored below max(original,
// usage x 1.25).
func TestRevertNeverLowersLimitsInUse(t *testing.T) {
	m, root := autoRoot(t)
	writeProc(t, root, KeyFileMax, "100000")
	writeProc(t, root, "fs.file-nr", "1000\t0\t100000")
	_, _, err := m.ApplyPlan(planFor(m, 8*gib, AutoInputs{}))
	require.NoError(t, err)
	require.Equal(t, "2097152", get(t, m, KeyFileMax))
	require.Equal(t, "524288", get(t, m, KeyConntrackMax))

	// Under load: 70000 tracked flows (above 65536 / 1.25), 90000 files.
	writeProc(t, root, KeyConntrackCount, "70000")
	writeProc(t, root, "fs.file-nr", "90000\t0\t2097152")
	warnings, err := m.RevertWithWarnings()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"net.netfilter.nf_conntrack_max kept at 524288 because 70000 are in use; its earlier value 65536 returns at the next boot",
		"fs.file-max kept at 2097152 because 90000 are in use; its earlier value 100000 returns at the next boot",
		PathHashsize + " kept at 131072 because 17500 are in use; its earlier value 16384 returns at the next boot",
	}, warnings)
	require.Equal(t, "524288", get(t, m, KeyConntrackMax))
	require.Equal(t, "2097152", get(t, m, KeyFileMax))
	require.Equal(t, "131072", get2(t, m, PathHashsize))
	require.Equal(t, "432000", get(t, m, KeyConntrackEstab), "not a limit: restored")
	_, err = os.Stat(m.ConfPath())
	require.True(t, os.IsNotExist(err), "the conf goes: the original applies at the next boot")

	// Light load: restored.
	m2, root2 := autoRoot(t)
	_, _, err = m2.ApplyPlan(planFor(m2, 8*gib, AutoInputs{}))
	require.NoError(t, err)
	writeProc(t, root2, KeyConntrackCount, "50000") // 62500 < 65536
	warnings, err = m2.RevertWithWarnings()
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, "65536", get(t, m2, KeyConntrackMax))

	// nr_open: the largest open-files limit of a running process counts.
	m3, root3 := autoRoot(t)
	writeProc(t, root3, KeyNROpen, "65536")
	_, _, err = m3.ApplyPlan(planFor(m3, 8*gib, AutoInputs{}))
	require.NoError(t, err)
	require.Equal(t, "1048576", get(t, m3, KeyNROpen))
	writeHostFile(t, root3, "proc/1234/limits", "Limit                     Soft Limit           Hard Limit           Units\nMax open files            1024                 524288               files\n")
	writeHostFile(t, root3, "proc/1/limits", "Max open files            1024                 unlimited            files\n")
	warnings, err = m3.RevertWithWarnings()
	require.NoError(t, err)
	require.Equal(t, []string{"fs.nr_open kept at 1048576 because 524288 are in use; its earlier value 65536 returns at the next boot"}, warnings)
}

func TestRevertKeepsIPForwardOthersNeed(t *testing.T) {
	m, root := fakeRoot(t)
	require.NoError(t, m.Ensure(KeyIPForward, "1"))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/class/net/docker0"), 0o750))
	warnings, err := m.RevertWithWarnings()
	require.NoError(t, err)
	require.Equal(t, []string{"net.ipv4.ip_forward kept at 1: docker0 also forwards packets"}, warnings)
	require.Equal(t, "1", get(t, m, KeyIPForward))

	m2, root2 := fakeRoot(t)
	require.NoError(t, m2.Ensure(KeyIPForward, "1"))
	writeHostFile(t, root2, "usr/lib/sysctl.d/50-libvirt.conf", "net/ipv4/ip_forward = 1\n")
	warnings, err = m2.RevertWithWarnings()
	require.NoError(t, err)
	require.Equal(t, []string{"net.ipv4.ip_forward kept at 1: /usr/lib/sysctl.d/50-libvirt.conf also forwards packets"}, warnings)

	// The ledger: Ensure records what it wrote, so a later change by
	// someone else is not undone.
	m3, root3 := fakeRoot(t)
	require.NoError(t, m3.Ensure(KeyNotsentLowat, "16384"))
	require.Contains(t, readHostFile(t, root3, config.SysctlBackup), "#@written "+KeyNotsentLowat+" = 16384\n")
	writeProc(t, root3, KeyNotsentLowat, "32768")
	warnings, err = m3.RevertWithWarnings()
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Equal(t, "32768", get(t, m3, KeyNotsentLowat))
}

// Switching from auto to balanced restores what only auto owned (conntrack,
// reserved ports, the module files); a key changed by someone else is
// given up once with a warning instead of being lowered on every apply.
func TestAutoToBalanced(t *testing.T) {
	m, root := autoRoot(t)
	writeProc(t, root, KeyReservedPorts, "8080")
	_, _, err := m.ApplyWith(ApplyOptions{Profile: config.SysctlAuto, BBR: true, UDPRungs: true, Reserved: []string{"30000-31999"}})
	require.NoError(t, err)
	require.Equal(t, "524288", get(t, m, KeyConntrackMax), "facts read from the fake host: 8 GiB")
	require.Equal(t, "fq", get(t, m, KeyDefaultQdisc), "sch_fq found in modules.dep")
	require.Equal(t, "8080,30000-31999", get(t, m, KeyReservedPorts))
	writeProc(t, root, KeyRmemDefault, "4194304") // someone else, after deyroute

	_, warnings, err := m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Equal(t, []string{KeyRmemDefault + " changed by another program after deyroute; left at 4194304"}, warnings)
	require.Equal(t, "1", get(t, m, KeySlowStartAfterIdle), "restored")
	require.Equal(t, "4194304", get(t, m, KeyRmemDefault))
	require.Equal(t, "65536", get(t, m, KeyConntrackMax))
	require.Equal(t, "432000", get(t, m, KeyConntrackEstab))
	require.Equal(t, "8080", get(t, m, KeyReservedPorts))
	require.Equal(t, "16384", get2(t, m, PathHashsize))
	_, err = os.Stat(filepath.Join(root, PathModulesLoad))
	require.True(t, os.IsNotExist(err))
	_, warnings, err = m.Apply(config.SysctlBalanced, false)
	require.NoError(t, err)
	require.Empty(t, warnings, "the warning is given once")
	b, err := m.readBackup()
	require.NoError(t, err)
	_, has := b.original(KeyRmemDefault)
	require.False(t, has)
	require.Empty(t, b.Files[PathModulesLoad].Content)

	require.NoError(t, m.Revert())
	for k, v := range originals {
		require.Equal(t, normalize(v), get(t, m, k), k)
	}
}

func TestApplyWithAutoInContainer(t *testing.T) {
	m, root := autoRoot(t)
	writeHostFile(t, root, ".dockerenv", "")
	applied, warnings, err := m.ApplyWith(ApplyOptions{Profile: config.SysctlAuto, BBR: true})
	require.NoError(t, err)
	require.Empty(t, applied)
	require.NotEmpty(t, warnings)
	require.Contains(t, warnings[0], "docker container")
	require.Equal(t, "4096", get(t, m, KeySomaxconn))
	// The given plan is applied as it is.
	p := planFor(m, 2*gib, AutoInputs{})
	applied, _, err = m.ApplyWith(ApplyOptions{Profile: config.SysctlAuto, Plan: &p})
	require.NoError(t, err)
	require.NotEmpty(t, applied)
	require.Equal(t, "131072", get(t, m, KeyConntrackMax))
}

func TestReassert(t *testing.T) {
	m, root := autoRoot(t)
	ct := []string{KeyConntrackMax, KeyConntrackCount, KeyConntrackBuckets, KeyConntrackEstab}
	for _, k := range ct {
		require.NoError(t, os.Remove(filepath.Join(root, "proc/sys", strings.ReplaceAll(k, ".", "/"))))
	}
	require.NoError(t, os.Remove(filepath.Join(root, PathHashsize)))
	f := facts(8*gib, 4)
	f.ConntrackLoaded = false
	_, _, err := m.ApplyPlan(AutoPlan(f, AutoInputs{Conntrack: true, Live: m.Live}))
	require.NoError(t, err)
	require.Contains(t, readHostFile(t, root, config.SysctlConfPath), "net.netfilter.nf_conntrack_max = 524288\n")
	require.Contains(t, readHostFile(t, root, PathModprobe), "hashsize=131072")

	// Nothing loaded: nothing to do.
	got, warnings, err := m.Reassert()
	require.NoError(t, err)
	require.Empty(t, got)
	require.Empty(t, warnings)

	// The module loads later (kernel defaults): the keys are reasserted once.
	for _, k := range ct {
		writeProc(t, root, k, autoOriginals[k])
	}
	writeProc(t, root, KeySomaxconn, "1000") // not a conntrack key: never touched
	got, _, err = m.Reassert()
	require.NoError(t, err)
	require.Equal(t, []KV{{KeyConntrackMax, "524288"}, {KeyConntrackEstab, "86400"}}, got)
	require.Equal(t, "524288", get(t, m, KeyConntrackMax))
	require.Equal(t, "1000", get(t, m, KeySomaxconn))
	require.Contains(t, readHostFile(t, root, config.SysctlBackup), "net.netfilter.nf_conntrack_max = 65536\n")
	got, _, err = m.Reassert()
	require.NoError(t, err)
	require.Empty(t, got)

	// Changed at runtime by the admin (not the original): left alone.
	writeProc(t, root, KeyConntrackMax, "70000")
	got, _, err = m.Reassert()
	require.NoError(t, err)
	require.Empty(t, got)
	// A file sorted after deyroute's sets it: left alone.
	writeProc(t, root, KeyConntrackMax, "65536")
	writeHostFile(t, root, "etc/sysctl.d/99-zz-admin.conf", "net.netfilter.nf_conntrack_max = 65536\n")
	got, _, err = m.Reassert()
	require.NoError(t, err)
	require.Empty(t, got)
	// The revert restores the original recorded by Reassert.
	require.NoError(t, os.Remove(filepath.Join(root, "etc/sysctl.d/99-zz-admin.conf")))
	got, _, err = m.Reassert()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NoError(t, m.Revert())
	require.Equal(t, "65536", get(t, m, KeyConntrackMax))

	// No conf: nothing.
	got, _, err = m.Reassert()
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestReassertFailedWrite(t *testing.T) {
	m, root := autoRoot(t)
	_, _, err := m.ApplyPlan(planFor(m, 8*gib, AutoInputs{}))
	require.NoError(t, err)
	writeProc(t, root, KeyConntrackEstab, "432000") // module reloaded
	b, _ := m.readBackup()
	require.Equal(t, "432000", must2(b.original(KeyConntrackEstab)))
	failWrites(t, root, KeyConntrackEstab)
	got, warnings, err := m.Reassert()
	require.NoError(t, err)
	require.Empty(t, got)
	require.Len(t, warnings, 1)
	require.True(t, strings.HasPrefix(warnings[0], "failed "+KeyConntrackEstab), warnings[0])
}

func must2(v string, ok bool) string {
	if !ok {
		return "<missing>"
	}
	return v
}

func TestBackupFormat(t *testing.T) {
	b := backupData{
		KVs:     []KV{{"net.core.somaxconn", "4096"}},
		Files:   map[string]fileOrig{PathModulesLoad: {}, PathHashsize: {Content: "16384\n", Exists: true}, "/etc/passwd": {}},
		Written: map[string]string{"net.core.somaxconn": "65535", PathHashsize: "131072"},
	}
	data := renderBackup(b)
	require.Equal(t, `# Kernel settings before deyroute changed them; restored by "deyroute optimize revert".
net.core.somaxconn = 4096
#@orig /etc/modules-load.d/deyroute.conf absent
#@orig /etc/passwd absent
#@orig /sys/module/nf_conntrack/parameters/hashsize = "16384\n"
#@written /sys/module/nf_conntrack/parameters/hashsize = 131072
#@written net.core.somaxconn = 65535
`, string(data))
	m, root := fakeRoot(t)
	writeHostFile(t, root, config.SysctlBackup, string(data)+"#@orig /etc/x = \"y\"\n#@orig "+PathModprobe+" = bad\n#@written ../x = 1\n")
	got, err := m.readBackup()
	require.NoError(t, err)
	delete(b.Files, "/etc/passwd") // not a plan file: ignored
	require.Equal(t, b, got)
	// Older versions read the same file: only the key = value lines count.
	require.Equal(t, b.KVs, parseKV(data))
}

// A modules-load file that existed before deyroute gets its content back.
func TestRevertRestoresAnEarlierModulesFile(t *testing.T) {
	m, root := autoRoot(t)
	writeHostFile(t, root, PathModulesLoad, "# admin\nnf_conntrack\n")
	_, _, err := m.ApplyPlan(planFor(m, 8*gib, AutoInputs{}))
	require.NoError(t, err)
	require.Equal(t, "nf_conntrack\n", readHostFile(t, root, PathModulesLoad))
	require.Contains(t, readHostFile(t, root, config.SysctlBackup), `#@orig `+PathModulesLoad+` = "# admin\nnf_conntrack\n"`)
	require.NoError(t, m.Revert())
	require.Equal(t, "# admin\nnf_conntrack\n", readHostFile(t, root, PathModulesLoad))

	_, ok := m.Live(PathModprobe)
	require.False(t, ok)
	_, ok = m.Live("../etc/passwd")
	require.False(t, ok)
}

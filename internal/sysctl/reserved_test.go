package sysctl

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/sysinfo"
)

// reservedDrift returns the drift item of the reserved ports, if any.
func reservedDrift(t *testing.T, m Manager) (DriftItem, bool) {
	t.Helper()
	drift, _, err := m.Check(sysinfo.Facts{})
	require.NoError(t, err)
	for _, d := range drift {
		if d.Key == KeyReservedPorts {
			return d, true
		}
	}
	return DriftItem{}, false
}

// TestReservedPortsLedgerKeepsOthers: balanced and aggressive reserve the
// ports of ApplyOptions.Reserved like auto (a set union with the live
// value, written to 99-deyroute.conf too). The ledger records the previous
// ledger plus the entries deyroute requested, never the live union, so a
// later apply, a profile without reserved ports and the revert remove only
// deyroute's entries: a reservation another program made after deyroute
// stays.
func TestReservedPortsLedgerKeepsOthers(t *testing.T) {
	for _, profile := range []string{config.SysctlBalanced, config.SysctlAggressive, config.SysctlAuto} {
		t.Run(profile, func(t *testing.T) {
			m, root := autoRoot(t)
			writeProc(t, root, KeyReservedPorts, "5000")
			o := ApplyOptions{Profile: profile, BBR: true, Reserved: []string{"30000-31999", "8443"}}
			_, _, err := m.ApplyWith(o)
			require.NoError(t, err)
			require.Equal(t, "5000,8443,30000-31999", get(t, m, KeyReservedPorts))
			require.Contains(t, readHostFile(t, root, config.SysctlConfPath), "\n"+KeyReservedPorts+" = 5000,8443,30000-31999\n")
			b, err := m.readBackup()
			require.NoError(t, err)
			require.Equal(t, "8443,30000-31999", b.Written[KeyReservedPorts])

			// Another program reserves 2053; the next apply keeps it live
			// and out of the ledger.
			writeProc(t, root, KeyReservedPorts, "2053,5000,8443,30000-31999")
			_, _, err = m.ApplyWith(o)
			require.NoError(t, err)
			require.Equal(t, "2053,5000,8443,30000-31999", get(t, m, KeyReservedPorts))
			b, err = m.readBackup()
			require.NoError(t, err)
			require.Equal(t, "8443,30000-31999", b.Written[KeyReservedPorts])

			require.NoError(t, m.Revert())
			require.Equal(t, "2053,5000", get(t, m, KeyReservedPorts))
		})
	}

	// A profile without reserved ports (the hub's own optimize apply
	// --profile balanced) gives back only deyroute's entries.
	m, root := autoRoot(t)
	writeProc(t, root, KeyReservedPorts, "5000")
	_, _, err := m.ApplyWith(ApplyOptions{Profile: config.SysctlBalanced, BBR: true, Reserved: []string{"30000-31999", "8443"}})
	require.NoError(t, err)
	writeProc(t, root, KeyReservedPorts, "2053,5000,8443,30000-31999")
	_, _, err = m.ApplyWith(ApplyOptions{Profile: config.SysctlBalanced, BBR: true})
	require.NoError(t, err)
	require.Equal(t, "2053,5000", get(t, m, KeyReservedPorts))
	require.NotContains(t, readHostFile(t, root, config.SysctlConfPath), KeyReservedPorts)

	// An invalid entry is skipped with a warning; a kernel without the key
	// skips it the same way.
	_, warnings, err := m.ApplyWith(ApplyOptions{Profile: config.SysctlBalanced, BBR: true, Reserved: []string{"30000-31999", "70000"}})
	require.NoError(t, err)
	require.Contains(t, warnings, "skip "+KeyReservedPorts+" entry 70000: not a port or port range")
	require.Equal(t, "2053,5000,30000-31999", get(t, m, KeyReservedPorts))
}

// TestCheckReservedPortsSuperset: the reserved ports drift only when the
// live value (or a sysctl file applied after deyroute's) lacks a port
// deyroute wants; a reservation added or removed by another program is no
// drift.
func TestCheckReservedPortsSuperset(t *testing.T) {
	m, root := autoRoot(t)
	writeProc(t, root, KeyReservedPorts, "5000")
	_, _, err := m.ApplyWith(ApplyOptions{Profile: config.SysctlBalanced, BBR: true, Reserved: []string{"30000-31999", "8443"}})
	require.NoError(t, err)
	_, has := reservedDrift(t, m)
	require.False(t, has)

	writeProc(t, root, KeyReservedPorts, "2053,5000,8443,30000-31999")
	_, has = reservedDrift(t, m)
	require.False(t, has, "another program added a port")
	writeProc(t, root, KeyReservedPorts, "8443,30000-31999")
	_, has = reservedDrift(t, m)
	require.False(t, has, "another program removed its own port")
	writeProc(t, root, KeyReservedPorts, "5000,8443,30000-30999")
	d, has := reservedDrift(t, m)
	require.True(t, has, "a port deyroute wants is missing")
	require.Equal(t, "5000,8443,30000-30999", d.Live)

	writeProc(t, root, KeyReservedPorts, "5000,8443,30000-31999")
	writeHostFile(t, root, "etc/sysctl.d/99-zz.conf", KeyReservedPorts+" = 2053,8443,30000-31999\n")
	_, has = reservedDrift(t, m)
	require.False(t, has, "a later file that keeps deyroute's ports")
	writeHostFile(t, root, "etc/sysctl.d/99-zz.conf", KeyReservedPorts+" = 2053\n")
	d, has = reservedDrift(t, m)
	require.True(t, has)
	require.Equal(t, "/etc/sysctl.d/99-zz.conf", d.OverriddenBy)
}

// TestFreeEphemeral: the ephemeral ports a profile leaves free after the
// reserved entries (balanced and auto use 10240-65535; off has no range).
func TestFreeEphemeral(t *testing.T) {
	free, lo, hi := FreeEphemeral(config.SysctlBalanced, []string{"30000-31999"})
	require.Equal(t, [3]int{55296 - 2000, 10240, 65535}, [3]int{free, lo, hi})
	free, _, _ = FreeEphemeral(config.SysctlAuto, []string{"30000-31999", "443", "20000", "30500"})
	require.Equal(t, 55296-2001, free, "ports below the range and overlaps count once")
	free, _, _ = FreeEphemeral(config.SysctlBalanced, []string{"10240-64600"})
	require.Equal(t, 935, free)
	free, lo, hi = FreeEphemeral(config.SysctlOff, nil)
	require.Equal(t, [3]int{0, 0, 0}, [3]int{free, lo, hi})
}

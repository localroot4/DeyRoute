package render

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/firewall"
)

// StatsSpec is the traffic accounting table (`table inet deyroute_stats`)
// the hub wants for cfg: one entry per enabled tunnel with its listen ports
// (tcp and udp) from the configuration, never from the active candidates,
// so a failover or a warm rung never rebuilds the table (and never resets a
// counter). nat reports whether a tunnel has a kernel-NAT rung on the hub
// (a planned candidate whose hub side has DNAT rules); nil = none. Seed is
// left empty: the caller fills it with the last counter reading.
func StatsSpec(cfg *config.Config, nat func(tunnel string) bool) firewall.StatsSpec {
	var s firewall.StatsSpec
	if cfg == nil {
		return s
	}
	for i := range cfg.Tunnels {
		t := &cfg.Tunnels[i]
		if !t.Enabled || !config.ValidID(t.ID) {
			continue
		}
		st := firewall.StatsTunnel{ID: t.ID, NAT: nat != nil && nat(t.ID)}
		for _, pm := range t.Ports {
			if pm.Listen < 1 || pm.Listen > 65535 {
				continue
			}
			switch pm.Proto {
			case config.ProtoUDP:
				st.UDP = appendUnique(st.UDP, pm.Listen)
			case config.ProtoTCP, "":
				st.TCP = appendUnique(st.TCP, pm.Listen)
			}
		}
		slices.Sort(st.TCP)
		slices.Sort(st.UDP)
		s.Tunnels = append(s.Tunnels, st)
	}
	slices.SortFunc(s.Tunnels, func(a, b firewall.StatsTunnel) int { return strings.Compare(a.ID, b.ID) })
	return s
}

// StatsHash identifies the shape of an accounting table: its tunnels, their
// ports and NAT flags (not the seed). The hub rebuilds the table only when
// the hash changes.
func StatsHash(s firewall.StatsSpec) string {
	tunnels := slices.Clone(s.Tunnels)
	slices.SortFunc(tunnels, func(a, b firewall.StatsTunnel) int { return strings.Compare(a.ID, b.ID) })
	var b strings.Builder
	b.WriteString("stats-v1\n")
	for _, t := range tunnels {
		b.WriteString(t.ID)
		b.WriteString(" tcp=")
		b.WriteString(joinInts(t.TCP))
		b.WriteString(" udp=")
		b.WriteString(joinInts(t.UDP))
		b.WriteString(" nat=")
		b.WriteString(strconv.FormatBool(t.NAT))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

func appendUnique(list []int, v int) []int {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func joinInts(v []int) string {
	c := slices.Clone(v)
	slices.Sort(c)
	c = slices.Compact(c)
	parts := make([]string, len(c))
	for i, n := range c {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

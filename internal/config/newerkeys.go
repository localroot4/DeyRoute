package config

import "slices"

// First releases that know newer config.yaml keys. A release decodes
// config.yaml strictly and refuses a key it does not know (DEY-C001), so
// while a key is set the hub refuses to roll back or downgrade to a release
// older than the key's first version (DEY-S011, see NewerKeysInUse).
const (
	// FirstTrafficVersion is the first release that knows the traffic
	// monitoring and automatic tuning keys. It is the first edge build that
	// has them: every later 0.3.0-edge.N, the 0.3.0 pre-releases after it
	// and 0.3.0 itself know them (a plain "0.3.0" here made every edge build
	// of 0.3.0 look older, so `deyroute update` refused the newest edge
	// build).
	FirstTrafficVersion = "0.3.0-edge.15"
	// FirstAutoUpdateVersion is the first release that knows
	// hub.auto_update: the stable 0.3.0, the first release cut after the
	// key came. The edge builds made between the key and 0.3.0 know it as
	// well, but every 0.3.0-edge.N sorts before 0.3.0, so while the key is
	// set a rollback or downgrade to any edge build of 0.3.0 is refused.
	// That is conservative on purpose: an edge build cannot be told apart
	// by its version, and refusing costs only `deyroute update auto off`
	// first. Updates are not affected: a release that is not older than
	// the running build knows every key that build writes. A failed
	// automatic update still goes back to the build it came from: that
	// build had loaded config.yaml with the key, and the hub records the
	// keys in use when it starts the update.
	FirstAutoUpdateVersion = "0.3.0"
	// FirstAllowUnsignedUpdatesVersion is the first release that knows
	// node.allow_unsigned_updates, for the same reason as
	// FirstAutoUpdateVersion: the stable 0.3.0 is the first release cut
	// after the key came, and every 0.3.0-edge.N sorts before it, so while
	// a node sets the key it refuses a self.update to an older edge build
	// of 0.3.0 too (DEY-S011). An edge build cannot be told apart by its
	// version, and the key is meant for test labs only. A self.update to a
	// version that is not older than the node's own is never refused: it
	// knows every key this build knows.
	FirstAllowUnsignedUpdatesVersion = "0.3.0"
	// FirstFirewallReassertVersion is the first release that knows
	// security.firewall_reassert_s and the event firewall_reapplied in
	// hub.notify.telegram.events (DR-14). Every later edge build is
	// 0.3.1-edge.N, which sorts before 0.3.1, so while either is set a
	// rollback or downgrade to an edge build is refused too: conservative
	// on purpose, as for FirstAutoUpdateVersion (an edge build cannot be
	// told apart by its version).
	FirstFirewallReassertVersion = "0.3.1"
)

// NewerKey is a config.yaml key in use that releases before Since refuse.
type NewerKey struct {
	// Key names the key as the owner finds it in config.yaml.
	Key string
	// Since is the first release that knows the key.
	Since string
}

// String is the key with its first release, as DEY-S011 names it.
func (k NewerKey) String() string { return k.Key + " (since " + k.Since + ")" }

// newerKeys registers every config.yaml key that older releases refuse, in
// the order of the file. To register a new key, add one line here: the key
// as the owner reads it, its First<Item>Version constant above, and
// whether a configuration sets it. Write such a key only when it differs
// from its default (`omitempty`), so a default install never blocks a
// rollback; a key the node reads needs the same check before a node
// installs an older version.
var newerKeys = []struct {
	key   string
	since string
	set   func(c *Config) bool
}{
	{"hub.auto_update", FirstAutoUpdateVersion, func(c *Config) bool { return c.Hub != nil && c.Hub.AutoUpdate }},
	{"nodes[].backend_tier", FirstTrafficVersion, func(c *Config) bool {
		return slices.ContainsFunc(c.Nodes, func(n Node) bool { return n.BackendTier != "" })
	}},
	{"tunnels[].advanced.monthly_quota_gib", FirstTrafficVersion, func(c *Config) bool {
		return slices.ContainsFunc(c.Tunnels, func(t Tunnel) bool { return t.Advanced != nil && t.Advanced.MonthlyQuotaGiB != 0 })
	}},
	{"tuning.sysctl_profile: auto", FirstTrafficVersion, func(c *Config) bool { return c.Tuning != nil && c.Tuning.SysctlProfile == SysctlAuto }},
	{"tuning.nodes_auto", FirstTrafficVersion, func(c *Config) bool { return c.Tuning != nil && c.Tuning.NodesAuto }},
	{"tuning.backend_tier", FirstTrafficVersion, func(c *Config) bool { return c.Tuning != nil && c.Tuning.BackendTier != "" }},
	{"tuning.wg_mtu", FirstTrafficVersion, func(c *Config) bool { return c.Tuning != nil && c.Tuning.WGMTU != 0 }},
	{"monitoring", FirstTrafficVersion, func(c *Config) bool { return c.Monitoring != nil }},
	{"node.allow_unsigned_updates", FirstAllowUnsignedUpdatesVersion, func(c *Config) bool {
		return c.Node != nil && c.Node.AllowUnsignedUpdates
	}},
	// Written only when set: an owner who writes the default (60) has the
	// key in the file, and an older release refuses it all the same.
	{"security.firewall_reassert_s", FirstFirewallReassertVersion, func(c *Config) bool {
		return c.Security != nil && c.Security.FirewallReassertS != nil
	}},
	{"hub.notify.telegram.events: firewall_reapplied", FirstFirewallReassertVersion, func(c *Config) bool {
		return c.Hub != nil && slices.Contains(c.Hub.Notify.Telegram.Events, EventFirewallReapplied)
	}},
}

// NewerKeysInUse lists the config.yaml keys set in c that older releases
// do not know, each with the first release that knows it, in the order of
// the file. Releases before a key's Since refuse config.yaml while the key
// is set, so a rollback or downgrade below it is refused (DEY-S011).
func (c *Config) NewerKeysInUse() []NewerKey {
	if c == nil {
		return nil
	}
	var keys []NewerKey
	for _, k := range newerKeys {
		if k.set(c) {
			keys = append(keys, NewerKey{Key: k.key, Since: k.since})
		}
	}
	return keys
}

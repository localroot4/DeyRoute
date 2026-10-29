package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	"github.com/localroot4/deyroute/internal/exec"
)

// realSetupHub runs the real setup.SetupHub below the test root (no
// firewall, no kernel tuning, no IPv6 detection, every port free, the
// backend user present) and records the options the CLI passed.
func (e *env) realSetupHub(got *setup.HubOptions) {
	var o setup.HubOptions
	e.fakeSetup(&o) // the join command stub
	e.g.Ops.SetupHub = func(ctx context.Context, o setup.HubOptions) (*setup.HubResult, error) {
		*got = o
		o.NoFirewall, o.NoIPv6, o.ApplySysctl = true, true, false
		o.CheckPort = func(context.Context, int) error { return nil }
		o.LookupUser = func(string) (int, error) { return 1000, nil }
		o.LookupGroup = func(string) (int, error) { return 1000, nil }
		o.Chown = func(string, int, int) error { return nil }
		return setup.SetupHub(ctx, o)
	}
}

// loadHubConfig reads the config.yaml setup wrote below the test root.
func (e *env) loadHubConfig() *config.Config {
	e.t.Helper()
	cfg, err := config.Load(filepath.Join(e.root, config.DefaultPath))
	require.NoError(e.t, err)
	return cfg
}

// TestSetupHubPublicIPFlag: setup --public-ip stores exactly that address
// (canonical form) without detecting or asking; the JSON document names the
// source of public_ip: flag, detected (non-interactive) or wizard (typed).
func TestSetupHubPublicIPFlag(t *testing.T) {
	e := newEnv(t)
	var o setup.HubOptions
	e.realSetupHub(&o)
	detected := 0
	e.g.DetectIP = func(context.Context) (string, bool, error) {
		detected++
		return "10.0.0.5", true, nil
	}
	doc := e.json("setup", "--role", "hub", "--name", "ir-1", "--public-ip", "203.0.113.7", "--yes")
	require.Equal(t, "203.0.113.7", o.PublicIP)
	require.Equal(t, "203.0.113.7", doc["public_ip"])
	require.Equal(t, "flag", doc["public_ip_source"])
	require.Equal(t, false, doc["private_ip"])
	require.Equal(t, "203.0.113.7", e.loadHubConfig().Hub.PublicIP)
	require.Zero(t, detected, "an address given with --public-ip is not detected")

	// A 4-in-6 form is stored as plain IPv4.
	e = newEnv(t)
	e.realSetupHub(&o)
	e.json("setup", "--role", "hub", "--name", "ir-1", "--public-ip", "::ffff:203.0.113.8", "--yes")
	require.Equal(t, "203.0.113.8", e.loadHubConfig().Hub.PublicIP)

	// A TTY with --public-ip asks no public IP question.
	e = newEnv(t)
	var fo setup.HubOptions
	e.fakeSetup(&fo)
	e.tuneFixture()
	e.tty("", "n") // control port, tuning
	out := e.ok("setup", "--role", "hub", "--name", "ir-1", "--public-ip", "203.0.113.7")
	require.NotContains(t, out, "Public IP of this server")
	require.Contains(t, out, "Step 1 of 2 · Control port for the nodes")
	require.Equal(t, "203.0.113.7", fo.PublicIP)

	// Without the flag: detected, or typed in the wizard.
	doc = e.json("setup", "--role", "hub", "--name", "ir-1", "--yes")
	require.Equal(t, "detected", doc["public_ip_source"])
	e.tty("5.6.7.9", "", "n")
	out = e.ok("setup", "--role", "hub", "--name", "ir-1", "--json")
	require.Contains(t, out, `"public_ip_source": "wizard"`)
	e.g.IsTTY = false

	// The JSON document of a hub setup.
	e = newEnv(t)
	e.fakeSetup(&fo)
	statsGolden(t, "setup_hub.json.golden",
		e.ok("setup", "--role", "hub", "--name", "ir-1", "--public-ip", "203.0.113.7", "--public-ip6", "2a01:4f8::7", "--yes", "--json"))
}

// TestSetupHubPublicIPFlagRejectsPrivate: a private, CGNAT, loopback or
// link-local --public-ip is refused (DEY-C013, field --public-ip) before
// any question or setup step; --allow-private-ip (the lab) accepts private,
// CGNAT and loopback addresses but never a link-local one.
func TestSetupHubPublicIPFlagRejectsPrivate(t *testing.T) {
	e := newEnv(t)
	var o setup.HubOptions
	e.fakeSetup(&o)
	called := 0
	inner := e.g.Ops.SetupHub
	e.g.Ops.SetupHub = func(ctx context.Context, ho setup.HubOptions) (*setup.HubResult, error) {
		called++
		return inner(ctx, ho)
	}
	for _, ip := range []string{"10.0.0.5", "192.168.1.2", "100.64.1.1", "127.0.0.1", "169.254.1.1", "0.0.0.0", "224.0.0.1"} {
		errOut := e.fail(1, "setup", "--role", "hub", "--name", "ir-1", "--public-ip", ip, "--yes")
		require.Contains(t, errOut, "DEY-C013", ip)
		require.Contains(t, errOut, "--public-ip", ip)
		require.Contains(t, errOut, "--allow-private-ip", "the fix names the lab flag")
	}
	// Refused before the wizard asks anything.
	e.tty("hub", "x")
	errOut := e.fail(1, "setup", "--public-ip", "10.0.0.5")
	require.Contains(t, errOut, "DEY-C013")
	require.NotContains(t, e.out.String(), "Role of this server")
	e.g.IsTTY = false
	require.Zero(t, called, "nothing is set up")

	for _, ip := range []string{"10.0.0.5", "100.64.1.1", "127.0.0.1"} {
		doc := e.json("setup", "--role", "hub", "--name", "ir-1", "--public-ip", ip, "--allow-private-ip", "--yes")
		require.Equal(t, ip, o.PublicIP)
		require.Equal(t, "flag", doc["public_ip_source"])
	}
	require.Contains(t, e.fail(1, "setup", "--role", "hub", "--public-ip", "169.254.1.1", "--allow-private-ip", "--yes"), "DEY-C013")
	require.Contains(t, e.fail(1, "setup", "--role", "hub", "--public-ip6", "fe80::1", "--allow-private-ip", "--yes"), "DEY-C013")
	require.Contains(t, e.fail(1, "setup", "--role", "hub", "--public-ip6", "fd00::1", "--yes"), "DEY-C013")
	e.ok("setup", "--role", "hub", "--name", "ir-1", "--public-ip6", "fd00::1", "--allow-private-ip", "--yes")
	require.Equal(t, "fd00::1", o.PublicIP6)

	// --allow-private-ip alone, or any of the address flags for a node.
	require.Contains(t, e.fail(1, "setup", "--role", "hub", "--allow-private-ip", "--yes"), "--allow-private-ip")
	for _, args := range [][]string{
		{"setup", "--role", "node", "--public-ip", "203.0.113.7"},
		{"setup", "--role", "node", "--public-ip6", "2a01:4f8::7"},
		{"setup", "--role", "node", "--public-ip", "10.0.0.5", "--allow-private-ip"},
		{"join", "dey://T@5.6.7.8:44433#sha256:ab", "--public-ip", "203.0.113.7"},
		{"join", "dey://T@5.6.7.8:44433#sha256:ab", "--public-ip6", "2a01:4f8::7"},
		{"join", "dey://T@5.6.7.8:44433#sha256:ab", "--allow-private-ip"},
	} {
		errOut := e.fail(1, args...)
		require.Contains(t, errOut, "DEY-C013", args)
		require.Contains(t, errOut, "--role hub", args)
	}
	require.Equal(t, 4, called)
}

// TestSetupHubPublicIP6Flag: --public-ip6 stores that IPv6 address
// (canonical, lower case, zeros compressed) next to the IPv4 one.
func TestSetupHubPublicIP6Flag(t *testing.T) {
	e := newEnv(t)
	var o setup.HubOptions
	e.realSetupHub(&o)
	doc := e.json("setup", "--role", "hub", "--name", "ir-1", "--public-ip", "203.0.113.7", "--public-ip6", "2A01:04F8:0000::0007", "--yes")
	require.Equal(t, "2a01:4f8::7", o.PublicIP6)
	require.Equal(t, "2a01:4f8::7", doc["public_ip6"])
	cfg := e.loadHubConfig()
	require.Equal(t, "203.0.113.7", cfg.Hub.PublicIP)
	require.Equal(t, "2a01:4f8::7", cfg.Hub.PublicIP6)

	// Only --public-ip6: the IPv4 address is still detected.
	e = newEnv(t)
	e.realSetupHub(&o)
	e.fake.On("ip -4 route get 1.1.1.1", exec.OK("1.1.1.1 via 5.6.7.1 dev eth0 src 5.6.7.8 uid 0\n"))
	doc = e.json("setup", "--role", "hub", "--name", "ir-1", "--public-ip6", "2a01:4f8::8", "--yes")
	require.Empty(t, o.PublicIP)
	require.Equal(t, "5.6.7.8", doc["public_ip"])
	require.Equal(t, "detected", doc["public_ip_source"])
	cfg = e.loadHubConfig()
	require.Equal(t, "2a01:4f8::8", cfg.Hub.PublicIP6)
}

// TestSetupHubPublicIPFlagRejectsV6InV4: --public-ip takes only IPv4 and
// --public-ip6 only IPv6 (a 4-in-6 address is IPv4); a zone or a value that
// is no address at all is refused too (DEY-C013 with the field).
func TestSetupHubPublicIPFlagRejectsV6InV4(t *testing.T) {
	e := newEnv(t)
	var o setup.HubOptions
	e.fakeSetup(&o)
	for _, c := range []struct{ flag, value string }{
		{"--public-ip", "2a01:4f8::7"},
		{"--public-ip", "203.0.113.7/24"},
		{"--public-ip", "example.com"},
		{"--public-ip6", "203.0.113.7"},
		{"--public-ip6", "::ffff:203.0.113.7"},
		{"--public-ip6", "2a01:4f8::7%eth0"},
	} {
		errOut := e.fail(1, "setup", "--role", "hub", "--name", "ir-1", c.flag, c.value, "--yes")
		require.Contains(t, errOut, "DEY-C013", c)
		require.Contains(t, errOut, "Invalid value for "+c.flag+":", c)
	}
}

// TestSetupHelpNamesAutoProfile: the help of setup --yes names the profile
// it applies (auto, see setupHub), not balanced.
func TestSetupHelpNamesAutoProfile(t *testing.T) {
	e := newEnv(t)
	out := e.ok("setup", "--help")
	require.Contains(t, out, "apply the automatic kernel profile")
	require.NotContains(t, out, "balanced")
	var o setup.HubOptions
	e.fakeSetup(&o)
	e.ok("setup", "--role", "hub", "--name", "ir-1", "--yes")
	require.Equal(t, config.SysctlAuto, o.SysctlProfile)
}

// TestJoinHelpNamesBalancedProfile: the (hidden) join --yes keeps its own
// help text, which names the balanced profile a join applies.
func TestJoinHelpNamesBalancedProfile(t *testing.T) {
	e := newEnv(t)
	yes := newJoinCmd(e.g).Flags().Lookup("yes")
	require.NotNil(t, yes)
	require.Contains(t, yes.Usage, "balanced kernel profile")
	require.NotEqual(t, newSetupCmd(e.g).Flags().Lookup("yes").Usage, yes.Usage)
}

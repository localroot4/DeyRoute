package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// TestFirewallReassertKey: security.firewall_reassert_s is absent by
// default (60 s, so a default install never blocks a rollback), 0 turns
// the re-assert off, it survives save, load and Clone, is checked against
// its range, registered in the rollback guard with its own first version,
// and firewall_managed: false turns the re-assert off whatever it says.
func TestFirewallReassertKey(t *testing.T) {
	dir := t.TempDir()
	c := validHub()
	require.Equal(t, 60*time.Second, c.FirewallReassertInterval())
	path := filepath.Join(dir, "default.yaml")
	require.NoError(t, SaveWith(path, c, fakeOpts()))
	raw, err := os.ReadFile(path) // #nosec G304 -- test file
	require.NoError(t, err)
	require.NotContains(t, string(raw), "firewall_reassert_s")
	require.Empty(t, c.NewerKeysInUse())

	off := 0
	c.Security.FirewallReassertS = &off
	require.Zero(t, c.FirewallReassertInterval())
	path = filepath.Join(dir, "off.yaml")
	require.NoError(t, SaveWith(path, c, fakeOpts()))
	raw, err = os.ReadFile(path) // #nosec G304 -- test file
	require.NoError(t, err)
	require.Contains(t, string(raw), "firewall_reassert_s: 0")
	back, err := LoadWith(path, fakeOpts())
	require.NoError(t, err)
	require.NotNil(t, back.Security.FirewallReassertS)
	require.Zero(t, back.FirewallReassertInterval())
	cl := Clone(back)
	*cl.Security.FirewallReassertS = 30
	require.Zero(t, *back.Security.FirewallReassertS, "Clone copies the value")

	require.Equal(t, "0.3.1", FirstFirewallReassertVersion)
	require.Equal(t, []NewerKey{{"security.firewall_reassert_s", FirstFirewallReassertVersion}}, back.NewerKeysInUse())

	every := 30
	c.Security.FirewallReassertS = &every
	require.Equal(t, 30*time.Second, c.FirewallReassertInterval())
	c.Security.FirewallManaged = false
	require.Zero(t, c.FirewallReassertInterval(), "an unmanaged firewall is never re-asserted")

	for _, bad := range []int{-1, 5, 3601} {
		c := validHub()
		c.Security.FirewallReassertS = &bad
		err := c.Validate(fakeOpts())
		require.Error(t, err, bad)
		require.Equal(t, deyerr.C013, deyerr.As(err).Code)
		require.Contains(t, deyerr.As(err).Message(), "security.firewall_reassert_s")
	}

	// A node reads the key from its own config.yaml.
	n := NewNode("de-1", "1.2.3.4:44433", "sha256:"+strings.Repeat("ab", 32))
	require.Equal(t, 60*time.Second, n.FirewallReassertInterval())
	n.Security = &Security{FirewallManaged: true, FirewallReassertS: &every}
	require.NoError(t, n.Validate(fakeOpts()))
	require.Equal(t, []NewerKey{{"security.firewall_reassert_s", FirstFirewallReassertVersion}}, n.NewerKeysInUse())
	var nilCfg *Config
	require.Equal(t, 60*time.Second, nilCfg.FirewallReassertInterval())
}

// TestFirewallReappliedEventNeedsNewerRelease: a release before
// FirstFirewallReassertVersion refuses hub.notify.telegram.events naming
// firewall_reapplied, so the rollback guard counts it like a key.
func TestFirewallReappliedEventNeedsNewerRelease(t *testing.T) {
	c := validHub()
	c.Hub.Notify.Telegram.Events = []string{"down", "firewall_reapplied"}
	require.NoError(t, c.Validate(fakeOpts()))
	require.Equal(t, []NewerKey{{"hub.notify.telegram.events: firewall_reapplied", FirstFirewallReassertVersion}},
		c.NewerKeysInUse())
}

// TestValidateRatePerSource: security.unknown_control_rate_per_source and
// security.unknown_control_rate take an nft rate ("3/minute", optionally
// "… burst N packets"), are absent by default (3/minute per source, 30/minute
// in all), survive save and load, and each is registered in the rollback
// guard with its own first version (DR-15).
func TestValidateRatePerSource(t *testing.T) {
	c := validHub()
	ps, global := c.UnknownControlRates()
	require.Equal(t, "3/minute", ps)
	require.Equal(t, "30/minute", global)
	require.Equal(t, DefaultUnknownControlRatePerSource, ps)
	require.Equal(t, DefaultUnknownControlRate, global)

	for _, good := range []string{"3/minute", "10/second burst 20 packets", "1/hour", "100/day"} {
		c := validHub()
		c.Security.UnknownControlRatePerSource = good
		c.Security.UnknownControlRate = good
		require.NoError(t, c.Validate(fakeOpts()), good)
	}
	for _, bad := range []string{"3 per minute", "0/minute", "3/minute; drop", "3/minute burst 3", "fast", "-3/minute"} {
		for _, field := range []string{"security.unknown_control_rate_per_source", "security.unknown_control_rate"} {
			c := validHub()
			if field == "security.unknown_control_rate" {
				c.Security.UnknownControlRate = bad
			} else {
				c.Security.UnknownControlRatePerSource = bad
			}
			err := c.Validate(fakeOpts())
			require.Error(t, err, bad)
			require.Equal(t, deyerr.C013, deyerr.As(err).Code)
			require.Contains(t, deyerr.As(err).Message(), field)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "default.yaml")
	require.NoError(t, SaveWith(path, c, fakeOpts()))
	raw, err := os.ReadFile(path) // #nosec G304 -- test file
	require.NoError(t, err)
	require.NotContains(t, string(raw), "unknown_control_rate")

	c.Security.UnknownControlRatePerSource = "5/minute"
	c.Security.UnknownControlRate = "60/minute burst 10 packets"
	path = filepath.Join(dir, "set.yaml")
	require.NoError(t, SaveWith(path, c, fakeOpts()))
	back, err := LoadWith(path, fakeOpts())
	require.NoError(t, err)
	ps, global = back.UnknownControlRates()
	require.Equal(t, "5/minute", ps)
	require.Equal(t, "60/minute burst 10 packets", global)
	require.Equal(t, "0.3.1", FirstUnknownControlRatePerSourceVersion)
	require.Equal(t, "0.3.1", FirstUnknownControlRateVersion)
	require.Equal(t, []NewerKey{
		{"security.unknown_control_rate_per_source", FirstUnknownControlRatePerSourceVersion},
		{"security.unknown_control_rate", FirstUnknownControlRateVersion},
	}, back.NewerKeysInUse())
	var nilCfg *Config
	ps, global = nilCfg.UnknownControlRates()
	require.Equal(t, "3/minute", ps)
	require.Equal(t, "30/minute", global)
}

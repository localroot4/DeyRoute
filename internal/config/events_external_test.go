package config_test

import (
	"sort"
	"testing"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/notify"
	"github.com/stretchr/testify/require"
)

// TestTelegramEventsMatchNotify keeps the values config accepts in
// hub.notify.telegram.events identical to what internal/notify can resolve,
// so a config that validates never fails at notifier start (and vice versa).
func TestTelegramEventsMatchNotify(t *testing.T) {
	set := map[string]bool{}
	for _, e := range append(append([]string{}, config.TelegramEventAliases...), config.TelegramEventNames...) {
		set[e] = true
	}
	got := make([]string, 0, len(set))
	for e := range set {
		got = append(got, e)
	}
	sort.Strings(got)
	require.Equal(t, notify.Valid(), got)

	aliases := make([]string, 0, len(notify.Aliases()))
	for a := range notify.Aliases() {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	want := append([]string{}, config.TelegramEventAliases...)
	sort.Strings(want)
	require.Equal(t, aliases, want)
	require.Equal(t, notify.DefaultEvents, config.DefaultTelegramEvents)
}

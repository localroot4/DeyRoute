package notify

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/localroot4/deyroute/internal/state"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		name string
		hub  string
		e    state.Event
		want string
	}{
		{
			name: "switch_transport",
			hub:  "ir-1",
			e:    switchEvent("main"),
			want: "DEYROUTE ir-1 · main · switch_transport\nbackhaul/tcpmux -> backhaul/wssmux\nreason: probe failed 3x (timeout)\n2026-09-29 12:41:03 UTC",
		},
		{
			name: "switch_node shows the nodes",
			hub:  "ir-1",
			e: state.Event{At: at, Type: state.EvSwitchNode, Tunnel: "main", FromNode: "de-1", ToNode: "nl-1",
				FromTransport: "backhaul/wssmux", ToTransport: "backhaul/wssmux", Reason: "node de-1 offline", Code: "DEY-N003"},
			want: "DEYROUTE ir-1 · main · switch_node\nde-1 backhaul/wssmux -> nl-1 backhaul/wssmux\nreason: node de-1 offline\ncode: DEY-N003\n2026-09-29 12:41:03 UTC",
		},
		{
			name: "node event without tunnel",
			e:    state.Event{At: at, Type: state.EvNodeOffline, Node: "de-1", Reason: "no heartbeat for 30s", Message: "Node de-1 is offline"},
			want: "DEYROUTE · de-1 · node_offline\nreason: no heartbeat for 30s\n2026-09-29 12:41:03 UTC",
		},
		{
			name: "tunnel event with node and message only",
			hub:  "ir-1",
			e:    state.Event{At: at, Type: state.EvServiceDown, Tunnel: "main", Node: "de-1", Message: "service on 127.0.0.1:443 is down"},
			want: "DEYROUTE ir-1 · main · service_down\nnode: de-1\nservice on 127.0.0.1:443 is down\n2026-09-29 12:41:03 UTC",
		},
		{
			name: "only the new side",
			e:    state.Event{At: at, Type: state.EvTunnelUp, Tunnel: "main", ToTransport: "rathole/noise"},
			want: "DEYROUTE · main · tunnel_up\nrathole/noise\n2026-09-29 12:41:03 UTC",
		},
		{
			name: "only the old side",
			e:    state.Event{At: at, Type: state.EvTunnelDown, Tunnel: "main", FromTransport: "rathole/noise", Code: "DEY-F001"},
			want: "DEYROUTE · main · tunnel_down\nrathole/noise\ncode: DEY-F001\n2026-09-29 12:41:03 UTC",
		},
		{
			name: "update without tunnel",
			e:    state.Event{At: at.In(time.FixedZone("IRST", 12600)), Type: state.EvUpdateApplied, Message: "deyroute 1.1.0 -> 1.2.0"},
			want: "DEYROUTE · update_applied\ndeyroute 1.1.0 -> 1.2.0\n2026-09-29 12:41:03 UTC",
		},
		{
			name: "fields are single lines",
			e:    state.Event{At: at, Type: state.EvProbeError, Tunnel: "ma\nin", Reason: "line1\r\nline2\tx"},
			want: "DEYROUTE · ma in · probe_error\nreason: line1 line2 x\n2026-09-29 12:41:03 UTC",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.hub == "" {
				assert.Equal(t, c.want, Format(c.e))
			}
			assert.Equal(t, c.want, FormatWithHub(c.hub, c.e))
		})
	}
}

func TestFormatSuppressedAndSecrets(t *testing.T) {
	s := format("ir-1", switchEvent("main"), 3)
	assert.True(t, strings.HasPrefix(s, "DEYROUTE ir-1 · main · switch_transport (+3 suppressed)\n"), s)

	e := state.Event{At: at, Type: state.EvProbeError, Tunnel: "main", Reason: "backend said token=abcdef0123456789 and bot " + testToken}
	s = Format(e)
	assert.NotContains(t, s, "abcdef0123456789")
	assert.NotContains(t, s, testToken)
	assert.Contains(t, s, "***")
}

func TestFormatTruncatesLongFields(t *testing.T) {
	e := state.Event{At: at, Type: state.EvProbeError, Tunnel: "main", Reason: strings.Repeat("é", 1000)}
	s := Format(e)
	for _, line := range strings.Split(s, "\n") {
		assert.LessOrEqual(t, utf8.RuneCountInString(line), maxField+len("reason: "))
	}
	assert.Contains(t, s, "…")
	assert.True(t, utf8.ValidString(s))
}

func TestFormatZeroTime(t *testing.T) {
	s := Format(state.Event{Type: state.EvTunnelUp, Tunnel: "main"})
	lines := strings.Split(s, "\n")
	ts, err := time.Parse("2006-01-02 15:04:05 UTC", lines[len(lines)-1])
	assert.NoError(t, err)
	assert.WithinDuration(t, time.Now().UTC(), ts, time.Minute)
}

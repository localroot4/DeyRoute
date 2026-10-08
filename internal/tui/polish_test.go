package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/api/apitest"
	"github.com/localroot4/deyroute/internal/i18n"
	"github.com/localroot4/deyroute/internal/state"
)

// Every event type has a readable word in LAST EVENTS (no snake_case ids).
func TestEventWordsAreReadable(t *testing.T) {
	for _, ev := range []string{
		state.EvTunnelUp, state.EvTunnelDegraded, state.EvTunnelDown, state.EvSwitchTransport, state.EvSwitchNode,
		state.EvFailback, state.EvFailbackFailed, state.EvFlapping, state.EvNodeOnline, state.EvNodeOffline,
		state.EvServiceDown, state.EvBackendCrash, state.EvProbeError, state.EvUpdateApplied, state.EvUpdateRolledBack,
		state.EvBackendRolledBack, state.EvNodeIPChanged, state.EvACMEFailed, state.EvRungSkipped, state.EvRungRestored,
		state.EvConfigApplied, eventUpdateAvailable,
	} {
		w := EventWord(ev)
		require.NotContains(t, w, "_", ev)
		require.LessOrEqual(t, width(w), eventTypeCap, ev)
	}
	require.Equal(t, "node offline", EventWord(state.EvNodeOffline))
	require.Equal(t, "rolled back", EventWord(state.EvBackendRolledBack))
	require.Equal(t, "some new event", EventWord("some_new_event"), "an unknown type still reads as words")
	// The hub's default message (the type itself) gives way to the reason.
	require.Equal(t, "no heartbeat", EventMessage(state.Event{Type: "node_offline", Message: "node_offline", Reason: "no heartbeat"}))
}

// A long event type or tunnel id is cut at its column cap: at 80 columns
// the message of the switch line stays readable (section 6 sample).
func TestLastEventsLongIDKeepsLayout(t *testing.T) {
	a := dashApp(Caps{Unicode: true, Width: 80})
	evs := []state.Event{
		{At: testNow, Type: state.EvSwitchTransport, Tunnel: "main", Message: "backhaul/tcpmux → backhaul/wssmux (failback)"},
		{At: testNow, Type: state.EvBackendRolledBack, Tunnel: "main", Level: state.LevelWarn, Message: "backhaul rolled back"},
		{At: testNow, Type: "a_brand_new_event_type_of_a_later_release", Tunnel: "a-very-long-tunnel-id-of-32-chars", Message: "hello"},
	}
	out := renderEvents(a, evs)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)
	require.Equal(t, "  12:45:00  main           switch         backhaul/tcpmux → backhaul/wssmux…", lines[0])
	require.Contains(t, lines[1], "  main           rolled back    backhaul rolled back")
	require.Contains(t, lines[2], "  a-very-long…   a brand new…   hello")
	for _, l := range lines {
		require.LessOrEqual(t, width(l), 80, l)
	}
}

func TestKVTableAlignsOnWidestLabel(t *testing.T) {
	var kt kvTable
	kt.add("Public IP", "1.2.3.4")
	kt.add("Control channel", "online")
	require.Equal(t, "  Public IP        1.2.3.4\n  Control channel  online\n", kt.String())
	require.Empty(t, (&kvTable{}).String())
}

func TestTitlesAndSizes(t *testing.T) {
	require.Equal(t, "Thresholds", itemName(i18n.TUIFoThresholds), "the Advanced star stays in the lists only")
	require.Equal(t, "Failover - Thresholds", subTitle(i18n.MenuFailover, i18n.TUIFoThresholds))
	require.Equal(t, "Thresholds: main", titleOf(i18n.TUIFoThresholds, "main"))
	require.Equal(t, "3.8 GiB", sizeText(4102328320))
	require.Equal(t, "1.5 MiB", sizeText(3<<19))
	require.Equal(t, "1 KiB", sizeText(10))
	require.Equal(t, "15 minutes", minutesText(14*time.Minute+30*time.Second))
	require.Equal(t, "1 minute", minutesText(20*time.Second))
}

// Titles never show the " *" of Advanced items; the language is named.
func TestTitlesWithoutStar(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	stub.SettingsSetFn = func(context.Context, api.SettingsRequest) error { return nil }
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("5")
	h.must("7) Thresholds *")
	h.choose("7")
	h.must(" Failover - Thresholds\n")
	h.choose("1")
	h.must(" Thresholds: main\n")
	h.mustNot("Thresholds *:")
	h.press("esc", "esc", "esc")
	h.choose("7").choose("6")
	h.must(" Settings in effect\n")
	h.press("esc", "esc")
	h.choose("12").choose("2").choose("1")
	h.must("Language: English")
}

// The join command's expiry reads in minutes; an expired one says so.
func TestJoinExpiryText(t *testing.T) {
	a := dashApp(Caps{Unicode: true})
	j := api.JoinCommand{Command: "x", ExpiresAt: testNow.Add(30 * time.Second)}
	require.Contains(t, joinText(a, j), "Single use; expires at 12:45:30 (in 1 minute).")
	j.ExpiresAt = testNow.Add(-time.Minute)
	require.Contains(t, joinText(a, j), "Expired at 12:44:00. Press r for a new command.")
}

// "?" on an empty text input shows the help (forms, typed confirmations,
// the wizard's Ports question); in a typed answer it is a character. The
// footer of a text field only names the keys that work there.
func TestHelpOnTextInputs(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	stub.TunnelDeleteFn = func(context.Context, string, func(api.Step)) error { return nil }
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("3").choose("3").choose("1") // Nodes > Rename de-1: a form
	h.must("New name for de-1 [Germany 1]: _", i18n.T(i18n.FooterTyping))
	h.mustNot("r refresh")
	h.press("?")
	h.must(" Help\n", "Enter on an empty line keeps the value in brackets.")
	h.press("esc")
	h.must("New name for de-1 [Germany 1]: _")
	h.press("a", "?")
	h.must("New name for de-1 [Germany 1]: a?_")
	h.press("esc", "esc", "esc")
	// typed confirmation
	h.choose("2").choose("6").choose("1")
	h.must("Type yes to continue: _")
	h.press("?")
	h.must("Destructive actions need the word yes typed exactly")
	h.press("esc", "esc", "esc")
	// the wizard's Ports question
	h.choose("1").choose("1")
	h.must("Ports: _")
	h.press("?")
	h.must("Add tunnel asks at most three questions")
	h.press("enter")
	h.must("Ports: _")
	// menus keep the full footer
	h.press("esc", "esc")
	h.must(i18n.T(i18n.FooterKeys))
}

func TestChoiceFieldAnswers(t *testing.T) {
	fl := field{def: "b", opts: []fieldOpt{{label: "none"}, {value: "a", label: "A"}, {value: "b", label: "B"}}}
	for in, want := range map[string]string{"2": "a", "3": "b", "1": "", "B": "b"} {
		v, ok := fl.option(in)
		require.True(t, ok, in)
		require.Equal(t, want, v, in)
	}
	for _, in := range []string{"0", "4", "c"} {
		_, ok := fl.option(in)
		require.False(t, ok, in)
	}
	require.Equal(t, 3, fl.defNumber())
	require.Equal(t, "none", fl.shown(""))
	require.Equal(t, "a", fl.shown("a"))
}

// A form's hint follows the intro after one blank line, not two.
func TestFormHintSpacing(t *testing.T) {
	h := newHarness(t, Options{Caps: Caps{Unicode: true}})
	h.m.(Model).a.push(newForm("T", "Intro.", []field{{key: "a", label: "A", hint: "Hint."}, {key: "b", label: "B", hint: "Hint B."}},
		func(a *app, _ map[string]string) tea.Cmd { return a.pop() }))
	h.must(" Intro.\n\n Hint.\n A: _")
	h.typeLine("x")
	h.must(" Intro.\n\n A: x\n\n Hint B.\n B: _")
}

// Edit tunnel: switching to custom TLS needs the files; with custom TLS in
// place Enter keeps them.
func TestEditTunnelCustomTLS(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "advanced")
	var mu sync.Mutex
	var got api.TunnelEditRequest
	stub.TunnelEditFn = func(_ context.Context, _ string, r api.TunnelEditRequest, _ func(api.Step)) (api.TunnelInfo, error) {
		mu.Lock()
		got = r
		mu.Unlock()
		return api.TunnelInfo{}, nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("2").choose("2").choose("1")
	h.typeLine("").typeLine("").typeLine("") // name, policy, ladder
	h.typeLine("3")                          // custom
	h.typeLine("")
	h.must("A value is required.")
	h.typeLine("/root/c.pem").typeLine("/root/k.pem").typeLine("")
	h.must("Tunnel main updated.")
	mu.Lock()
	require.Equal(t, "custom", *got.TLSMode)
	require.Equal(t, "/root/c.pem", *got.TLSCert)
	require.Nil(t, got.Policy)
	mu.Unlock()

	// Custom already: Enter on every question changes nothing.
	show := stub.TunnelShowFn
	stub.TunnelShowFn = func(ctx context.Context, id string) (api.TunnelDetail, error) {
		d, err := show(ctx, id)
		d.TLSMode = "custom"
		return d, err
	}
	h.press("esc", "esc")
	h.choose("2").choose("1")
	h.typeLine("").typeLine("").typeLine("") // name, policy, ladder
	h.must("3) custom - your own certificate and key files (current)")
	for i := 0; i < 4; i++ { // TLS mode, certificate, key, probe port
		h.typeLine("")
	}
	h.must("Nothing changed.")
}

// Without a warm rung on the backup node the wizard says so instead of
// "ready (warm)".
func TestTunnelUpBackupNotWarm(t *testing.T) {
	a := dashApp(Caps{Unicode: true})
	out := renderTunnelUp(a, created{t: api.TunnelInfo{ID: "web", State: stUp, ActiveTransport: "x", RTTms: 3}, backup: "nl-1", total: 2})
	require.Contains(t, out, "backup nl-1: no rung is warm yet")
	require.Contains(t, out, "Backup only works if the same service runs on both nodes.")
	require.NotContains(t, out, "ready (warm)")
	plain := renderTunnelUp(a, created{t: api.TunnelInfo{ID: "web", State: stUp}})
	require.NotContains(t, plain, "backup")
}

// Logs: deyroute's JSON lines are formatted, backend lines stay as they
// are, and a tunnel log names its side ([hub] / [node]).
func TestLogsFormatted(t *testing.T) {
	stub := &apitest.Stub{
		StatusFn: func(context.Context) (api.Status, error) {
			return api.Status{Role: "hub", Hub: &api.HubStatus{UIMode: "simple"}}, nil
		},
		TunnelListFn: func(context.Context) ([]api.TunnelInfo, error) { return sampleTunnels(), nil },
		LogsFn: func(ctx context.Context, q api.LogQuery, emit func(api.LogLine) error) error {
			lines := []api.LogLine{
				{Source: "hub", Line: `{"ts":"2026-09-30T12:41:03.123Z","level":"warn","component":"failover","msg":"probe failed","tunnel":"main","attempt":3,"err":"dial tcp: timeout","code":"DEY-F001"}`},
				{Source: "node", Line: "2026/09/30 12:41:04 [INFO] client connected"},
			}
			if q.Target == "hub" {
				lines = lines[:1]
			}
			for _, l := range lines {
				if err := emit(l); err != nil {
					return err
				}
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("6").choose("4").choose("2") // tunnel main
	h.must(` [hub]  12:41:03 WARN  failover probe failed tunnel=main code=DEY-F001 attempt=3 err="dial tcp: timeout"`,
		" [node] 2026/09/30 12:41:04 [INFO] client connected")
	h.mustNot(`{"ts"`)
	h.press("esc")
	h.choose("1") // the hub log: one source, no prefix
	h.must("\n 12:41:03 WARN  failover probe failed")
	h.mustNot("[hub]")
}

// Notifications shows the Telegram settings and offers them as defaults.
func TestNotificationsContext(t *testing.T) {
	log := &callLog{}
	stub := fullStub(log, "simple")
	st := sampleStatus()
	st.Hub.Telegram = api.TelegramStatus{Enabled: true, ChatID: "-100123", TokenFile: "/etc/deyroute/secrets/telegram.token", Events: []string{"down", "switch"}}
	stub.StatusFn = func(context.Context) (api.Status, error) { return st, nil }
	stub.NotifyTelegramSetFn = func(_ context.Context, file, chat string, events []string) error {
		log.add("tg " + file + " " + chat + " " + strings.Join(events, "|"))
		return nil
	}
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Local: stub})
	h.choose("9")
	h.must("\n  Telegram  on\n  Chat id   -100123\n  Events    down, switch\n")
	h.choose("1")
	h.must("Bot token file [/etc/deyroute/secrets/telegram.token]: _")
	h.typeLine("")
	h.must("Chat id [-100123]: _")
	h.typeLine("")
	h.must("Events (comma separated) [down,switch]: _", "Names: down, up, degraded")
	h.typeLine("")
	require.True(t, log.has("tg /etc/deyroute/secrets/telegram.token -100123 down|switch"))
}

// Restore lists the backups of this server by number; an unencrypted one
// needs no passphrase; "Another file" asks for the path.
func TestRestoreListsBackups(t *testing.T) {
	var mu sync.Mutex
	var checked []string
	o := Options{
		Caps: Caps{Unicode: true},
		Backups: func(context.Context) ([]BackupFile, error) {
			return []BackupFile{
				{Path: "/var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age", Size: 12 << 10, ModTime: testNow.Add(-time.Hour)},
				{Path: "/var/lib/deyroute/backups/auto/deyroute-backup-20260929T080000.000000000Z.tar.gz", Size: 3 << 20, ModTime: testNow.Add(-28 * time.Hour), Auto: true},
			}, nil
		},
		RestoreCheck: func(_ context.Context, path, pass string) (RestorePlan, error) {
			mu.Lock()
			checked = append(checked, path+"|"+pass)
			mu.Unlock()
			return RestorePlan{Lost: "Restoring " + path}, nil
		},
		Restore: func(context.Context, string, string, string) (string, error) { return "", nil },
	}
	h := newHarness(t, o)
	h.choose("10").choose("2")
	h.must("Choose the backup to restore (newest first):",
		" 1) 2026-09-30 11:45  deyroute-backup-20260930T120000Z.tar.gz.age        12 KiB\n",
		" 2) 2026-09-29 08:45  deyroute-backup-20260929T080000.000000000Z.tar.gz  3.0 MiB  automatic\n",
		" 3) Another file (type its path)")
	h.choose("2")
	h.must("Restoring /var/lib/deyroute/backups/auto/deyroute-backup-20260929T080000.000000000Z.tar.gz", "Type yes")
	h.press("esc")
	h.choose("1")
	h.must("Restore from a backup: deyroute-backup-20260930T120000Z.tar.gz.age", "Passphrase (empty if the backup is not encrypted): _")
	h.typeLine("pw")
	h.must("Type yes")
	h.press("esc")
	h.choose("3")
	h.must("Backup file: _")
	mu.Lock()
	require.Equal(t, []string{
		"/var/lib/deyroute/backups/auto/deyroute-backup-20260929T080000.000000000Z.tar.gz|",
		"/var/lib/deyroute/backups/deyroute-backup-20260930T120000Z.tar.gz.age|pw",
	}, checked)
	mu.Unlock()

	// No backups yet: the list says where they would be.
	o.Backups = func(context.Context) ([]BackupFile, error) { return nil, nil }
	h2 := newHarness(t, o)
	h2.choose("10").choose("2")
	h2.must("No backups in /var/lib/deyroute/backups yet.", " 1) Another file (type its path)")
}

// The doctor summary is indented like every other page.
func TestDoctorIndented(t *testing.T) {
	h := newHarness(t, Options{Caps: Caps{Unicode: true}, Doctor: func(context.Context) (string, string, error) {
		return "DEYROUTE doctor: hub ir-1\nNo problems found", "", nil
	}})
	h.choose("6").choose("5")
	h.must("\n DEYROUTE doctor: hub ir-1\n No problems found\n")
}

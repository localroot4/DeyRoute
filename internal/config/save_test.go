package config

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test -update to create %s", path)
	require.Equal(t, string(want), string(got))
}

func TestRoundTripHubSample(t *testing.T) {
	opts := fakeOpts()
	c, err := ParseWith(readTestdata(t, "hub_sample.yaml"), opts)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, SaveWith(path, c, opts))

	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, FileMode, st.Mode().Perm())

	back, err := LoadWith(path, opts)
	require.NoError(t, err)
	require.Equal(t, c, back)

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	golden(t, "hub_sample.saved.golden", saved)

	// Saving the reloaded config is byte-identical (stable output).
	path2 := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, SaveWith(path2, back, opts))
	saved2, err := os.ReadFile(path2)
	require.NoError(t, err)
	require.Equal(t, string(saved), string(saved2))
}

func TestRoundTripNodeSample(t *testing.T) {
	c := validNode()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, Save(path, c))
	back, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, c, back)
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	golden(t, "node.saved.golden", saved)
}

func TestRoundTripEverything(t *testing.T) {
	c := validHub()
	c.Hub.Domain = "t.example.com"
	c.Hub.PublicIP6 = "2001:db8::1"
	c.Hub.Language = "en"
	c.Hub.DecoySNIs = []string{"www.example.com"}
	c.Hub.Mirror = "https://m.example.com/deyroute"
	c.Hub.UpdateCheck = true
	c.Hub.ACME = &ACME{Email: "o@example.com", Staging: true, RenewBeforeDays: 30}
	c.Hub.Notify.Telegram = Telegram{Enabled: true, BotTokenFile: DefaultTelegramTokenFile, ChatID: "-100123", Events: []string{"down", "update"}}
	c.Ladders["fast"] = []string{"backhaul/tcpmux", "direct/native"}
	tun := &c.Tunnels[0]
	tun.Enabled = false
	tun.Failover.Failback = false
	tun.Ladder = LadderRef{Inline: []string{"rathole/noise", "direct/native"}}
	tun.TLS = TLS{Mode: TLSModeACME}
	tun.ProbePort = 2053
	tun.Ports[1].Probe = ProbeTLS
	tun.Advanced = &Advanced{ConnectionPool: 16, HysteriaPortHopping: true, BackhaulWebPort: 8081}
	c.Tunnels = append(c.Tunnels, NewTunnel("game", "Game", []string{"nl-1"}, []PortMap{{Listen: 27015, Proto: ProtoUDP}}))
	c.Tunnels[1].Ladder = LadderRef{Name: "fast"}
	c.Tuning.BBR = false
	c.Security.RestrictControlToNodes = false

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, SaveWith(path, c, fakeOpts()))
	back, err := LoadWith(path, fakeOpts())
	require.NoError(t, err)
	require.Equal(t, c, back)
}

func TestSaveRejectsInvalid(t *testing.T) {
	c := validHub()
	c.Tunnels[0].Ports[0].Listen = 22
	path := filepath.Join(t.TempDir(), "config.yaml")
	err := Save(path, c)
	requireCodes(t, err, deyerr.C011)
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "nothing is written for an invalid config")

	c = validHub()
	c.Ladders["xx"] = []string{"nope/nope"}
	requireCodes(t, SaveWith(path, c, fakeOpts()), deyerr.C005)
}

func TestSaveWriteFailureC017(t *testing.T) {
	dir := t.TempDir()
	err := Save(filepath.Join(dir, "missing", "config.yaml"), validHub())
	requireCodes(t, err, deyerr.C017)
	require.Contains(t, firstErr(t, err).Message(), "missing")

	// Renaming over a directory fails; the temp file is cleaned up.
	target := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.Mkdir(target, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(target, "keep"), []byte("x"), 0o600))
	err = Save(target, validHub())
	requireCodes(t, err, deyerr.C017)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp file left behind: %v", entries)
}

func TestSaveReplacesAndFixesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644)) // #nosec G306 -- test fixture
	require.NoError(t, Save(path, validHub()))
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, FileMode, st.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	c, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, validHub(), c)
}

func TestMarshalLadderForms(t *testing.T) {
	c := validHub()
	c.Tunnels[0].Ladder = LadderRef{Inline: []string{"direct/native"}}
	b, err := Marshal(c)
	require.NoError(t, err)
	require.Contains(t, string(b), "    ladder:\n      - direct/native\n")
	require.Contains(t, string(b), "\nhub:\n  name: ir-1\n", "two-space indent")
}

func TestMutate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, Save(path, validHub()))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	c, err := Mutate(path, fakeOpts(), func(c *Config) error {
		c.Hub.UIMode = UIModeAdvanced
		return c.AddTunnel(NewTunnel("alt", "", []string{"nl-1"}, []PortMap{{Listen: 8443}}))
	})
	require.NoError(t, err)
	require.Equal(t, UIModeAdvanced, c.Hub.UIMode)
	back, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, c, back)
	alt, ok := back.Tunnel("alt")
	require.True(t, ok)
	require.True(t, alt.Enabled)
	require.True(t, alt.Failover.Failback)

	// fn error: nothing written.
	now, err := os.ReadFile(path)
	require.NoError(t, err)
	_, err = Mutate(path, fakeOpts(), func(*Config) error { return deyerr.New(deyerr.C008, deyerr.Params{"tunnel": "x"}) })
	requireCodes(t, err, deyerr.C008)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, now, after)
	require.NotEqual(t, before, after)

	// validation error: nothing written.
	_, err = Mutate(path, fakeOpts(), func(c *Config) error { c.Hub.ControlPort = 0; c.Hub.UIMode = "bad"; return nil })
	requireCodes(t, err, deyerr.C013)
	after2, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, now, after2)

	_, err = Mutate(filepath.Join(t.TempDir(), "none.yaml"), fakeOpts(), func(*Config) error { return nil })
	requireCodes(t, err, deyerr.C014)
}

func TestSaveNilAndWriteDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	requireCodes(t, Save(path, nil), deyerr.C016)

	err := Save(filepath.Join(t.TempDir(), "missing", "config.yaml"), validHub())
	e := firstErr(t, err)
	require.Equal(t, deyerr.C017, e.Code)
	require.Contains(t, e.Format(false), "no such file or directory", "the I/O cause reaches the owner")
}

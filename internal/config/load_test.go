package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/stretchr/testify/require"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func TestParseHubSample(t *testing.T) {
	c, err := ParseWith(readTestdata(t, "hub_sample.yaml"), fakeOpts())
	require.NoError(t, err)

	require.Equal(t, 1, c.SchemaVersion)
	require.Equal(t, RoleHub, c.Role)
	require.Equal(t, "ir-1", c.Hub.Name)
	require.Equal(t, 44433, c.Hub.ControlPort)
	require.Equal(t, "5.6.7.8", c.Hub.PublicIP)
	require.Equal(t, UIModeSimple, c.Hub.UIMode)
	require.Equal(t, DefaultTelegramTokenFile, c.Hub.Notify.Telegram.BotTokenFile)
	require.Equal(t, []string{"down", "switch", "failback", "node_offline"}, c.Hub.Notify.Telegram.Events)
	require.Len(t, c.Nodes, 2)
	require.Equal(t, []string{"primary"}, c.Nodes[0].Tags)
	require.Len(t, c.Tunnels, 1)

	tun := c.Tunnels[0]
	require.True(t, tun.Enabled)
	require.Equal(t, []string{"de-1", "nl-1"}, tun.Nodes)
	require.Equal(t, LadderRef{Name: "default"}, tun.Ladder)
	require.Equal(t, PortMap{Listen: 443, Proto: "tcp", Target: "127.0.0.1:443", Probe: ProbeAuto}, tun.Ports[0])
	// Section 9 default that is not in the sample: quarantine_s.
	require.Equal(t, DefaultFailover(), tun.Failover)
	require.Equal(t, TLSModeAuto, tun.TLS.Mode)

	// The sample's own ladder (ending with direct/haproxy) wins over the builtin.
	require.Equal(t, "direct/haproxy", c.Ladders["default"][7])
	rungs, err := c.ResolveLadder(&c.Tunnels[0], nil)
	require.NoError(t, err)
	require.Equal(t, c.Ladders["default"], rungs)

	require.Equal(t, &Tuning{SysctlProfile: SysctlBalanced, BBR: true}, c.Tuning)
	require.Equal(t, &Security{FirewallManaged: true, RestrictControlToNodes: true}, c.Security)
}

func TestParseHubSampleUnknownRung(t *testing.T) {
	// With the real registry semantics, a rung the registry does not know is C005.
	opts := fakeOpts()
	opts.KnownTransport = func(id string) bool { return id != "direct/haproxy" && knownRungs[id] }
	_, err := ParseWith(readTestdata(t, "hub_sample.yaml"), opts)
	requireCodes(t, err, deyerr.C005)
	e := firstErr(t, err)
	require.Equal(t, "direct/haproxy", e.Params["transport"])
	require.Contains(t, e.Fix(), "backhaul/wssmux")
}

func TestParseNodeSample(t *testing.T) {
	raw := readTestdata(t, "node_sample.yaml")
	// The spec sample uses the placeholder "sha256:..."; a real node pins a
	// full fingerprint, so the verbatim sample is rejected on that one field.
	_, err := Parse(raw)
	requireCodes(t, err, deyerr.C013)
	require.Equal(t, "node.hub_ca_fingerprint", firstErr(t, err).Params["field"])

	fixed := strings.Replace(string(raw), `"sha256:..."`, `"`+testFP+`"`, 1)
	c, err := Parse([]byte(fixed))
	require.NoError(t, err)
	require.Equal(t, RoleNode, c.Role)
	require.Equal(t, &NodeSelf{
		ID: "de-1", HubAddr: "5.6.7.8:44433", HubCAFingerprint: testFP,
		CertFile: "/etc/deyroute/secrets/node.crt", KeyFile: "/etc/deyroute/secrets/node.key",
	}, c.Node)
	require.Nil(t, c.Hub)
	require.Nil(t, c.Ladders, "node configs get no ladders")
	require.NotNil(t, c.Tuning)
	require.NotNil(t, c.Security)
}

func TestLoadMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.yaml")
	_, err := Load(path)
	requireCodes(t, err, deyerr.C014)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.Equal(t, path, firstErr(t, err).Params["path"])
}

func TestLoadOK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, readTestdata(t, "hub_sample.yaml"), 0o600))
	c, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "ir-1", c.Hub.Name)
}

const miniHub = `schema_version: 1
role: hub
hub:
  name: ir-1
  public_ip: 5.6.7.8
nodes:
  - id: de-1
    public_ip: 1.2.3.4
tunnels:
  - id: main
    nodes: [de-1]
    ports:
      - listen: 443
`

func TestParseMinimalAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte(miniHub))
	require.NoError(t, err)
	tun := c.Tunnels[0]
	require.True(t, tun.Enabled, "missing enabled: defaults to true")
	require.True(t, tun.Failover.Failback, "missing failback: defaults to true")
	require.Equal(t, "main", tun.Name)
	require.Equal(t, "127.0.0.1:443", tun.Ports[0].Target)
	require.Equal(t, ProtoTCP, tun.Ports[0].Proto)
	require.Equal(t, DefaultControlPort, c.Hub.ControlPort)
	require.Equal(t, DefaultLadder, c.Ladders[DefaultLadderName])
	require.True(t, c.Tuning.BBR)
	require.True(t, c.Security.FirewallManaged)
}

func TestParsePresenceAwareBooleans(t *testing.T) {
	doc := miniHub + `    enabled: false
    failover:
      failback: false
tuning:
  sysctl_profile: aggressive
security:
  firewall_managed: false
`
	c, err := Parse([]byte(doc))
	require.NoError(t, err)
	require.False(t, c.Tunnels[0].Enabled, "explicit false is kept")
	require.False(t, c.Tunnels[0].Failover.Failback, "explicit false is kept")
	require.True(t, c.Tuning.BBR, "missing bbr in a present tuning section defaults to true")
	require.Equal(t, SysctlAggressive, c.Tuning.SysctlProfile)
	require.False(t, c.Security.FirewallManaged)
	require.True(t, c.Security.RestrictControlToNodes)

	doc2 := miniHub + `    failover:
      policy: node_only
tuning:
  bbr: false
`
	c, err = Parse([]byte(doc2))
	require.NoError(t, err)
	require.True(t, c.Tunnels[0].Failover.Failback, "failover section without failback")
	require.Equal(t, PolicyNodeOnly, c.Tunnels[0].Failover.Policy)
	require.False(t, c.Tuning.BBR)
	require.Equal(t, SysctlBalanced, c.Tuning.SysctlProfile)
}

func TestParseUnknownKeysC001(t *testing.T) {
	doc := `schema_version: 1
role: hub
colour: blue
hub:
  name: ir-1
  public_ip: 5.6.7.8
  notify:
    telegram:
      token: x
nodes:
  - id: de-1
    public_ip: 1.2.3.4
tunnels:
  - id: main
    nodes: [de-1]
    ports:
      - listen: 443
        protocol: tcp
`
	_, err := Parse([]byte(doc))
	requireCodes(t, err, deyerr.C001, deyerr.C001, deyerr.C001)
	all := deyErrors(err)
	got := map[string]any{}
	for _, e := range all {
		got[e.Params["key"].(string)] = e.Params["line"]
	}
	require.Equal(t, map[string]any{
		"colour":                       3,
		"hub.notify.telegram.token":    9,
		"tunnels[0].ports[0].protocol": 18,
	}, got)
	require.Contains(t, all[0].Why(), "line 3")
}

func TestParseTypeErrorsC013(t *testing.T) {
	cases := []struct {
		name, doc, field, value string
	}{
		{"int", strings.Replace(miniHub, "listen: 443", "listen: https", 1), "tunnels[0].ports[0].listen", "https"},
		{"long value", strings.Replace(miniHub, "listen: 443", "listen: four-four-three", 1), "tunnels[0].ports[0].listen", "four-four-three"},
		{"bool", miniHub + "    enabled: maybe\n", "tunnels[0].enabled", "maybe"},
		{"section as list", strings.Replace(miniHub, "nodes: [de-1]", "nodes: {a: b}", 1), "tunnels[0].nodes", ""},
		{"ladder mapping", miniHub + "    ladder:\n      fast: true\n", "tunnels[0].ladder", "(mapping)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.doc))
			requireCodes(t, err, deyerr.C013)
			e := firstErr(t, err)
			require.Equal(t, tc.field, e.Params["field"])
			if tc.value != "" {
				require.Equal(t, tc.value, e.Params["value"])
			}
			require.NotEmpty(t, e.Params["allowed"])
		})
	}
}

func TestParseSyntaxErrorsC014(t *testing.T) {
	cases := map[string]string{
		"empty":         "",
		"comment only":  "# nothing here\n",
		"syntax":        "schema_version: 1\nrole: [hub\n",
		"tab indent":    "schema_version: 1\nhub:\n\tname: x\n",
		"two documents": "schema_version: 1\n---\nschema_version: 1\n",
		"broken second": "schema_version: 1\n---\n[\n",
		"list root":     "- a\n- b\n",
		"scalar root":   "hello\n",
		"duplicate key": "schema_version: 1\nrole: hub\nrole: node\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(doc))
			requireCodes(t, err, deyerr.C014)
			require.Equal(t, inputName, firstErr(t, err).Params["path"])
		})
	}
	_, err := Parse([]byte("schema_version: 1\nhub:\n\tname: x\n"))
	require.Equal(t, 3, firstErr(t, err).Params["line"])
	require.Contains(t, err.Error(), "line 3")
}

func TestParseSchemaVersionC019(t *testing.T) {
	cases := map[string]string{
		"missing":   "role: hub\n",
		"null":      "schema_version:\nrole: hub\n",
		"zero":      "schema_version: 0\n",
		"negative":  "schema_version: -1\n",
		"future":    "schema_version: 2\n",
		"string":    "schema_version: one\n",
		"quoted":    "schema_version: \"1\"\n",
		"float":     "schema_version: 1.5\n",
		"list":      "schema_version: [1]\n",
		"huge":      "schema_version: 99999999999999999999\n",
		"hex float": "schema_version: 0x1p3\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(doc))
			requireCodes(t, err, deyerr.C019)
		})
	}
}

func TestSchemaVersionOf(t *testing.T) {
	v, err := SchemaVersionOf(readTestdata(t, "hub_sample.yaml"))
	require.NoError(t, err)
	require.Equal(t, 1, v)
	_, err = SchemaVersionOf([]byte("schema_version: 7\n"))
	requireCodes(t, err, deyerr.C019)
	_, err = SchemaVersionOf([]byte("a: [\n"))
	requireCodes(t, err, deyerr.C014)
}

func TestDecodeDoesNotValidate(t *testing.T) {
	c, err := Decode([]byte("schema_version: 1\nrole: hub\n"))
	require.NoError(t, err)
	require.Nil(t, c.Hub, "no defaults, no validation")
	_, err = Decode([]byte("schema_version: 1\nbogus: 1\n"))
	requireCodes(t, err, deyerr.C001)
}

func TestParseValidationErrorsAreJoined(t *testing.T) {
	doc := strings.Replace(miniHub, "listen: 443", "listen: 22", 1)
	doc = strings.Replace(doc, "nodes: [de-1]", "nodes: [xx-9]", 1)
	_, err := Parse([]byte(doc))
	requireCodes(t, err, deyerr.C010, deyerr.C011)
}

func TestDescribeGoType(t *testing.T) {
	require.Equal(t, "a list", describeGoType("[]string"))
	require.Equal(t, "a section of key: value lines", describeGoType("config.Hub"))
	require.Equal(t, "a section of key: value lines", describeGoType("map[string][]string"))
	require.Equal(t, "a whole number", describeGoType("int"))
	require.Equal(t, "true or false", describeGoType("bool"))
	require.Equal(t, "a text value", describeGoType("string"))
	require.Equal(t, "float64", describeGoType("float64"))
}

func TestMapTypeErrorFallbacks(t *testing.T) {
	// A line the tree does not know still yields a usable C013/C001/C014.
	root, err := parseRoot([]byte("a: 1\n"), inputName)
	require.NoError(t, err)
	e := mapTypeErrorLine("line 42: cannot unmarshal !!str `x` into int", root, inputName)
	requireCodes(t, e, deyerr.C013)
	require.Equal(t, "line 42", firstErr(t, e).Params["field"])
	require.Equal(t, "str", firstErr(t, e).Params["value"])

	e = mapTypeErrorLine("line 42: field zz not found in type config.Config", root, inputName)
	require.Equal(t, "zz", firstErr(t, e).Params["key"])

	e = mapTypeErrorLine("something else", root, inputName)
	requireCodes(t, e, deyerr.C014)
	require.Equal(t, 0, lineOf("no line here"))
}

// TestParsePresenceDefaultsFollowAnchors: an explicit false that reaches a
// tunnel through a YAML anchor (*alias) or merge key (<<) must be kept; only
// a key that is really absent gets the spec default (true).
func TestParsePresenceDefaultsFollowAnchors(t *testing.T) {
	const head = `schema_version: 1
role: hub
hub:
  name: ir-1
  public_ip: 5.6.7.8
nodes:
  - id: de-1
    public_ip: 1.2.3.4
tunnels:
`
	cases := []struct {
		name               string
		tunnels            string
		enabled, failbacks []bool
	}{
		{
			name: "merge key keeps explicit false",
			tunnels: `  - &base
    id: main
    enabled: false
    nodes: [de-1]
    ports: [{listen: 443}]
  - <<: *base
    id: alt
    ports: [{listen: 8443}]
`,
			enabled: []bool{false, false}, failbacks: []bool{true, true},
		},
		{
			name: "own key wins over merged key",
			tunnels: `  - &base
    id: main
    enabled: false
    nodes: [de-1]
    ports: [{listen: 443}]
  - <<: *base
    id: alt
    enabled: true
    ports: [{listen: 8443}]
`,
			enabled: []bool{false, true}, failbacks: []bool{true, true},
		},
		{
			name: "merge sequence first wins",
			tunnels: `  - id: main
    enabled: false
    nodes: [de-1]
    ports: [{listen: 443}]
    failover: &slow
      failback: false
  - id: alt
    nodes: [de-1]
    ports: [{listen: 8443}]
    failover:
      <<: [*slow, {failback: true}]
`,
			enabled: []bool{false, true}, failbacks: []bool{false, false},
		},
		{
			name: "aliased failover section",
			tunnels: `  - id: main
    nodes: [de-1]
    ports: [{listen: 443}]
    failover: &fo
      failback: false
  - id: alt
    nodes: [de-1]
    ports: [{listen: 8443}]
    failover: *fo
`,
			enabled: []bool{true, true}, failbacks: []bool{false, false},
		},
		{
			name: "merge without the key still defaults",
			tunnels: `  - &base
    id: main
    nodes: [de-1]
    ports: [{listen: 443}]
  - <<: *base
    id: alt
    ports: [{listen: 8443}]
`,
			enabled: []bool{true, true}, failbacks: []bool{true, true},
		},
		{
			name: "null value counts as missing",
			tunnels: `  - id: main
    enabled: ~
    nodes: [de-1]
    ports: [{listen: 443}]
    failover:
      failback:
`,
			enabled: []bool{true}, failbacks: []bool{true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Parse([]byte(head + tc.tunnels))
			require.NoError(t, err)
			require.Len(t, c.Tunnels, len(tc.enabled))
			for i := range c.Tunnels {
				require.Equal(t, tc.enabled[i], c.Tunnels[i].Enabled, "tunnel %d enabled", i)
				require.Equal(t, tc.failbacks[i], c.Tunnels[i].Failover.Failback, "tunnel %d failback", i)
			}
		})
	}

	// Anchored tuning/security sections are followed too.
	doc := miniHub + `tuning: &tu
  bbr: false
security:
  <<: {firewall_managed: false}
`
	c, err := Parse([]byte(doc))
	require.NoError(t, err)
	require.False(t, c.Tuning.BBR)
	require.False(t, c.Security.FirewallManaged)
	require.True(t, c.Security.RestrictControlToNodes)
}

func TestLookupHelpers(t *testing.T) {
	root, err := parseRoot([]byte("a: &x {k: 1}\nb: *x\nc: {<<: [*x, {k: 2, j: 3}], i: 0}\n"), inputName)
	require.NoError(t, err)
	b, ok := lookup(root, "b")
	require.True(t, ok)
	v, ok := lookup(b, "k")
	require.True(t, ok)
	require.Equal(t, "1", v.Value)
	c, _ := lookup(root, "c")
	for key, want := range map[string]string{"k": "1", "j": "3", "i": "0"} {
		v, ok := lookup(c, key)
		require.True(t, ok, key)
		require.Equal(t, want, v.Value, key)
	}
	_, ok = lookup(c, "none")
	require.False(t, ok)
	_, ok = lookup(nil, "k")
	require.False(t, ok)
	require.False(t, hasKey(nil, "k"))
	scalar, _ := lookup(root, "a")
	_, ok = lookup(scalar.Content[1], "k") // a scalar is not a mapping
	require.False(t, ok)
}

func TestParseEmptyInlineLadderC009(t *testing.T) {
	c, err := Parse([]byte(miniHub + "    ladder: []\n"))
	require.Nil(t, c)
	requireCodes(t, err, deyerr.C009)

	// A missing or empty-string ladder is the default ladder.
	for _, doc := range []string{miniHub, miniHub + "    ladder: \"\"\n", miniHub + "    ladder:\n"} {
		c, err = Parse([]byte(doc))
		require.NoError(t, err)
		require.Equal(t, LadderRef{Name: DefaultLadderName}, c.Tunnels[0].Ladder)
	}
}

func TestParseSchemaVersionForms(t *testing.T) {
	for _, v := range []string{"1", "+1", "0o1", "0x1", "!!int 1"} {
		c, err := Parse([]byte(strings.Replace(miniHub, "schema_version: 1", "schema_version: "+v, 1)))
		require.NoError(t, err, v)
		require.Equal(t, 1, c.SchemaVersion)
	}
	for doc, want := range map[string]any{
		"schema_version: \"1\"\n":  `"1"`,
		"schema_version: [1]\n":    "(not a number)",
		"schema_version: {a: 1}\n": "(not a number)",
		"role: hub\n":              "(missing)",
		"schema_version: 1.5\n":    "1.5",
		"schema_version: 7\n":      7,
	} {
		_, err := Parse([]byte(doc))
		requireCodes(t, err, deyerr.C019)
		require.Equal(t, want, firstErr(t, err).Params["version"], doc)
	}
}

// TestParseErrorsShowCause: the owner-facing Format() block does not print
// the wrapped cause, so the YAML parser message (with its line) must be in
// the Detail of DEY-C014.
func TestParseErrorsShowCause(t *testing.T) {
	_, err := Parse([]byte("schema_version: 1\nhub:\n\tname: x\n"))
	e := firstErr(t, err)
	require.Equal(t, deyerr.C014, e.Code)
	out := e.Format(false)
	require.Contains(t, out, "line 3")
	require.Contains(t, out, "| yaml:")

	_, err = Load(filepath.Join(t.TempDir(), "nope.yaml"))
	require.Contains(t, firstErr(t, err).Format(false), "no such file")

	_, err = Parse([]byte(strings.Replace(miniHub, "listen: 443", "listen: [443]", 1)))
	e = firstErr(t, err)
	require.Equal(t, deyerr.C013, e.Code)
	require.Equal(t, "(list)", e.Params["value"])
	require.Equal(t, "line 13", e.Detail)
}

func TestDescribeYAMLTag(t *testing.T) {
	require.Equal(t, "(list)", describeYAMLTag("!!seq"))
	require.Equal(t, "(mapping)", describeYAMLTag("!!map"))
	require.Equal(t, "(empty)", describeYAMLTag("!!null"))
	require.Equal(t, "str", describeYAMLTag("!!str"))
}

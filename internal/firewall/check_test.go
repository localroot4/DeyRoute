package firewall

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

const iptablesAcceptAll = "-P INPUT ACCEPT\n-P FORWARD ACCEPT\n-P OUTPUT ACCEPT\n"

// firewall-cmd --list-all outputs (firewalld 1.x format).
const (
	firewalldTrusted = `trusted (active)
  target: ACCEPT
  icmp-block-inversion: no
  interfaces: eth0
  sources:
  services:
  ports:
  protocols:
  forward: yes
  masquerade: no
  forward-ports:
  source-ports:
  icmp-blocks:
  rich rules:
`
	firewalldPublic = `public (active)
  target: default
  icmp-block-inversion: no
  interfaces: eth0
  sources:
  services: cockpit dhcpv6-client ssh
  ports:
  protocols:
  forward: yes
  masquerade: no
  forward-ports:
  source-ports:
  icmp-blocks:
  rich rules:
	rule family="ipv4" port port="2053" protocol="tcp" accept
	rule family="ipv4" source address="10.0.0.0/8" port port="8443" protocol="tcp" accept
	rule family="ipv6" port port="5000" protocol="tcp" accept
	rule family="ipv4" port port="5000" protocol="tcp" reject
	rule service name="syncthing" accept
	rule family="ipv4" destination address="203.0.113.5" port port="7000" protocol="udp" accept
`
)

// bareFake is a system with nft and an empty iptables, no ufw/firewalld.
func bareFake() *exec.Fake {
	return exec.NewFake().
		On("nft list tables").
		On("nft list ruleset").
		On("iptables -w -S INPUT", exec.OK("-P INPUT ACCEPT\n")).
		On("iptables -w -S", exec.OK(iptablesAcceptAll)).
		On("ufw status", exec.Fail(127, "ufw: not found")).
		On("ufw status verbose", exec.Fail(127, "ufw: not found")).
		On("firewall-cmd --state", exec.Fail(252, "not running"))
}

func TestDetect(t *testing.T) {
	ctx := context.Background()

	// Nothing installed at all.
	require.Empty(t, Detect(ctx, exec.NewFake()))

	// nftables only; an ACCEPT-only iptables is not a firewall.
	require.Equal(t, []Kind{NFTables}, Detect(ctx, bareFake()))

	// Raw iptables with rules.
	f := bareFake().On("iptables -w -S INPUT", exec.OK(fixture(t, "iptables_S.txt")))
	require.Equal(t, []Kind{NFTables, IPTables}, Detect(ctx, f))

	// ufw active: its iptables rules are not reported separately.
	f = bareFake().
		On("ufw status", exec.OK(ufwPlain)).
		On("iptables -w -S INPUT", exec.OK("-P INPUT DROP\n-A INPUT -j ufw-before-input\n"))
	require.Equal(t, []Kind{NFTables, UFW}, Detect(ctx, f))
	require.False(t, f.Called("iptables -w -S INPUT"))

	// ufw inactive.
	f = bareFake().On("ufw status", exec.OK("Status: inactive\n"))
	require.Equal(t, []Kind{NFTables}, Detect(ctx, f))

	// firewalld running.
	f = bareFake().On("firewall-cmd --state", exec.OK("running\n"))
	require.Equal(t, []Kind{NFTables, Firewalld}, Detect(ctx, f))

	// Order of the commands follows section 10.
	f = bareFake()
	Detect(ctx, f)
	require.Equal(t, []string{"nft list tables", "ufw status", "firewall-cmd --state", "iptables -w -S INPUT"}, f.Lines())
}

func TestCheckNothingBlocks(t *testing.T) {
	ctx := context.Background()
	for _, f := range []*exec.Fake{exec.NewFake(), bareFake()} {
		v, err := Check(ctx, f, 443, "tcp")
		require.NoError(t, err)
		require.False(t, v.Blocked)
		require.False(t, v.Uncertain)
		require.Empty(t, v.Commands)
		require.NoError(t, v.Err(443, "tcp"))
		require.NoError(t, v.Open(ctx, f))
		blocked, by, err := Blocks(ctx, f, 443, "tcp")
		require.NoError(t, err)
		require.False(t, blocked)
		require.Empty(t, by)
	}
}

func TestCheckUFW(t *testing.T) {
	ctx := context.Background()
	f := bareFake().
		On("ufw status verbose", exec.OK(ufwVerbose)).
		On("nft list ruleset", exec.OK(fixture(t, "nft_iptables.txt"))) // ufw's own ip filter table: skipped

	v, err := Check(ctx, f, 9000, "tcp")
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.Equal(t, UFW, v.By)
	require.Equal(t, []string{"ufw allow 9000/tcp"}, v.Commands)
	require.Equal(t, "ufw allow 9000/tcp", v.Command())
	require.Equal(t, "policy drop", v.Detail)
	require.False(t, f.Called("iptables -w -S"), "iptables belongs to ufw")

	e := deyerr.As(v.Err(9000, "tcp"))
	require.Equal(t, deyerr.P013, e.Code)
	require.Equal(t, "Firewall ufw blocks port 9000/tcp", e.Message())
	require.Equal(t, "allow it: ufw allow 9000/tcp", e.Fix())

	// Open after confirmation runs exactly the suggestion.
	f.On("ufw allow 9000/tcp", exec.OK("Rule added\n"))
	require.NoError(t, v.Open(ctx, f))
	require.True(t, f.Called("ufw allow 9000/tcp"))

	// Allowed by ufw; the ip filter table (which would drop it) is skipped.
	v, err = Check(ctx, f, 443, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)

	// Uncertain ufw rule is reported but not as blocked.
	v, err = Check(ctx, f, 6000, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)
	require.True(t, v.Uncertain)
	require.Contains(t, v.Detail, "6000/tcp on eth0")
}

func TestCheckFirewalld(t *testing.T) {
	ctx := context.Background()
	f := bareFake().
		On("firewall-cmd --state", exec.OK("running\n")).
		On("firewall-cmd --list-ports", exec.OK("8443/tcp 2000-2010/udp\n")).
		On("firewall-cmd --list-services", exec.OK("dhcpv6-client ssh https\n")).
		On("nft list ruleset", exec.OK("table inet firewalld {\n\tchain filter_INPUT {\n\t\ttype filter hook input priority filter + 10; policy accept;\n\t\treject with icmpx admin-prohibited\n\t}\n}\n"))

	v, err := Check(ctx, f, 80, "tcp")
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.Equal(t, Firewalld, v.By)
	require.Equal(t, []string{"firewall-cmd --permanent --add-port=80/tcp && firewall-cmd --reload"}, v.Commands)
	require.Equal(t, "firewall-cmd --permanent --add-port=80/tcp && firewall-cmd --reload", v.Command())
	require.Contains(t, v.Detail, "80/tcp is not in the default zone")

	for _, ok := range []struct {
		port  int
		proto string
	}{{443, "tcp"}, {8443, "tcp"}, {2005, "udp"}} {
		v, err = Check(ctx, f, ok.port, ok.proto)
		require.NoError(t, err)
		require.False(t, v.Blocked, "%d/%s", ok.port, ok.proto)
	}
	require.False(t, f.Called("iptables -w -S"))

	// Open runs both commands in order.
	f.On("firewall-cmd --permanent --add-port=80/tcp", exec.OK("success\n")).On("firewall-cmd --reload", exec.OK("success\n"))
	f.Reset()
	require.NoError(t, Open(ctx, f, Firewalld, 80, "tcp"))
	require.Equal(t, []string{"firewall-cmd --permanent --add-port=80/tcp", "firewall-cmd --reload"}, f.Lines())

	// A trusted default zone (target ACCEPT) opens everything: --list-ports
	// is empty but nothing is blocked.
	trusted := bareFake().
		On("firewall-cmd --state", exec.OK("running\n")).
		On("firewall-cmd --list-ports", exec.OK("\n")).
		On("firewall-cmd --list-services", exec.OK("\n")).
		On("firewall-cmd --list-all", exec.OK(firewalldTrusted))
	v, err = Check(ctx, trusted, 8443, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)
	require.False(t, v.Uncertain)

	// Services outside the builtin map are read with --info-service; rich
	// rules open ports for everyone or make the result uncertain.
	f3 := bareFake().
		On("firewall-cmd --state", exec.OK("running\n")).
		On("firewall-cmd --list-ports", exec.OK("\n")).
		On("firewall-cmd --list-services", exec.OK("cockpit dhcpv6-client ssh\n")).
		On("firewall-cmd --list-all", exec.OK(firewalldPublic)).
		On("firewall-cmd --info-service=cockpit", exec.OK("cockpit\n  ports: 9090/tcp\n  protocols: \n  source-ports: \n")).
		On("firewall-cmd --info-service=dhcpv6-client", exec.OK("dhcpv6-client\n  ports: 546/udp\n  protocols: \n  destination: ipv6:fe80::/64\n")).
		On("firewall-cmd --info-service=syncthing", exec.OK("syncthing\n  ports: 22000/tcp 22000/udp 21027/udp\n  protocols: \n"))
	for _, c := range []portCase{
		{9090, "tcp", false, false},  // cockpit
		{546, "udp", false, false},   // dhcpv6-client
		{22, "tcp", false, false},    // ssh (builtin)
		{2053, "tcp", false, false},  // rich rule for everyone
		{22000, "tcp", false, false}, // rich rule with a service
		{8443, "tcp", true, false},   // rich rule only from 10.0.0.0/8
		{7000, "udp", false, true},   // rich rule for one destination address
		{5000, "tcp", true, false},   // ipv6 accept, ipv4 reject
	} {
		v, err = Check(ctx, f3, c.port, c.proto)
		require.NoError(t, err)
		require.Equal(t, c.blocked, v.Blocked, "%d/%s %s", c.port, c.proto, v.Detail)
		require.Equal(t, c.uncertain, v.Uncertain, "%d/%s %s", c.port, c.proto, v.Detail)
	}
	f3.Reset()
	v, _ = Check(ctx, f3, 7000, "udp")
	require.Contains(t, v.Detail, `destination address="203.0.113.5"`)
	require.Equal(t, 1, f3.Count("firewall-cmd --info-service=syncthing"), "service lookups are cached per check")

	// A zone that opens the whole protocol.
	proto := bareFake().
		On("firewall-cmd --state", exec.OK("running\n")).
		On("firewall-cmd --list-ports", exec.OK("\n")).
		On("firewall-cmd --list-services", exec.OK("\n")).
		On("firewall-cmd --list-all", exec.OK("public (active)\n  target: default\n  protocols: udp\n  rich rules: \n"))
	v, err = Check(ctx, proto, 27015, "udp")
	require.NoError(t, err)
	require.False(t, v.Blocked)
	v, err = Check(ctx, proto, 27015, "tcp")
	require.NoError(t, err)
	require.True(t, v.Blocked)

	// The refinement commands failing leaves the task's verdict: blocked.
	f4 := bareFake().
		On("firewall-cmd --state", exec.OK("running\n")).
		On("firewall-cmd --list-ports", exec.OK("\n")).
		On("firewall-cmd --list-services", exec.OK("mystery\n"))
	v, err = Check(ctx, f4, 443, "tcp")
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.True(t, f4.Called("firewall-cmd --info-service=mystery"))

	// Listing fails: uncertain, not blocked.
	f2 := bareFake().
		On("firewall-cmd --state", exec.OK("running\n")).
		On("firewall-cmd --list-ports", exec.Fail(1, "error"))
	v, err = Check(ctx, f2, 80, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)
	require.True(t, v.Uncertain)
}

func TestCheckIPTables(t *testing.T) {
	ctx := context.Background()
	f := bareFake().
		On("iptables -w -S", exec.OK(fixture(t, "iptables_S.txt"))).
		On("nft list ruleset", exec.OK(fixture(t, "nft_iptables.txt")))

	v, err := Check(ctx, f, 8443, "tcp")
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.Equal(t, IPTables, v.By)
	require.Equal(t, []string{"iptables -I INPUT -p tcp --dport 8443 -j ACCEPT"}, v.Commands)

	f.On("iptables -I INPUT -p tcp --dport 8443 -j ACCEPT", exec.Fail(4, "iptables: Resource temporarily unavailable."))
	err = v.Open(ctx, f)
	require.True(t, deyerr.HasCode(err, deyerr.P019))
	e := deyerr.As(err)
	require.Equal(t, "iptables", e.Params["firewall"])
	require.Contains(t, e.Detail, "Resource temporarily unavailable")

	v, err = Check(ctx, f, 443, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)
}

// iptables-legacy takes the xtables lock even to list rules: while another
// port check (doctor runs them in parallel), docker or fail2ban holds it, a
// listing without -w exits 4 and the blocking iptables would be skipped.
func TestCheckIPTablesWaitsForLock(t *testing.T) {
	ctx := context.Background()
	locked := exec.Fail(4, "Another app is currently holding the xtables lock. Perhaps you want to use the -w option?")
	f := bareFake().
		On("iptables -S INPUT", locked).
		On("iptables -S", locked).
		On("iptables -w -S INPUT", exec.OK("-P INPUT DROP\n")).
		On("iptables -w -S", exec.OK("-P INPUT DROP\n-P FORWARD ACCEPT\n-P OUTPUT ACCEPT\n"))

	require.Equal(t, []Kind{NFTables, IPTables}, Detect(ctx, f))
	v, err := Check(ctx, f, 9000, "tcp")
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.Equal(t, IPTables, v.By)
	require.Equal(t, []string{"iptables -I INPUT -p tcp --dport 9000 -j ACCEPT"}, v.Commands)
}

func TestCheckNFTables(t *testing.T) {
	ctx := context.Background()
	f := bareFake().On("nft list ruleset", exec.OK(fixture(t, "nft_native.txt")))

	v, err := Check(ctx, f, 9000, "tcp")
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.Equal(t, NFTables, v.By)
	require.Equal(t, "inet filter", v.Table)
	require.Equal(t, "input", v.Chain)
	require.Equal(t, []string{"nft insert rule inet filter input tcp dport 9000 accept"}, v.Commands)
	require.Contains(t, v.Detail, "inet filter chain input: nftables: counter packets 0 bytes 0 reject")
	require.Nil(t, OpenCommand(NFTables, 9000, "tcp"))

	f.On("nft insert rule inet filter input tcp dport 9000 accept")
	require.NoError(t, v.Open(ctx, f))
	require.True(t, f.Called("nft insert rule inet filter input tcp dport 9000 accept"))

	// Our own table drops the backend range from non-nodes; it is never
	// reported as an external block.
	v, err = Check(ctx, f, 30500, "tcp")
	require.NoError(t, err)
	require.Equal(t, "inet filter", v.Table) // blocked by inet filter's reject, not by inet deyroute
	v, err = Check(ctx, bareFake().On("nft list ruleset", exec.OK(Render(hubSpec()))), 30500, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)

	// ip6 tables are ignored (IPv4 check).
	v, err = Check(ctx, f, 443, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)

	// Uncertain: an interface-bound accept before the drop.
	v, err = Check(ctx, f, 7443, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)
	require.True(t, v.Uncertain)
	require.Contains(t, v.Detail, `iifname "eth0" tcp dport 7443 accept`)

	// With iptables handled, its ip filter table is not evaluated twice.
	f3 := bareFake().
		On("iptables -w -S", exec.OK(iptablesAcceptAll)).
		On("nft list ruleset", exec.OK(fixture(t, "nft_iptables.txt")))
	v, err = Check(ctx, f3, 9999, "tcp")
	require.NoError(t, err)
	require.False(t, v.Blocked)
	// Without iptables, the ip filter table is evaluated as nftables.
	f4 := exec.NewFake().On("nft list ruleset", exec.OK(fixture(t, "nft_iptables.txt")))
	v, err = Check(ctx, f4, 9999, "tcp")
	require.NoError(t, err)
	require.True(t, v.Blocked)
	require.Equal(t, "ip filter", v.Table)
	require.Equal(t, "INPUT", v.Chain)
	require.Equal(t, []string{"nft insert rule ip filter INPUT tcp dport 9999 accept"}, v.Commands)
	require.Equal(t, "nft insert rule ip filter INPUT tcp dport 9999 accept", v.Command())
}

// nft joins its arguments into one command line: a table or chain name that
// is not a plain identifier gives no command to run (the owner opens the
// port by hand), while the port is still reported blocked.
func TestCheckNFTablesOddNames(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct{ table, chain string }{{`"odd;name"`, "input"}, {"9lives", "input"}, {"filter", "in;put"}} {
		ruleset := "table inet " + tc.table + " {\n\tchain " + tc.chain + " {\n\t\ttype filter hook input priority filter; policy drop;\n\t}\n}\n"
		f := bareFake().On("nft list ruleset", exec.OK(ruleset))
		v, err := Check(ctx, f, 9000, "tcp")
		require.NoError(t, err)
		require.True(t, v.Blocked, tc.table)
		require.Equal(t, NFTables, v.By)
		require.Empty(t, v.Commands, tc.table)
		require.Empty(t, v.Command())
		require.NoError(t, v.Open(ctx, f))
		require.False(t, slices.ContainsFunc(f.Lines(), func(l string) bool { return strings.HasPrefix(l, "nft insert") }))
	}
	for _, ok := range []string{"filter", "_x", ".a/b-c_9", "INPUT"} {
		require.True(t, nftIdent(ok), ok)
	}
	for _, bad := range []string{"", "9a", "-a", "a b", "a;b", `"a"`, "a$b", strings.Repeat("a", 65)} {
		require.False(t, nftIdent(bad), bad)
	}
}

func TestCheckInvalidAndCancelled(t *testing.T) {
	for _, c := range []struct {
		port  int
		proto string
	}{{0, "tcp"}, {65536, "tcp"}, {443, "icmp"}, {443, "TCP"}} {
		_, err := Check(context.Background(), bareFake(), c.port, c.proto)
		require.True(t, deyerr.HasCode(err, deyerr.P010), "%d/%s", c.port, c.proto)
		_, _, err = Blocks(context.Background(), bareFake(), c.port, c.proto)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Check(ctx, bareFake(), 443, "tcp")
	require.True(t, deyerr.HasCode(err, deyerr.X031))
}

func TestOpenCommand(t *testing.T) {
	require.Equal(t, []string{"ufw allow 443/tcp"}, OpenCommand(UFW, 443, "tcp"))
	require.Equal(t, []string{"ufw allow 27015/udp"}, OpenCommand(UFW, 27015, "udp"))
	require.Equal(t, []string{"firewall-cmd --permanent --add-port=443/tcp && firewall-cmd --reload"}, OpenCommand(Firewalld, 443, "tcp"))
	require.Equal(t, []string{"iptables -I INPUT -p tcp --dport 443 -j ACCEPT"}, OpenCommand(IPTables, 443, "tcp"))
	require.Equal(t, []string{"iptables -I INPUT -p udp --dport 53 -j ACCEPT"}, OpenCommand(IPTables, 53, "udp"))
	require.Nil(t, OpenCommand(NFTables, 443, "tcp"))
	require.Nil(t, OpenCommand(UFW, 0, "tcp"))
	require.Nil(t, OpenCommand(UFW, 443, "sctp"))
	require.Nil(t, OpenCommand(Kind("pf"), 443, "tcp"))

	ctx := context.Background()
	f := exec.NewFake().On("ufw allow 443/tcp", exec.OK("Rule added\nRule added (v6)\n"))
	require.NoError(t, Open(ctx, f, UFW, 443, "tcp"))
	require.Equal(t, []string{"ufw allow 443/tcp"}, f.Lines())

	err := Open(ctx, f, NFTables, 443, "tcp")
	require.True(t, deyerr.HasCode(err, deyerr.P019))
	require.Contains(t, deyerr.As(err).Why(), "no generic command")

	// A failing first firewalld command stops before --reload.
	f = exec.NewFake().On("firewall-cmd --permanent --add-port=443/tcp", exec.Fail(1, "Error: INVALID_PORT"))
	err = Open(ctx, f, Firewalld, 443, "tcp")
	require.True(t, deyerr.HasCode(err, deyerr.P019))
	require.True(t, deyerr.HasCode(err, deyerr.X007))
	require.Equal(t, []string{"firewall-cmd --permanent --add-port=443/tcp"}, f.Lines())
}

package firewall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(b)
}

const ufwVerbose = `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), deny (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
Nginx Full                 ALLOW IN    Anywhere
2000:2010/tcp              ALLOW IN    Anywhere
8443                       DENY IN     Anywhere
8443/tcp                   ALLOW IN    Anywhere
9000/tcp                   ALLOW IN    10.0.0.0/8
27015/udp                  LIMIT IN    Anywhere                   # game server
1.2.3.4 5000/tcp           ALLOW IN    Anywhere
6000/tcp on eth0           ALLOW IN    Anywhere
7000/tcp                   ALLOW OUT   Anywhere
53,853/udp                 ALLOW IN    Anywhere
22/tcp (v6)                ALLOW IN    Anywhere (v6)
3000/tcp (v6)              ALLOW IN    Anywhere (v6)
`

const ufwPlain = `Status: active

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW       Anywhere
443                        ALLOW       Anywhere
Anywhere                   ALLOW       192.0.2.10
`

type portCase struct {
	port      int
	proto     string
	blocked   bool
	uncertain bool
}

func TestUFW(t *testing.T) {
	st := parseUFW(ufwVerbose)
	require.True(t, st.active)
	require.Equal(t, vDrop, st.defaultIn)
	cases := []portCase{
		{22, "tcp", false, false},
		{80, "tcp", false, false},    // Nginx Full
		{443, "tcp", false, false},   // Nginx Full
		{443, "udp", true, false},    // Nginx Full is tcp only
		{2005, "tcp", false, false},  // range
		{2011, "tcp", true, false},   // outside range
		{8443, "tcp", true, false},   // DENY comes first
		{9000, "tcp", true, false},   // only from 10.0.0.0/8
		{27015, "udp", false, false}, // LIMIT allows
		{27015, "tcp", true, false},  // udp only
		{853, "udp", false, false},   // port list
		{5000, "tcp", false, true},   // destination-specific: may be ours
		{6000, "tcp", false, true},   // interface-specific
		{7000, "tcp", true, false},   // OUT rules do not count
		{3000, "tcp", true, false},   // v6 rules do not count
	}
	for _, c := range cases {
		b, u, detail := st.ruleset(c.port, c.proto).blocks("ufw")
		require.Equal(t, c.blocked, b, "%d/%s %s", c.port, c.proto, detail)
		require.Equal(t, c.uncertain, u, "%d/%s %s", c.port, c.proto, detail)
		if c.uncertain {
			require.NotEmpty(t, detail)
		}
	}
	b, _, detail := st.ruleset(8443, "tcp").blocks("ufw")
	require.True(t, b)
	require.Contains(t, detail, "8443                       DENY IN")

	_, _, detail = st.ruleset(7000, "tcp").blocks("ufw")
	require.Equal(t, "policy drop", detail)

	// An application profile deyroute does not know may cover any port.
	custom := parseUFW(ufwVerbose + "MyCustomApp                ALLOW IN    Anywhere\n")
	b, u, detail := custom.ruleset(7000, "tcp").blocks("ufw")
	require.False(t, b)
	require.True(t, u)
	require.Contains(t, detail, "MyCustomApp")
	b, _, _ = custom.ruleset(8443, "tcp").blocks("ufw") // the DENY still comes first
	require.True(t, b)

	plain := parseUFW(ufwPlain)
	require.True(t, plain.active)
	b, _, _ = plain.ruleset(443, "udp").blocks("ufw")
	require.False(t, b)
	b, _, _ = plain.ruleset(8080, "tcp").blocks("ufw")
	require.True(t, b)

	allow := parseUFW("Status: active\nDefault: allow (incoming), allow (outgoing), disabled (routed)\n\nTo Action From\n-- ------ ----\n25 DENY Anywhere\n")
	b, _, _ = allow.ruleset(443, "tcp").blocks("ufw")
	require.False(t, b)
	b, _, _ = allow.ruleset(25, "tcp").blocks("ufw")
	require.True(t, b)

	require.False(t, parseUFW("Status: inactive\n").active)
	m, _ := ufwRule("garbage line", 1, "tcp")
	require.Equal(t, maybe, m)
	m, v := ufwRule("Anywhere/udp               ALLOW       Anywhere", 53, "udp")
	require.Equal(t, yes, m)
	require.Equal(t, vAccept, v)
	m, _ = ufwRule("Anywhere/udp               ALLOW       Anywhere", 53, "tcp")
	require.Equal(t, no, m)
	m, v = ufwRule("Anywhere                   REJECT      Anywhere", 53, "tcp")
	require.Equal(t, yes, m)
	require.Equal(t, vDrop, v)
	m, _ = ufwRule("10.0.0.1                   ALLOW       Anywhere", 53, "tcp")
	require.Equal(t, maybe, m)
}

func TestFirewalldOpen(t *testing.T) {
	ports := "8443/tcp 2000-2010/udp\n"
	svcs := "dhcpv6-client ssh https\n"
	cases := []struct {
		port  int
		proto string
		open  bool
	}{
		{443, "tcp", true}, {22, "tcp", true}, {8443, "tcp", true}, {2005, "udp", true},
		{2005, "tcp", false}, {80, "tcp", false}, {443, "udp", false}, {546, "udp", false},
	}
	for _, c := range cases {
		require.Equal(t, c.open, firewalldOpen(ports, svcs, c.port, c.proto), "%d/%s", c.port, c.proto)
	}
	require.True(t, firewalldOpen("", "http", 80, "tcp"))
	require.False(t, firewalldOpen("bogus 443", "", 443, "tcp"))
}

func TestFirewalldRich(t *testing.T) {
	cases := []struct {
		rule  string
		port  int
		proto string
		want  tri
	}{
		{`rule family="ipv4" port port="443" protocol="tcp" accept`, 443, "tcp", yes},
		{`rule family="ipv4" port port="443" protocol="tcp" accept`, 443, "udp", no},
		{`rule family="ipv4" port port="2000-2010" protocol="udp" accept`, 2005, "udp", yes},
		{`rule port port="443" protocol="tcp" accept limit value="10/m"`, 443, "tcp", yes},
		{`rule family="ipv6" port port="443" protocol="tcp" accept`, 443, "tcp", no},
		{`rule family="ipv4" source address="10.0.0.0/8" port port="443" protocol="tcp" accept`, 443, "tcp", no},
		{`rule family="ipv4" source ipset="allowed" accept`, 443, "tcp", no},
		{`rule family="ipv4" source address="0.0.0.0/0" port port="443" protocol="tcp" accept`, 443, "tcp", yes},
		{`rule family="ipv4" source NOT address="198.51.100.7" port port="443" protocol="tcp" accept`, 443, "tcp", maybe},
		{`rule family="ipv4" destination address="203.0.113.5" port port="443" protocol="tcp" accept`, 443, "tcp", maybe},
		{`rule family="ipv4" port port="443" protocol="tcp" log prefix="https " level="info" accept`, 443, "tcp", yes},
		{`rule family="ipv4" port port="443" protocol="tcp" reject`, 443, "tcp", no},
		{`rule family="ipv4" port port="443" protocol="tcp" drop`, 443, "tcp", no},
		{`rule family="ipv4" port port="443" protocol="tcp" log prefix="x"`, 443, "tcp", no},
		{`rule service name="https" accept`, 443, "tcp", yes},
		{`rule service name="https" accept`, 80, "tcp", no},
		{`rule service name="cockpit" accept`, 9090, "tcp", maybe},
		{`rule protocol value="tcp" accept`, 443, "tcp", yes},
		{`rule protocol value="udp" accept`, 443, "tcp", no},
		{`rule family="ipv4" accept`, 443, "tcp", yes},
		{`rule family="ipv4" source-port port="53" protocol="udp" accept`, 53, "udp", no},
		{`rule family="ipv4" forward-port port="443" protocol="tcp" to-port="8443" accept`, 443, "tcp", maybe},
		{`rule family="ipv4" icmp-type name="echo-request" accept`, 443, "tcp", no},
		{`not a rule`, 443, "tcp", maybe},
	}
	for _, c := range cases {
		require.Equal(t, c.want, firewalldRich(c.rule, c.port, c.proto, nil), "%s (%d/%s)", c.rule, c.port, c.proto)
	}

	// A resolver turns unknown services into their ports.
	lookup := func(name string) (string, []string, bool) {
		switch name {
		case "cockpit":
			return "9090/tcp", nil, true
		case "gre-all":
			return "", []string{"udp"}, true
		}
		return "", nil, false
	}
	require.Equal(t, yes, firewalldRich(`rule service name="cockpit" accept`, 9090, "tcp", lookup))
	require.Equal(t, no, firewalldRich(`rule service name="cockpit" accept`, 9091, "tcp", lookup))
	require.Equal(t, yes, firewalldRich(`rule service name="gre-all" accept`, 1, "udp", lookup))
	require.Equal(t, maybe, firewalldRich(`rule service name="other" accept`, 1, "udp", lookup))

	z := parseFirewalldZone("public (active)\n  target: %%REJECT%%\n  protocols: gre tcp\n  rich rules: \n\trule family=\"ipv4\" accept\n  rule outside the rich section\n")
	require.Equal(t, "%%REJECT%%", z.target)
	require.Equal(t, []string{"gre", "tcp"}, z.protocols)
	require.Equal(t, []string{`rule family="ipv4" accept`, "rule outside the rich section"}, z.rich)
	z = parseFirewalldZone("public\n  rich rules: \n  target: default\n  rule family=\"ipv4\" accept\n")
	require.Empty(t, z.rich, "rules are only read inside the rich rules section")

	ports, protos := parseFirewalldService("svc\n  ports: 80/tcp 443/tcp\n  protocols: udplite\n  summary: x: y\n")
	require.Equal(t, "80/tcp 443/tcp", ports)
	require.Equal(t, []string{"udplite"}, protos)
}

func TestIPTables(t *testing.T) {
	out := fixture(t, "iptables_S.txt")
	cases := []portCase{
		{22, "tcp", false, false},   // via f2b-sshd (returns) then accept
		{80, "tcp", false, false},   // multiport
		{443, "tcp", false, false},  // multiport
		{443, "udp", true, false},   // tcp only
		{1500, "udp", false, false}, // range 1000:2000
		{2001, "udp", true, false},
		{8443, "tcp", true, false}, // only from 10.0.0.0/8
		{9999, "tcp", true, false},
	}
	for _, c := range cases {
		b, u, detail := parseIPTables(out, c.port, c.proto).blocks("INPUT")
		require.Equal(t, c.blocked, b, "%d/%s %s", c.port, c.proto, detail)
		require.Equal(t, c.uncertain, u, "%d/%s %s", c.port, c.proto, detail)
	}
	_, _, detail := parseIPTables(out, 9999, "tcp").blocks("INPUT")
	require.Equal(t, "policy drop", detail)

	// RHEL-style catch-all reject with an accept policy, an interface-bound
	// accept (uncertain), an unknown match module and a goto.
	rhel := `-P INPUT ACCEPT
-N IN_extra
-N GOTO_chain
-A INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT
-A INPUT -i lo -j ACCEPT
-A INPUT ! -i lo -d 127.0.0.0/8 -j REJECT
-A INPUT -p tcp -m state --state NEW -m tcp --dport 22 -j ACCEPT
-A INPUT -i eth0 -p tcp --dport 8080 -j ACCEPT
-A INPUT -p tcp -m recent --name x --rcheck -j DROP
-A INPUT -p tcp -m set --match-set allowed src -j ACCEPT
-A INPUT -p udp -j IN_extra
-A INPUT -p tcp --dport 7000 -g GOTO_chain
-A INPUT -p tcp --dport 7001 -j DOCKER-UNKNOWN
-A INPUT -p tcp --sport 53 -j ACCEPT
-A INPUT -p tcp ! --dport 9000 -j REJECT --reject-with tcp-reset
-A INPUT -j REJECT --reject-with icmp-host-prohibited
-A IN_extra -p udp --dport 5353 -j ACCEPT
-A IN_extra -j RETURN
-A GOTO_chain -p tcp --dport 7000 -j RETURN
`
	cases = []portCase{
		{22, "tcp", false, false},
		{8080, "tcp", false, true}, // eth0 accept may apply
		{443, "tcp", false, true},  // ipset accept may apply
		{5353, "udp", false, false},
		{5354, "udp", true, false},
	}
	for _, c := range cases {
		b, u, detail := parseIPTables(rhel, c.port, c.proto).blocks("INPUT")
		require.Equal(t, c.blocked, b, "%d/%s %s", c.port, c.proto, detail)
		require.Equal(t, c.uncertain, u, "%d/%s %s", c.port, c.proto, detail)
	}

	// An extension target that is not a chain (NFQUEUE hands the packet to
	// an IPS that may accept it) is never skipped as if it did nothing.
	queue := "-P INPUT DROP\n-A INPUT -p tcp --dport 443 -j NFQUEUE --queue-num 0\n-A INPUT -p tcp --dport 22 -j ACCEPT\n"
	cases = []portCase{
		{443, "tcp", false, true},
		{22, "tcp", false, false},
		{80, "tcp", true, false},
	}
	for _, c := range cases {
		b, u, detail := parseIPTables(queue, c.port, c.proto).blocks("INPUT")
		require.Equal(t, c.blocked, b, "%d/%s %s", c.port, c.proto, detail)
		require.Equal(t, c.uncertain, u, "%d/%s %s", c.port, c.proto, detail)
	}

	simple := `-P INPUT ACCEPT
-N GOTO_chain
-A INPUT -p tcp --dport 7000 -g GOTO_chain
-A INPUT -p tcp ! --dport 9000 -j REJECT --reject-with tcp-reset
-A GOTO_chain -p tcp --dport 7000 -j RETURN
`
	// goto + RETURN ends INPUT: policy ACCEPT applies.
	b, _, _ := parseIPTables(simple, 7000, "tcp").blocks("INPUT")
	require.False(t, b)
	b, _, _ = parseIPTables(simple, 9000, "tcp").blocks("INPUT")
	require.False(t, b)
	b, _, detail = parseIPTables(simple, 9001, "tcp").blocks("INPUT")
	require.True(t, b)
	require.Contains(t, detail, "! --dport 9000")

	require.True(t, iptablesActive("-P INPUT DROP\n"))
	require.True(t, iptablesActive("-P INPUT ACCEPT\n-A INPUT -j ACCEPT\n"))
	require.False(t, iptablesActive("-P INPUT ACCEPT\n"))
	require.False(t, iptablesActive(""))

	require.Equal(t, []string{"-A", "INPUT", "-m", "comment", "--comment", `say "hi" there`, "-j", "ACCEPT"},
		splitArgs(`-A INPUT -m comment --comment "say \"hi\" there" -j ACCEPT`))
	require.Equal(t, []string{`"lo"`, "accept"}, tokenize(`"lo" accept`, true))
	require.Equal(t, []string{`""`}, tokenize(`""`, true))
}

func TestNFTParse(t *testing.T) {
	tables := parseNFTRuleset(fixture(t, "nft_native.txt"))
	var refs []string
	for _, tb := range tables {
		refs = append(refs, tb.ref())
	}
	require.Equal(t, []string{"inet filter", "ip nat", "ip6 filter6", "inet deyroute"}, refs)
	filter := tables[0]
	require.Len(t, filter.sets["allowed_tcp"], 23)
	require.Contains(t, filter.sets["allowed_tcp"], "8018")
	require.Contains(t, filter.sets["allowed_tcp"], "2000-2010")
	require.Equal(t, []string{"198.51.100.7"}, filter.sets["blocklist"])
	require.Len(t, filter.inputChains(), 1)
	require.Equal(t, "input", filter.inputChains()[0].name)
	require.Equal(t, vDrop, filter.inputChains()[0].policy)
	require.Empty(t, tables[1].inputChains()) // nat prerouting
	dey := tables[3]
	require.Equal(t, "input", dey.inputChains()[0].name)
	require.Equal(t, vAccept, dey.inputChains()[0].policy)

	cases := []portCase{
		{22, "tcp", false, false},
		{443, "tcp", false, false},
		{2005, "tcp", false, false},
		{8018, "tcp", false, false}, // element on a continuation line
		{7443, "tcp", false, true},  // iifname eth0 accept may apply before the drop
		{9000, "tcp", true, false},  // falls to the final reject
		{53, "udp", false, false},
		{51820, "udp", false, false},
		{27015, "udp", true, false}, // verdict map drop
		{5000, "udp", true, false},
	}
	rsFor := func(port int, proto string) ruleset { return filter.ruleset(port, proto) }
	for _, c := range cases {
		b, u, detail := rsFor(c.port, c.proto).blocks("input")
		require.Equal(t, c.blocked, b, "%d/%s %s", c.port, c.proto, detail)
		require.Equal(t, c.uncertain, u, "%d/%s %s", c.port, c.proto, detail)
	}
	_, _, detail := rsFor(9000, "tcp").blocks("input")
	require.Contains(t, detail, "reject with icmpx admin-prohibited")
	_, _, detail = rsFor(27015, "udp").blocks("input")
	require.Contains(t, detail, "vmap")

	// iptables-nft translation, including a trailing `log prefix "drop"`
	// that must not be read as a drop verdict.
	ipt := parseNFTRuleset(fixture(t, "nft_iptables.txt"))
	require.Len(t, ipt, 1)
	require.Equal(t, "ip filter", ipt[0].ref())
	cases = []portCase{
		{22, "tcp", false, false}, {443, "tcp", false, false}, {1500, "udp", false, false},
		{8443, "tcp", true, false}, {9999, "tcp", true, false},
	}
	for _, c := range cases {
		b, u, detail := ipt[0].ruleset(c.port, c.proto).blocks("INPUT")
		require.Equal(t, c.blocked, b, "%d/%s %s", c.port, c.proto, detail)
		require.Equal(t, c.uncertain, u, "%d/%s %s", c.port, c.proto, detail)
	}
	_, _, detail = ipt[0].ruleset(9999, "tcp").blocks("INPUT")
	require.Equal(t, "policy drop", detail)
}

// TestNFTCommentsAndMaps uses a ruleset listed by nft 1.0.9: comments after
// verdicts, a named verdict map with a jump, and concatenations. A commented
// accept used to be read as a non-terminating rule, so every commented open
// port of a policy-drop chain was reported blocked.
func TestNFTCommentsAndMaps(t *testing.T) {
	tables := parseNFTRuleset(fixture(t, "nft_comments.txt"))
	require.Len(t, tables, 1)
	fw := tables[0]
	require.Equal(t, []string{"8080 : accept", "8081 : drop", "8082 : jump web"}, fw.maps["svc"])
	cases := []portCase{
		{443, "tcp", false, false},  // accept comment "https"
		{53, "udp", false, false},   // accept comment "dns"
		{8080, "tcp", false, false}, // map: accept
		{8081, "tcp", true, false},  // map: drop
		{8082, "tcp", false, false}, // map: jump web, which accepts 8082
		{22, "tcp", false, true},    // jump web returns; then concatenations may accept
		{9999, "tcp", false, true},
	}
	for _, c := range cases {
		b, u, detail := fw.ruleset(c.port, c.proto).blocks("input")
		require.Equal(t, c.blocked, b, "%d/%s %s", c.port, c.proto, detail)
		require.Equal(t, c.uncertain, u, "%d/%s %s", c.port, c.proto, detail)
	}
	_, _, detail := fw.ruleset(9999, "tcp").blocks("input")
	require.Contains(t, detail, "ip saddr . tcp dport @allowed accept")
}

func TestNFTRuleForms(t *testing.T) {
	sets := map[string][]string{"web": {"80", "https"}}
	cases := []struct {
		rule    string
		port    int
		proto   string
		match   tri
		verdict verdict
	}{
		{`tcp dport 443 accept`, 443, "tcp", yes, vAccept},
		{`tcp dport != 443 drop`, 443, "tcp", no, vDrop},
		{`tcp dport != 443 drop`, 80, "tcp", yes, vDrop},
		{`tcp dport { 22, 443 } accept`, 443, "tcp", yes, vAccept},
		{`tcp dport https accept`, 443, "tcp", yes, vAccept},
		{`tcp dport @web accept`, 443, "tcp", yes, vAccept},
		{`tcp dport @web accept`, 22, "tcp", no, vAccept},
		{`tcp dport @missing accept`, 22, "tcp", maybe, vAccept},
		{`tcp dport sometcpname accept`, 22, "tcp", maybe, vAccept},
		{`tcp dport < 1024 accept`, 443, "tcp", yes, vAccept},
		{`tcp dport > 1024 accept`, 443, "tcp", no, vAccept},
		{`tcp dport <= 443 accept`, 443, "tcp", yes, vAccept},
		{`tcp dport >= 444 accept`, 443, "tcp", no, vAccept},
		{`tcp dport < abc accept`, 443, "tcp", maybe, vAccept},
		{`tcp dport < { 1, 2 } accept`, 443, "tcp", maybe, vAccept},
		{`udp dport 443 accept`, 443, "tcp", no, vAccept},
		{`th dport 443 accept`, 443, "udp", yes, vAccept},
		{`tcp sport 53 accept`, 443, "tcp", no, vAccept},
		{`tcp sport != 53 accept`, 443, "tcp", maybe, vAccept},
		{`tcp option maxseg size 1400 accept`, 443, "tcp", maybe, vAccept},
		{`ip saddr 0.0.0.0/0 accept`, 443, "tcp", yes, vAccept},
		{`ip saddr 10.0.0.0/8 accept`, 443, "tcp", no, vAccept},
		{`ip saddr != @allowed drop`, 443, "tcp", maybe, vDrop},
		{`ip daddr 127.0.0.0/8 drop`, 443, "tcp", no, vDrop},
		{`iifname != "lo" ip daddr 127.0.0.0/8 drop`, 443, "tcp", no, vDrop},
		{`ip daddr != 127.0.0.0/8 accept`, 443, "tcp", yes, vAccept},
		{`ip daddr 0.0.0.0/0 accept`, 443, "tcp", yes, vAccept},
		{`ip daddr 1.2.3.4 accept`, 443, "tcp", maybe, vAccept},
		{`ip protocol tcp accept`, 443, "tcp", yes, vAccept},
		{`ip protocol != tcp drop`, 443, "tcp", no, vDrop},
		{`ip protocol vmap { tcp : accept }`, 443, "tcp", yes, vAccept},
		{`ip version 4 accept`, 443, "tcp", yes, vAccept},
		{`ip ttl 64 accept`, 443, "tcp", maybe, vAccept},
		{`ip dscp set cs1 accept`, 443, "tcp", yes, vAccept},
		{`ip6 saddr ::1 accept`, 443, "tcp", no, vAccept},
		{`icmp type echo-request accept`, 443, "tcp", no, vAccept},
		{`meta l4proto tcp accept`, 443, "tcp", yes, vAccept},
		{`meta l4proto { tcp, udp } accept`, 443, "udp", yes, vAccept},
		{`meta nfproto ipv6 drop`, 443, "tcp", no, vDrop},
		{`meta nfproto ipv4 drop`, 443, "tcp", yes, vDrop},
		{`meta iifname "lo" accept`, 443, "tcp", no, vAccept},
		{`meta oifname "eth0" accept`, 443, "tcp", yes, vAccept},
		{`meta pkttype host accept`, 443, "tcp", yes, vAccept},
		{`meta pkttype { broadcast, multicast } drop`, 443, "tcp", no, vDrop},
		{`meta mark 0x1 accept`, 443, "tcp", maybe, vAccept},
		{`meta mark set 0x1`, 443, "tcp", yes, vNone},
		{`meta nftrace set 1`, 443, "tcp", yes, vNone},
		{`iif "lo" accept`, 443, "tcp", no, vAccept},
		{`iif lo accept`, 443, "tcp", no, vAccept},
		{`iifname { "eth0", "lo" } accept`, 443, "tcp", maybe, vAccept},
		{`iifname vmap { "lo" : accept }`, 443, "tcp", no, vNone},
		{`iifname vmap { "lo" : accept, "eth0" : drop }`, 443, "tcp", no, vNone}, // a maybe-drop is ignored
		{`iifname vmap { "lo" : accept, "eth0" : jump wan }`, 443, "tcp", maybe, vAccept},
		{`oif "eth0" accept`, 443, "tcp", yes, vAccept},
		{`ct state new accept`, 443, "tcp", yes, vAccept},
		{`ct state { established, related } accept`, 443, "tcp", no, vAccept},
		{`ct state != established drop`, 443, "tcp", yes, vDrop},
		{`ct direction original accept`, 443, "tcp", yes, vAccept},
		{`ct status dnat accept`, 443, "tcp", maybe, vAccept},
		{`ct mark set 1`, 443, "tcp", yes, vNone},
		{`ct state vmap { new : jump in_new, established : accept }`, 443, "tcp", yes, vJump},
		{`ct state vmap { new : goto in_new }`, 443, "tcp", yes, vGoto},
		{`ct state vmap { new : return }`, 443, "tcp", yes, vReturn},
		{`ct state vmap { new : continue }`, 443, "tcp", yes, vNone},
		{`ct state vmap { new : jump }`, 443, "tcp", maybe, vAccept}, // unreadable verdict: may accept
		{`fib daddr type local accept`, 443, "tcp", yes, vAccept},
		{`fib daddr type { broadcast, multicast } drop`, 443, "tcp", no, vDrop},
		{`fib daddr type != local drop`, 443, "tcp", no, vDrop},
		{`tcp flags syn / fin,syn,rst,ack accept`, 443, "tcp", maybe, vAccept},
		{`counter packets 10 bytes 600 accept`, 443, "tcp", yes, vAccept},
		{`counter name "c1" accept`, 443, "tcp", yes, vAccept},
		{`log prefix "in: " level warn flags all group 2 accept`, 443, "tcp", yes, vAccept},
		{`log prefix "drop"`, 443, "tcp", yes, vNone},
		{`limit rate 10/second burst 20 packets accept`, 443, "tcp", yes, vAccept},
		{`limit rate over 10 mbytes/second drop`, 443, "tcp", maybe, vDrop},
		{`comment "reject" accept`, 443, "tcp", yes, vAccept},
		{`tcp dport 443 reject with tcp reset`, 443, "tcp", yes, vDrop},
		{`tcp dport 443 jump web`, 443, "tcp", yes, vJump},
		{`tcp dport 443 goto web`, 443, "tcp", yes, vGoto},
		{`tcp dport 443 return`, 443, "tcp", yes, vReturn},
		{`tcp dport 443 continue`, 443, "tcp", yes, vNone},
		{`tcp dport 443 counter`, 443, "tcp", yes, vNone},
		{`xt match "recent" accept`, 443, "tcp", maybe, vAccept},
		{`tcp dport vmap { 443 : drop, 80 : accept }`, 443, "tcp", yes, vDrop},
		{`tcp dport vmap { 22 : accept }`, 443, "tcp", no, vNone},
		{`tcp dport vmap { 443 }`, 443, "tcp", no, vNone},
		{`meta l4proto vmap { udp : drop }`, 443, "tcp", no, vNone},
		{`tcp dport vmap { https : accept }`, 443, "tcp", yes, vAccept},
		{`tcp dport vmap { 443:accept }`, 443, "tcp", yes, vAccept},
		{`meta l4proto tcp ip daddr vmap { 2001:db8::1 : drop }`, 443, "tcp", no, vNone}, // a maybe-drop is ignored
		{`ip daddr vmap { 203.0.113.5 : accept }`, 443, "tcp", maybe, vAccept},
		{`ip daddr vmap { 127.0.0.0/8 : accept }`, 443, "tcp", no, vNone},
		{`ip daddr vmap { 0.0.0.0/0 : drop }`, 443, "tcp", yes, vDrop},
		{`ip saddr vmap { 10.0.0.0/8 : accept }`, 443, "tcp", no, vNone},
		{`ip saddr vmap { 0.0.0.0/0 : jump in }`, 443, "tcp", yes, vJump},
		{`meta mark vmap { 0x1 : accept }`, 443, "tcp", maybe, vAccept},
		{`udp dport vmap { 443 : accept }`, 443, "tcp", no, vAccept},
		{`tcp dport vmap { webports : accept }`, 443, "tcp", maybe, vAccept}, // unknown service name
		{`tcp dport vmap { webports : drop }`, 443, "tcp", no, vNone},
		// nft prints the comment after the verdict.
		{`tcp dport 443 accept comment "https"`, 443, "tcp", yes, vAccept},
		{`tcp dport 443 counter packets 0 bytes 0 accept comment "a \"quoted\" drop"`, 443, "tcp", yes, vAccept},
		{`tcp dport 22 jump web comment "ssh"`, 22, "tcp", yes, vJump},
		{`tcp dport 443 drop comment "no"`, 443, "tcp", yes, vDrop},
		// Named verdict maps: unknown ones may accept.
		{`tcp dport vmap @missing`, 443, "tcp", maybe, vAccept},
		// Concatenations cannot be evaluated: they may match.
		{`ip saddr . tcp dport @allowed accept`, 443, "tcp", maybe, vAccept},
		{`ip daddr . tcp dport { 10.9.9.9 . 9100 } drop`, 443, "tcp", maybe, vDrop},
		{`ip saddr . tcp dport vmap @m`, 443, "tcp", maybe, vAccept},
		// queue hands the packet to a program that may accept it.
		{`tcp dport 443 queue flags bypass to 0`, 443, "tcp", maybe, vAccept},
		{`tcp dport 443 queue num 0 bypass`, 443, "tcp", maybe, vAccept},
		{`tcp dport 80 queue to 0`, 443, "tcp", no, vAccept},
	}
	for _, c := range cases {
		r := nftRule(c.rule, sets, c.port, c.proto)
		require.Equal(t, c.match, r.match, "%s (%d/%s)", c.rule, c.port, c.proto)
		require.Equal(t, c.verdict, r.verdict, "%s (%d/%s)", c.rule, c.port, c.proto)
	}
	r := nftRule(`ct state vmap { new : jump in_new }`, nil, 443, "tcp")
	require.Equal(t, "in_new", r.target)

	// Named verdict maps are resolved from the table.
	maps := map[string][]string{"svc": {"8080 : accept", "8081 : drop", "8082 : jump web"}}
	for _, c := range []struct {
		port    int
		match   tri
		verdict verdict
		target  string
	}{{8080, yes, vAccept, ""}, {8081, yes, vDrop, ""}, {8082, yes, vJump, "web"}, {9000, no, vNone, ""}} {
		r = nftRuleIn(`tcp dport vmap @svc`, nil, maps, c.port, "tcp")
		require.Equal(t, c.match, r.match, c.port)
		require.Equal(t, c.verdict, r.verdict, c.port)
		require.Equal(t, c.target, r.target, c.port)
	}

	// Unknown blocks, table flags, chain comments and multi-line rules.
	text := `table inet t {
	flags dormant
	flowtable f {
		hook ingress priority filter
		devices = { lo }
	}
	counter c1 {
		packets 0 bytes 0
	}
	chain input {
		comment "main input"
		type filter hook input priority filter
		policy drop;
		tcp dport { 22,
			443 } accept
	}
}
table noname {
	chain input {
		type filter hook input priority 0; policy accept;
	}
}
junk outside any table
`
	tabs := parseNFTRuleset(text)
	require.Len(t, tabs, 2)
	require.Equal(t, "ip noname", tabs[1].ref())
	c := tabs[0].inputChains()[0]
	require.Equal(t, vDrop, c.policy)
	require.Equal(t, []string{"tcp dport { 22, 443 } accept"}, c.rules)
	b, _, _ := tabs[0].ruleset(443, "tcp").blocks("input")
	require.False(t, b)
	b, _, _ = tabs[0].ruleset(80, "tcp").blocks("input")
	require.True(t, b)

	require.Nil(t, parseElements("type ipv4_addr\n"))
	require.Nil(t, parseElements("elements = broken"))
	require.Equal(t, []string{"1.2.3.4", "5.6.7.8"}, parseElements("elements = { 1.2.3.4 timeout 1h expires 59m, 5.6.7.8 }"))
	_, _, ok := nftPort("a-b")
	require.False(t, ok)
}

func TestEvalEdgeCases(t *testing.T) {
	// A jump loop is cut and reported as uncertain; the policy still applies.
	rs := ruleset{
		"input": {policy: vDrop, rules: []evalRule{{match: yes, verdict: vJump, target: "a", text: "jump a"}}},
		"a":     {rules: []evalRule{{match: yes, verdict: vJump, target: "a", text: "jump a again"}}},
	}
	b, u, detail := rs.blocks("input")
	require.False(t, b)
	require.True(t, u)
	require.Contains(t, detail, "cannot follow jump")

	// A maybe-jump into a chain that accepts makes a later drop uncertain.
	rs = ruleset{
		"input": {policy: vDrop, rules: []evalRule{{match: maybe, verdict: vJump, target: "ok", text: "maybe jump ok"}}},
		"ok":    {rules: []evalRule{{match: yes, verdict: vAccept, text: "accept"}}},
	}
	b, u, detail = rs.blocks("input")
	require.False(t, b)
	require.True(t, u)
	require.Equal(t, "rule not understood: maybe jump ok", detail)

	// A certain jump into a dropping chain blocks.
	rs = ruleset{
		"input": {policy: vAccept, rules: []evalRule{{match: yes, verdict: vJump, target: "deny", text: "jump deny"}}},
		"deny":  {rules: []evalRule{{match: yes, verdict: vDrop, text: "drop all"}}},
	}
	b, u, detail = rs.blocks("input")
	require.True(t, b)
	require.False(t, u)
	require.Equal(t, "drop all", detail)

	// A maybe-return is uncertain; unknown base chain accepts.
	rs = ruleset{
		"input": {policy: vDrop, rules: []evalRule{{match: yes, verdict: vJump, target: "r", text: "jump r"}}},
		"r":     {rules: []evalRule{{match: maybe, verdict: vReturn, text: "maybe return"}, {match: yes, verdict: vDrop, text: "drop"}}},
	}
	b, u, _ = rs.blocks("input")
	require.False(t, b)
	require.True(t, u)
	b, u, _ = ruleset{}.blocks("missing")
	require.False(t, b)
	require.False(t, u)

	require.Equal(t, no, and(yes, no))
	require.Equal(t, maybe, and(yes, maybe))
	require.Equal(t, yes, not(no))
	require.Equal(t, maybe, not(maybe))
}

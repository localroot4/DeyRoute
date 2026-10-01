package hysteria2

import (
	"encoding/json"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
)

// yamlWriter builds a small YAML document with deterministic key order.
// Every scalar string is written double-quoted with JSON escaping, which is
// valid YAML.
type yamlWriter struct{ b strings.Builder }

func quote(s string) string {
	q, _ := json.Marshal(s) // a string never fails to marshal
	return string(q)
}

func (w *yamlWriter) line(indent int, s string) {
	w.b.WriteString(strings.Repeat("  ", indent) + s + "\n")
}
func (w *yamlWriter) comment(s string)             { w.line(0, "# "+s) }
func (w *yamlWriter) section(indent int, k string) { w.line(indent, k+":") }
func (w *yamlWriter) str(indent int, k, v string)  { w.line(indent, k+": "+quote(v)) }
func (w *yamlWriter) boolean(indent int, k string, v bool) {
	w.line(indent, k+": "+strconv.FormatBool(v))
}
func (w *yamlWriter) bytes() []byte { return []byte(w.b.String()) }

func header(w *yamlWriter, in backend.RenderInput, side backend.Side) {
	w.comment("Rendered by deyroute for tunnel " + in.Tunnel.ID + ", node " + in.Node.ID +
		", transport " + in.Transport.ID() + " (" + side.String() + " side).")
	w.comment("Regenerated from /etc/deyroute/config.yaml on every apply; do not edit.")
}

// obfs writes the Salamander block shared by both sides.
func obfs(w *yamlWriter, p planned) {
	w.section(0, "obfs")
	w.str(1, "type", "salamander")
	w.section(1, "salamander")
	w.str(2, "password", p.obfs)
}

// aclRules returns the server ACL: one "direct(<host>, <proto>/<port>)"
// rule per distinct target of the tunnel, then "reject(all)". Only the exact
// target pairs are reachable through the node, which is stricter than the
// 127.0.0.0/8 bound of spec section 11 (see docs/backends/hysteria2.md).
//
// Rules for IP-literal targets carry the same IP as hijack address
// ("direct(127.0.0.1, tcp/443, 127.0.0.1)"). Hysteria resolves host-name
// requests before the ACL and an IP rule also matches a name whose A or
// AAAA record is that IP; without the hijack the direct outbound would then
// dial every resolved address (happy eyeballs), so a name with
// A=127.0.0.1 and AAAA=<any host> would reach <any host>:<port>. The hijack
// rewrites the destination to exactly the allowed IP.
func aclRules(maps []mapping) []string {
	seen := map[string]bool{}
	var rules []string
	for _, m := range maps {
		hijack := ""
		if ip := net.ParseIP(m.host); ip != nil {
			hijack = ", " + ip.String()
		}
		r := "direct(" + m.host + ", " + m.proto + "/" + strconv.Itoa(m.port) + hijack + ")"
		if !seen[r] {
			seen[r] = true
			rules = append(rules, r)
		}
	}
	sort.Strings(rules)
	return append(rules, "reject(all)")
}

func hasUDP(p planned) bool {
	for _, m := range p.maps {
		if m.proto == config.ProtoUDP {
			return true
		}
	}
	return false
}

// renderServer renders the node side (server.yaml).
func renderServer(in backend.RenderInput, p planned) backend.Rendered {
	var w yamlWriter
	header(&w, in, backend.SideNode)
	w.str(0, "listen", ":"+strconv.Itoa(in.ControlPort))
	w.section(0, "tls")
	w.str(1, "cert", filepath.Join(in.Paths.ConfigDir, NodeCertFile))
	w.str(1, "key", filepath.Join(in.Paths.ConfigDir, NodeKeyFile))
	// The client pins the certificate hash and may use the hub IP or domain
	// as SNI; the SNI guard would reject names outside the node cert.
	w.str(1, "sniGuard", "disable")
	w.section(0, "auth")
	w.str(1, "type", "password")
	w.str(1, "password", in.Secrets.Token)
	obfs(&w, p)
	// No UDP port maps: refuse UDP relaying entirely.
	w.boolean(0, "disableUDP", !hasUDP(p))
	w.boolean(0, "speedTest", false)
	w.section(0, "acl")
	w.section(1, "inline")
	for _, r := range aclRules(p.maps) {
		w.line(2, "- "+quote(r))
	}
	w.section(0, "masquerade")
	w.str(1, "type", "404")

	r := backend.Rendered{
		Files: map[string][]byte{
			ServerFile:   w.bytes(),
			NodeCertFile: []byte(in.Secrets.Keys[KeyNodeCert]),
			NodeKeyFile:  []byte(in.Secrets.Keys[KeyNodeKey]),
		},
		Unit:  unit(in, "server", ServerFile),
		Binds: []backend.PortUse{{Port: in.ControlPort, Proto: config.ProtoUDP, Addr: "0.0.0.0", Purpose: "control"}},
	}
	if p.hopping {
		// Port hopping: every UDP port of the range lands on <ctl>.
		r.NAT = []backend.NATRule{{Proto: config.ProtoUDP, DportLow: HopLow, DportHigh: HopHigh, ToPort: in.ControlPort}}
	}
	return r
}

// renderClient renders the hub side (client.yaml) that binds the user ports.
func renderClient(in backend.RenderInput, p planned) backend.Rendered {
	var w yamlWriter
	header(&w, in, backend.SideHub)
	server := backend.HostPort(in.Node.PublicIP, in.ControlPort)
	if p.hopping {
		// "host:20000-20999" selects hysteria's UDP port hopping client.
		server = bracket(in.Node.PublicIP) + ":" + strconv.Itoa(HopLow) + "-" + strconv.Itoa(HopHigh)
	}
	w.str(0, "server", server)
	w.str(0, "auth", in.Secrets.Token)
	obfs(&w, p)
	w.section(0, "tls")
	if in.Secrets.ServerName != "" {
		w.str(1, "sni", in.Secrets.ServerName)
	}
	// The node serves its own self-signed certificate; the client accepts
	// exactly that pinned leaf (hysteria checks the hash in
	// VerifyPeerCertificate when insecure is set).
	w.boolean(1, "insecure", true)
	w.str(1, "pinSHA256", strings.ToLower(in.Secrets.Keys[KeyNodeCertSHA256]))
	w.section(0, "bandwidth")
	w.str(1, "up", strconv.Itoa(p.upMbps)+" mbps")
	w.str(1, "down", strconv.Itoa(p.downMbps)+" mbps")
	w.boolean(0, "fastOpen", true)

	var binds []backend.PortUse
	var tcpMaps, udpMaps []mapping
	for _, m := range p.maps {
		if m.proto == config.ProtoUDP {
			udpMaps = append(udpMaps, m)
		} else {
			tcpMaps = append(tcpMaps, m)
		}
		binds = append(binds, backend.PortUse{Port: m.listen, Proto: m.proto, Addr: p.listenAddr, Purpose: "user"})
	}
	if len(tcpMaps) > 0 {
		w.section(0, "tcpForwarding")
		for _, m := range tcpMaps {
			w.line(1, "- listen: "+quote(backend.HostPort(p.listenAddr, m.listen)))
			w.line(1, "  remote: "+quote(m.target()))
		}
	}
	if len(udpMaps) > 0 {
		w.section(0, "udpForwarding")
		for _, m := range udpMaps {
			w.line(1, "- listen: "+quote(backend.HostPort(p.listenAddr, m.listen)))
			w.line(1, "  remote: "+quote(m.target()))
		}
	}
	return backend.Rendered{
		Files: map[string][]byte{ClientFile: w.bytes()},
		Unit:  unit(in, "client", ClientFile),
		Binds: binds,
	}
}

// bracket wraps IPv6 literals in brackets for "host:port" strings.
func bracket(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

// unit is "hysteria <server|client> -c <file> --disable-update-check"
// (app/cmd/root.go of the pinned version; the update check would contact
// api.hy2.io on every start).
func unit(in backend.RenderInput, mode, file string) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{in.Paths.Binary, mode, "-c", filepath.Join(in.Paths.ConfigDir, file), "--disable-update-check"},
		WorkingDirectory: in.Paths.ConfigDir,
	}
}

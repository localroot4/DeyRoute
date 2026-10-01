package frp

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
)

// tomlWriter builds frp's TOML with deterministic key order. Top-level
// (dotted) keys are written before the first [[proxies]] table.
type tomlWriter struct{ b strings.Builder }

func (w *tomlWriter) comment(s string)    { w.b.WriteString("# " + s + "\n") }
func (w *tomlWriter) blank()              { w.b.WriteString("\n") }
func (w *tomlWriter) str(k, v string)     { w.b.WriteString(k + " = " + backend.TOMLString(v) + "\n") }
func (w *tomlWriter) num(k string, v int) { w.b.WriteString(k + " = " + strconv.Itoa(v) + "\n") }
func (w *tomlWriter) boolean(k string, v bool) {
	w.b.WriteString(k + " = " + strconv.FormatBool(v) + "\n")
}
func (w *tomlWriter) bytes() []byte { return []byte(w.b.String()) }

// header writes the provenance comment shared by both sides.
func header(w *tomlWriter, in backend.RenderInput, side backend.Side) {
	w.comment("Rendered by deyroute for tunnel " + commentSafe(in.Tunnel.ID) + ", node " + commentSafe(in.Node.ID) +
		", transport " + commentSafe(in.Transport.ID()) + " (" + side.String() + " side).")
	w.comment("Regenerated from /etc/deyroute/config.yaml on every apply; do not edit.")
}

// commentSafe keeps s from ending the comment line (TOML injection) or
// forming a template action: frp runs the whole file, comments included,
// through text/template before parsing it. Config ids are [a-z0-9-], so
// this only matters for corrupted input.
func commentSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '{' || r == '}' {
			return '?'
		}
		return r
	}, s)
}

// hubHost is the hub address frpc dials, without IPv6 brackets.
func hubHost(in backend.RenderInput) string {
	return strings.Trim(strings.TrimSpace(in.Hub.PublicIP), "[]")
}

// logKeys sends frp's log to stdout, which the unit appends to the tunnel
// log file; colors are disabled because the output is not a terminal.
func logKeys(w *tomlWriter) {
	w.str("log.to", "console")
	w.str("log.level", logLevel)
	w.boolean("log.disablePrintColor", true)
}

// allowPorts renders frps allowPorts: exactly the tunnel's listen ports
// (each once, in port-map order), so a client holding the token cannot
// make frps bind any other port on the hub.
func allowPorts(proxies []proxy) string {
	seen := map[int]bool{}
	var b strings.Builder
	b.WriteString("[\n")
	for _, p := range proxies {
		if seen[p.listen] {
			continue
		}
		seen[p.listen] = true
		b.WriteString("  { single = " + strconv.Itoa(p.listen) + " },\n")
	}
	b.WriteString("]")
	return b.String()
}

// renderServer renders the hub side: frps.toml, unit and binds.
func renderServer(in backend.RenderInput, sp spec, proxies []proxy) backend.Rendered {
	addr := listenAddr(in)

	var w tomlWriter
	header(&w, in, backend.SideHub)
	w.str("bindAddr", "0.0.0.0")
	w.num("bindPort", in.ControlPort)
	if sp.udp != noUDP {
		w.num(string(sp.udp), in.ControlPort)
	}
	w.str("proxyBindAddr", addr)
	w.b.WriteString("allowPorts = " + allowPorts(proxies) + "\n")
	logKeys(&w)
	w.str("auth.method", "token")
	w.str("auth.token", in.Secrets.Token)
	w.num("transport.maxPoolCount", maxPoolCount)
	w.boolean("transport.tcpMux", true)
	w.boolean("transport.tls.force", true)
	w.str("transport.tls.certFile", in.Secrets.TLSCertFile)
	w.str("transport.tls.keyFile", in.Secrets.TLSKeyFile)

	// frps always listens on bindPort/tcp (control and websocket);
	// quic/kcp add a UDP listener on the same port number.
	binds := []backend.PortUse{{Port: in.ControlPort, Proto: config.ProtoTCP, Addr: "0.0.0.0", Purpose: "control"}}
	if sp.udp != noUDP {
		binds = append(binds, backend.PortUse{Port: in.ControlPort, Proto: config.ProtoUDP, Addr: "0.0.0.0", Purpose: "control"})
	}
	for _, p := range proxies {
		binds = append(binds, backend.PortUse{Port: p.listen, Proto: p.proto, Addr: addr, Purpose: "user"})
	}

	return backend.Rendered{
		Files: map[string][]byte{ServerFile: w.bytes()},
		Unit:  unit(in, ServerBinary, ServerFile),
		Binds: binds,
	}
}

// renderClient renders the node side: frpc.toml and unit. frpc binds
// nothing (its admin webServer stays disabled); it dials the hub and the
// fixed localIP:localPort of each proxy.
func renderClient(in backend.RenderInput, sp spec, proxies []proxy) backend.Rendered {
	var w tomlWriter
	header(&w, in, backend.SideNode)
	// serverAddr takes a bare host, also for IPv6 (frpc joins host:port).
	w.str("serverAddr", hubHost(in))
	w.num("serverPort", in.ControlPort)
	w.boolean("loginFailExit", false)
	logKeys(&w)
	w.str("auth.method", "token")
	w.str("auth.token", in.Secrets.Token)
	w.str("transport.protocol", sp.name)
	w.num("transport.poolCount", poolCount(in))
	w.boolean("transport.tcpMux", true)
	w.boolean("transport.tls.enable", true)
	// A standard TLS ClientHello, not frp's 0x17 marker byte.
	w.boolean("transport.tls.disableCustomTLSFirstByte", true)
	w.str("transport.tls.trustedCaFile", trustAnchor(in))
	if in.Secrets.ServerName != "" {
		w.str("transport.tls.serverName", in.Secrets.ServerName)
	}

	for _, p := range proxies {
		w.blank()
		w.b.WriteString("[[proxies]]\n")
		w.str("name", p.name)
		w.str("type", p.proto)
		w.str("localIP", p.localIP)
		w.num("localPort", p.localPort)
		w.num("remotePort", p.listen)
	}

	return backend.Rendered{
		Files: map[string][]byte{ClientFile: w.bytes()},
		Unit:  unit(in, ClientBinary, ClientFile),
	}
}

// unit is the deyroute-tun@ drop-in data: "<BinDir>/frps -c frps.toml" or
// "<BinDir>/frpc -c frpc.toml" (strict config parsing is the default of
// both binaries in v0.71.0, so an unknown key fails the start).
func unit(in backend.RenderInput, binary, file string) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{filepath.Join(in.Paths.BinDir, binary), "-c", filepath.Join(in.Paths.ConfigDir, file)},
		WorkingDirectory: in.Paths.ConfigDir,
	}
}

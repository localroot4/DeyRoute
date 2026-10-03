package backhaul

import (
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/exec"
)

// tomlWriter builds a flat TOML table with deterministic key order.
type tomlWriter struct{ b strings.Builder }

func (w *tomlWriter) comment(s string)    { w.b.WriteString("# " + s + "\n") }
func (w *tomlWriter) table(name string)   { w.b.WriteString("[" + name + "]\n") }
func (w *tomlWriter) str(k, v string)     { w.b.WriteString(k + " = " + backend.TOMLString(v) + "\n") }
func (w *tomlWriter) num(k string, v int) { w.b.WriteString(k + " = " + strconv.Itoa(v) + "\n") }
func (w *tomlWriter) boolean(k string, v bool) {
	w.b.WriteString(k + " = " + strconv.FormatBool(v) + "\n")
}

func (w *tomlWriter) strList(k string, items []string) {
	if len(items) == 0 {
		w.b.WriteString(k + " = []\n")
		return
	}
	w.b.WriteString(k + " = [\n")
	for _, it := range items {
		w.b.WriteString("  " + backend.TOMLString(it) + ",\n")
	}
	w.b.WriteString("]\n")
}

func (w *tomlWriter) bytes() []byte { return []byte(w.b.String()) }

// header writes the provenance comment shared by both sides.
func header(w *tomlWriter, in backend.RenderInput, side backend.Side) {
	w.comment("Rendered by deyroute for tunnel " + in.Tunnel.ID + ", node " + in.Node.ID +
		", transport " + in.Transport.ID() + " (" + side.String() + " side).")
	w.comment("Regenerated from /etc/deyroute/config.yaml on every apply; do not edit.")
}

// acceptsUDP reports whether the server needs accept_udp (UDP port maps
// carried over the tcp transport).
func acceptsUDP(sp spec, maps []mapping) bool {
	if !sp.acceptUDP {
		return false
	}
	for _, m := range maps {
		if m.udp {
			return true
		}
	}
	return false
}

// portEntry renders one Backhaul "ports" entry. On the default listen
// address the spec's short form "443=127.0.0.1:443" is used (Backhaul then
// listens on ":443"); a specific address uses "addr:port=target". Backhaul
// v0.7.2 only accepts the short form for 1 < port < 65535, so the two edge
// ports use ":<port>=target", which it passes to net.Listen unchanged.
func portEntry(addr string, m mapping) string {
	if addr == "" || addr == "0.0.0.0" {
		if m.listen > 1 && m.listen < 65535 {
			return strconv.Itoa(m.listen) + "=" + m.target
		}
		return ":" + strconv.Itoa(m.listen) + "=" + m.target
	}
	return backend.HostPort(addr, m.listen) + "=" + m.target
}

// controlBindHost is the address the server's control port listens on: every
// IPv4 address (spec sample "0.0.0.0:<ctl>"), or every address when the node
// clients dial the hub over IPv6 (a 0.0.0.0 socket would not accept them).
func controlBindHost(hubIP string) string {
	if ip := net.ParseIP(strings.TrimSpace(hubIP)); ip != nil && ip.To4() == nil {
		return "::"
	}
	return "0.0.0.0"
}

// renderServer renders the hub side: the server config (file), unit and binds.
func renderServer(in backend.RenderInput, sp spec, maps []mapping, file string) backend.Rendered {
	addr := listenAddr(in)
	udpOverTCP := acceptsUDP(sp, maps)

	var w tomlWriter
	header(&w, in, backend.SideHub)
	w.table("server")
	ctlHost := controlBindHost(in.Hub.PublicIP)
	w.str("bind_addr", backend.HostPort(ctlHost, in.ControlPort))
	w.str("transport", sp.name)
	if sp.acceptUDP {
		w.boolean("accept_udp", udpOverTCP)
	}
	w.str("token", in.Secrets.Token)
	if !sp.udpData {
		w.num("keepalive_period", keepalivePeriod)
		w.boolean("nodelay", true)
	}
	w.num("heartbeat", heartbeat)
	w.num("channel_size", tierChannelSize(in.Tier(backend.SideHub)))
	if sp.mux {
		w.num("mux_con", muxCon)
		w.num("mux_version", muxVersion)
		w.num("mux_framesize", muxFrameSize)
		w.num("mux_recievebuffer", tierReceiveBuffer(in.Tier(backend.SideHub)))
		w.num("mux_streambuffer", muxStreamBuffer)
	}
	w.boolean("sniffer", false)
	w.num("web_port", 0)
	if sp.tls {
		w.str("tls_cert", in.Secrets.TLSCertFile)
		w.str("tls_key", in.Secrets.TLSKeyFile)
	}
	w.str("log_level", logLevel)
	// deyroute owns kernel tuning (spec section 12); Backhaul must not run
	// its own "sysctl -w" calls at start.
	w.boolean("skip_optz", true)
	entries := make([]string, 0, len(maps))
	for _, m := range maps {
		entries = append(entries, portEntry(addr, m))
	}
	w.strList("ports", entries)

	binds := []backend.PortUse{{Port: in.ControlPort, Proto: config.ProtoTCP, Addr: ctlHost, Purpose: "control"}}
	if sp.udpData {
		binds = append(binds, backend.PortUse{Port: in.ControlPort, Proto: config.ProtoUDP, Addr: ctlHost, Purpose: "control"})
	}
	for _, m := range maps {
		if !sp.udpData {
			// Every entry of a TCP-family server starts a TCP listener, and
			// with accept_udp also a UDP listener on the same port (plan
			// guarantees both protocols are mapped then).
			binds = append(binds, backend.PortUse{Port: m.listen, Proto: config.ProtoTCP, Addr: addr, Purpose: "user"})
		}
		if sp.udpData || udpOverTCP {
			binds = append(binds, backend.PortUse{Port: m.listen, Proto: config.ProtoUDP, Addr: addr, Purpose: "user"})
		}
	}

	return backend.Rendered{
		Files: map[string][]byte{file: w.bytes()},
		Unit:  unit(in, file),
		Binds: binds,
	}
}

// renderClient renders the node side: the client config (file) and unit.
// The client binds nothing (web_port 0); it dials the hub and the targets
// the server names for each stream.
func renderClient(in backend.RenderInput, sp spec, maps []mapping, file string) backend.Rendered {
	var w tomlWriter
	header(&w, in, backend.SideNode)
	w.table("client")
	w.str("remote_addr", backend.HostPort(in.Hub.PublicIP, in.ControlPort))
	w.str("transport", sp.name)
	w.str("token", in.Secrets.Token)
	w.num("connection_pool", connectionPool(in, len(maps)))
	w.boolean("aggressive_pool", false)
	if !sp.udpData {
		w.num("keepalive_period", keepalivePeriod)
	}
	w.num("dial_timeout", dialTimeout)
	if !sp.udpData {
		w.boolean("nodelay", true)
	}
	w.num("retry_interval", retryInterval)
	if sp.mux {
		w.num("mux_version", muxVersion)
		w.num("mux_framesize", muxFrameSize)
		w.num("mux_recievebuffer", tierReceiveBuffer(in.Tier(backend.SideNode)))
		w.num("mux_streambuffer", muxStreamBuffer)
	}
	w.boolean("sniffer", false)
	w.num("web_port", 0)
	w.str("log_level", logLevel)
	w.boolean("skip_optz", true)

	return backend.Rendered{
		Files: map[string][]byte{file: w.bytes()},
		Unit:  unit(in, file),
	}
}

// tierChannelSize is the server's channel_size for the hub's backend tier:
// 1024/2048/4096 for small/medium/large, channelSize without a tier.
func tierChannelSize(tier string) int {
	return backend.ByTier(tier, channelSize, channelSize/2, channelSize, channelSize*2)
}

// tierReceiveBuffer is mux_recievebuffer for the backend tier of the side
// that runs the process: 2/4/8 MiB for small/medium/large,
// muxReceiveBuffer without a tier.
func tierReceiveBuffer(tier string) int {
	return backend.ByTier(tier, muxReceiveBuffer, muxReceiveBuffer/2, muxReceiveBuffer, muxReceiveBuffer*2)
}

// connectionPool is advanced.connection_pool, or max(8, number of ports)
// (spec 7.1: "based on the number of ports, default 8").
func connectionPool(in backend.RenderInput, ports int) int {
	if adv := in.Tunnel.Advanced; adv != nil && adv.ConnectionPool > 0 {
		return adv.ConnectionPool
	}
	if ports > minPool {
		return ports
	}
	return minPool
}

// unit is the deyroute-tun@ drop-in data: "backhaul -c <file>" (the only
// flag besides -v in v0.7.2).
func unit(in backend.RenderInput, file string) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{in.Paths.Binary, "-c", filepath.Join(in.Paths.ConfigDir, file)},
		WorkingDirectory: in.Paths.ConfigDir,
	}
}

// pairUnit runs the main and the UDP companion process in one unit:
// "deyroute pair <backhaul> -c <file> -- <backhaul> -c <udpFile>". deyroute
// stops both when one of them exits, so systemd restarts the pair.
func pairUnit(in backend.RenderInput, file, udpFile string) backend.UnitSpec {
	u := unit(in, file)
	u.ExecStart = []string{in.Paths.SelfBinary, exec.PairCommand,
		in.Paths.Binary, "-c", filepath.Join(in.Paths.ConfigDir, file), exec.PairSeparator,
		in.Paths.Binary, "-c", filepath.Join(in.Paths.ConfigDir, udpFile)}
	return u
}

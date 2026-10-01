package direct

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// HAProxy settings (spec 7.8: mode tcp, option tcp-check, send-proxy).
const (
	haproxyMaxConn      = 65536
	haproxyCheckInter   = "5s"
	checkDialTimeoutS   = 3 // node-side reachability check, per address
	haproxyFinTimeout   = "30s"
	haproxyCheckTimeout = "5s"
)

// haproxyAddr formats host:port the way HAProxy parses it (the port follows
// the last colon; IPv6 literals are not bracketed).
func haproxyAddr(host string, port int) string {
	return host + ":" + strconv.Itoa(port)
}

// renderHAProxy renders the hub side: haproxy.cfg with one frontend and
// one backend per port map, the unit and the binds.
func renderHAProxy(in backend.RenderInput, ports []portMap) backend.Rendered {
	addr := listenAddr(in)
	nodeIP := strings.TrimSpace(in.Node.PublicIP)
	proxy := in.Tunnel.Advanced != nil && in.Tunnel.Advanced.ProxyProtocol
	idle := strconv.Itoa(int(DefaultIdleTimeout.Minutes())) + "m"
	connect := strconv.Itoa(int(DefaultDialTimeout.Seconds())) + "s"

	var b strings.Builder
	b.WriteString(header(in, backend.SideHub))
	b.WriteString("global\n")
	b.WriteString("    maxconn " + strconv.Itoa(haproxyMaxConn) + "\n\n")
	b.WriteString("defaults\n")
	b.WriteString("    mode tcp\n")
	b.WriteString("    timeout connect " + connect + "\n")
	b.WriteString("    timeout client " + idle + "\n")
	b.WriteString("    timeout server " + idle + "\n")
	b.WriteString("    timeout client-fin " + haproxyFinTimeout + "\n")
	b.WriteString("    timeout server-fin " + haproxyFinTimeout + "\n")
	b.WriteString("    timeout check " + haproxyCheckTimeout + "\n")

	binds := make([]backend.PortUse, 0, len(ports))
	for _, p := range ports {
		name := backend.ServiceName(p.proto, p.listen)
		bind := haproxyAddr(addr, p.listen)
		if ipv6Any(addr) {
			bind += " v4v6"
		}
		server := "    server " + in.Node.ID + " " + haproxyAddr(nodeIP, p.port) +
			" check inter " + haproxyCheckInter + " fall 3 rise 2"
		if proxy {
			// PROXY protocol v1 so the node service sees the client IP; the
			// health check sends it too so the service never sees a bare
			// connection.
			server += " send-proxy check-send-proxy"
		}
		b.WriteString("\nfrontend " + name + "\n")
		b.WriteString("    bind " + bind + "\n")
		b.WriteString("    default_backend " + name + "\n")
		b.WriteString("\nbackend " + name + "\n")
		b.WriteString("    option tcp-check\n")
		b.WriteString(server + "\n")
		binds = append(binds, backend.PortUse{Port: p.listen, Proto: config.ProtoTCP, Addr: addr, Purpose: "user"})
	}

	cfg := filepath.Join(in.Paths.ConfigDir, HAProxyFile)
	return backend.Rendered{
		Files: map[string][]byte{HAProxyFile: []byte(b.String())},
		Unit: backend.UnitSpec{
			// Refuse to start with a config HAProxy rejects; -db keeps it in
			// the foreground under systemd.
			ExecStartPre:     [][]string{{in.Paths.Binary, "-c", "-f", cfg}},
			ExecStart:        []string{in.Paths.Binary, "-f", cfg, "-db"},
			WorkingDirectory: in.Paths.ConfigDir,
		},
		Binds: binds,
	}
}

func ipv6Any(addr string) bool { return addr == "::" || addr == "[::]" }

// renderCheck renders the node side of direct/haproxy. Nothing is relayed
// on the node (HAProxy connects to the service directly), so the unit is a
// oneshot "deyroute relay" in check role: it verifies that every target
// answers on a non-loopback address and exits, failing the start with
// DEY-B062 otherwise.
func renderCheck(in backend.RenderInput, ports []portMap) (backend.Rendered, error) {
	cfg := RelayConfig{
		Version:      RelayConfigVersion,
		Tunnel:       in.Tunnel.ID,
		Role:         RoleCheck,
		NodeIP:       strings.TrimSpace(in.Node.PublicIP),
		DialTimeoutS: checkDialTimeoutS,
	}
	for _, p := range ports {
		cfg.Ports = append(cfg.Ports, RelayPort{Index: p.index, Proto: p.proto, Target: p.target})
	}
	data, err := backend.JSONIndent(cfg)
	if err != nil {
		return backend.Rendered{}, deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": Name, "transport": HAProxy})
	}
	u := relayUnit(in)
	u.Type = "oneshot"
	u.RemainAfterExit = true
	// checkCandidates lists the local addresses (net.InterfaceAddrs), which
	// needs a netlink route socket; the shared template allows only
	// AF_INET, AF_INET6 and AF_UNIX.
	u.AddressFamilies = []string{"AF_NETLINK"}
	return backend.Rendered{
		Files: map[string][]byte{RelayConfigFile: data},
		Unit:  u,
	}, nil
}

package chisel

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// listenAddr is the hub address user ports bind to, as chisel spells it in
// a remote (IPv6 bracketed); the canary never binds a public address.
func listenAddr(in backend.RenderInput) string {
	addr := in.ListenAddrOrDefault()
	if in.Canary && !isLoopback(addr) {
		addr = "127.0.0.1"
	}
	if strings.Contains(addr, ":") && !strings.HasPrefix(addr, "[") {
		addr = "[" + addr + "]"
	}
	return addr
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

// bareAddr strips IPv6 brackets for PortUse.Addr.
func bareAddr(addr string) string { return strings.Trim(addr, "[]") }

// remote returns the client remote of m: the hub server listens on
// <addr>:<listen> and the node dials the target.
func remote(addr string, m portMap) string {
	r := "R:" + addr + ":" + strconv.Itoa(m.listen) + ":" + m.targetHost + ":" + strconv.Itoa(m.targetPort)
	if m.proto == config.ProtoUDP {
		r += "/udp"
	}
	return r
}

// userAddr is the address chisel's server matches authfile patterns
// against for a reverse remote: "R:<local-host>:<local-port>" without the
// protocol (share/settings.Remote.UserAddr at v1.12.1).
func userAddr(addr string, listen int) string {
	return "R:" + addr + ":" + strconv.Itoa(listen)
}

// renderHub renders users.json, server.key, the server unit and the binds.
// The authfile grants the tunnel user exactly the tunnel's reverse remotes
// (anchored patterns; chisel v1.12.1 does not anchor them itself): no
// forward remotes, no SOCKS, no other port on the hub.
func renderHub(in backend.RenderInput, maps []portMap) (backend.Rendered, error) {
	addr := listenAddr(in)
	var patterns []string
	seen := map[string]bool{}
	for _, m := range maps {
		p := "^" + regexp.QuoteMeta(userAddr(addr, m.listen)) + "$"
		if !seen[p] {
			seen[p] = true
			patterns = append(patterns, p)
		}
	}
	users, err := backend.JSONIndent(map[string][]string{User + ":" + Password(in.Secrets.Token): patterns})
	if err != nil {
		return backend.Rendered{}, deyerr.Wrap(deyerr.B002, err, deyerr.Params{"backend": Name, "transport": WSS})
	}
	dir := in.Paths.ConfigDir
	binds := []backend.PortUse{{Port: in.ControlPort, Proto: config.ProtoTCP, Addr: "0.0.0.0", Purpose: "control"}}
	for _, m := range maps {
		binds = append(binds, backend.PortUse{Port: m.listen, Proto: m.proto, Addr: bareAddr(addr), Purpose: "user"})
	}
	return backend.Rendered{
		Files: map[string][]byte{
			UsersFile: users,
			KeyFile:   []byte(in.Secrets.Keys[KeyServerKey]),
		},
		Unit: backend.UnitSpec{
			ExecStart: []string{
				in.Paths.Binary, "server",
				"--host", "0.0.0.0",
				"--port", strconv.Itoa(in.ControlPort),
				"--reverse",
				"--keyfile", filepath.Join(dir, KeyFile),
				"--authfile", filepath.Join(dir, UsersFile),
				"--tls-key", in.Secrets.TLSKeyFile,
				"--tls-cert", in.Secrets.TLSCertFile,
				"--keepalive", keepalive,
			},
			WorkingDirectory: dir,
		},
		Binds: binds,
	}, nil
}

// renderNode renders the client unit. It pins the hub twice: the TLS chain
// against the tunnel CA (--tls-ca, plus --sni when a server name is set)
// and the SSH host key (--fingerprint). The credentials travel in AUTH.
func renderNode(in backend.RenderInput, maps []portMap) backend.Rendered {
	s := in.Secrets
	argv := []string{
		in.Paths.Binary, "client",
		"--fingerprint", s.Keys[KeyFingerprint],
		"--tls-ca", s.CAFile,
	}
	if s.ServerName != "" {
		argv = append(argv, "--sni", s.ServerName)
	}
	argv = append(argv,
		"--keepalive", keepalive,
		"--max-retry-interval", maxRetryInterval,
		"https://"+backend.HostPort(in.Hub.PublicIP, in.ControlPort),
	)
	addr := listenAddr(in)
	for _, m := range maps {
		argv = append(argv, remote(addr, m))
	}
	return backend.Rendered{
		Files: map[string][]byte{},
		Unit: backend.UnitSpec{
			ExecStart:        argv,
			WorkingDirectory: in.Paths.ConfigDir,
			Env:              map[string]string{"AUTH": User + ":" + Password(s.Token)},
		},
	}
}

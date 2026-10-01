package rathole

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
)

// tomlWriter builds TOML with deterministic key order. Callers write every
// plain key of a table before its sub-tables, as TOML requires.
type tomlWriter struct {
	b      strings.Builder
	tables int
}

func (w *tomlWriter) comment(s string)    { w.b.WriteString("# " + s + "\n") }
func (w *tomlWriter) str(k, v string)     { w.b.WriteString(k + " = " + backend.TOMLString(v) + "\n") }
func (w *tomlWriter) num(k string, v int) { w.b.WriteString(k + " = " + strconv.Itoa(v) + "\n") }

// table starts [name], separated from the previous table by a blank line.
func (w *tomlWriter) table(name string) {
	if w.tables > 0 {
		w.b.WriteString("\n")
	}
	w.tables++
	w.b.WriteString("[" + name + "]\n")
}

func (w *tomlWriter) bytes() []byte { return []byte(w.b.String()) }

// header writes the provenance comment shared by both sides.
func header(w *tomlWriter, in backend.RenderInput, side backend.Side) {
	w.comment("Rendered by deyroute for tunnel " + commentSafe(in.Tunnel.ID) + ", node " + commentSafe(in.Node.ID) +
		", transport " + commentSafe(in.Transport.ID()) + " (" + side.String() + " side).")
	w.comment("Regenerated from /etc/deyroute/config.yaml on every apply; do not edit.")
}

// commentSafe keeps s from ending the comment line (TOML injection). Config
// ids are [a-z0-9-], so this only matters for corrupted input.
func commentSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

// renderServer renders the hub side: server.toml, unit and binds.
func renderServer(in backend.RenderInput, sp spec, svcs []service) backend.Rendered {
	addr := listenAddr(in)

	var w tomlWriter
	header(&w, in, backend.SideHub)
	w.table("server")
	w.str("bind_addr", backend.HostPort("0.0.0.0", in.ControlPort))
	w.str("default_token", in.Secrets.Token)
	w.num("heartbeat_interval", heartbeatInterval)

	w.table("server.transport")
	w.str("type", sp.name)
	switch {
	case sp.noise:
		w.table("server.transport.noise")
		w.str("pattern", NoisePattern)
		w.str("local_private_key", in.Secrets.Keys[KeyNoisePrivate])
	case sp.tls:
		w.table("server.transport.tls")
		w.str("pkcs12", in.Secrets.TLSP12File)
		w.str("pkcs12_password", in.Secrets.TLSP12Password)
	}

	binds := []backend.PortUse{{Port: in.ControlPort, Proto: config.ProtoTCP, Addr: "0.0.0.0", Purpose: "control"}}
	for _, s := range svcs {
		w.table("server.services." + s.name)
		w.str("type", s.proto)
		w.str("bind_addr", backend.HostPort(addr, s.listen))
		binds = append(binds, backend.PortUse{Port: s.listen, Proto: s.proto, Addr: addr, Purpose: "user"})
	}

	return backend.Rendered{
		Files: map[string][]byte{ServerFile: w.bytes()},
		Unit:  unit(in, "--server", ServerFile),
		Binds: binds,
	}
}

// renderClient renders the node side: client.toml and unit. The client
// binds nothing; it dials the hub and the fixed local_addr of each service.
func renderClient(in backend.RenderInput, sp spec, svcs []service) backend.Rendered {
	var w tomlWriter
	header(&w, in, backend.SideNode)
	w.table("client")
	w.str("remote_addr", backend.HostPort(hubHost(in), in.ControlPort))
	w.str("default_token", in.Secrets.Token)
	w.num("heartbeat_timeout", heartbeatTimeout)
	w.num("retry_interval", retryInterval)

	w.table("client.transport")
	w.str("type", sp.name)
	switch {
	case sp.noise:
		w.table("client.transport.noise")
		w.str("pattern", NoisePattern)
		w.str("remote_public_key", in.Secrets.Keys[KeyNoisePublic])
	case sp.tls:
		w.table("client.transport.tls")
		w.str("trusted_root", trustedRoot(in))
		if in.Secrets.ServerName != "" {
			w.str("hostname", in.Secrets.ServerName)
		}
	}

	for _, s := range svcs {
		w.table("client.services." + s.name)
		w.str("type", s.proto)
		w.str("local_addr", s.target)
	}

	return backend.Rendered{
		Files: map[string][]byte{ClientFile: w.bytes()},
		Unit:  unit(in, "--client", ClientFile),
	}
}

// unit is the deyroute-tun@ drop-in data: "rathole --server|--client <file>"
// (explicit mode flags of v0.5.0 src/cli.rs).
func unit(in backend.RenderInput, mode, file string) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{in.Paths.Binary, mode, filepath.Join(in.Paths.ConfigDir, file)},
		WorkingDirectory: in.Paths.ConfigDir,
		Env:              map[string]string{"RUST_LOG": "info"},
		DropHardening: map[string]string{
			// The upstream v0.5.0 release workflow packs the Linux binaries
			// with UPX; the UPX loader unpacks the code into anonymous
			// memory and mprotect()s it executable, which
			// MemoryDenyWriteExecute forbids (the process would be killed
			// by seccomp at start).
			"MemoryDenyWriteExecute": "release binaries are UPX-packed; the UPX loader maps the unpacked code executable at start",
		},
	}
}

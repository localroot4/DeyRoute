package node

import (
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// privAgent is enough of an agent for checkCommands.
func privAgent() *agent {
	return &agent{o: Options{SelfBinary: config.BinaryPath}, nodeID: testNode}
}

// privDir is the config directory of transport b/tr of tunnel main on this
// node, as render.ConfigDir names it.
func privDir(b, tr string) string { return render.ConfigDir(b, "main", testNode, tr) }

// plainUnit is an unprivileged unit of backend b: "<b> -c <dir>/config"
// from b's install directory, the shape of the xray, frp, backhaul …
// renderers.
func plainUnit(b, dir string) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{config.BinDir + "/" + b + "/v1.0.0/" + b, "-c", dir + "/config"},
		WorkingDirectory: dir,
	}
}

// wgKernelUnit is the node side of wireguard/kernel as the renderer writes
// it when the node DNATs to 127.0.0.1 (wireguard/render.go unit).
func wgKernelUnit(dir string) backend.UnitSpec {
	cfg := dir + "/wg.json"
	return backend.UnitSpec{
		Type: "oneshot", RemainAfterExit: true, WorkingDirectory: dir,
		ExecStart:       []string{config.BinaryPath, "wg", "up", "--config", cfg},
		ExecStop:        [][]string{{config.BinaryPath, "wg", "down", "--config", cfg}},
		RunAsRoot:       true,
		ExtraCaps:       []string{"CAP_NET_ADMIN"},
		AddressFamilies: []string{"AF_NETLINK"},
		DropHardening:   map[string]string{"ProtectKernelTunables": "deyroute wg up sets route_localnet"},
	}
}

// awgUnit is awg/userspace as the renderer writes it.
func awgUnit(dir string) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{config.BinDir + "/awg/v0.2.12/amneziawg-go", "-f", "dey-main"},
		WorkingDirectory: dir,
		Env:              map[string]string{"LOG_LEVEL": "error"},
		RunAsRoot:        true,
		ExtraCaps:        []string{"CAP_NET_ADMIN"},
		AddressFamilies:  []string{"AF_NETLINK"},
		ReadWritePaths:   []string{"-/run/amneziawg"},
	}
}

// ratholeUnit is a rathole client as the renderer writes it.
func ratholeUnit(dir string) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{config.BinDir + "/rathole/v0.5.0/rathole", "--client", dir + "/client.toml"},
		WorkingDirectory: dir,
		Env:              map[string]string{"RUST_LOG": "info"},
		DropHardening:    map[string]string{"MemoryDenyWriteExecute": "release binaries are UPX-packed"},
	}
}

// relayUnit is the deyroute relay of direct/native; checkUnit is the node
// side of direct/haproxy (the relay in check role, direct/haproxy.go
// renderCheck).
func relayUnit(dir string) backend.UnitSpec {
	return backend.UnitSpec{
		ExecStart:        []string{config.BinaryPath, "relay", "--tunnel", "main", "--config", dir + "/relay.json"},
		WorkingDirectory: dir,
	}
}

func checkUnit(dir string) backend.UnitSpec {
	u := relayUnit(dir)
	u.Type, u.RemainAfterExit = "oneshot", true
	u.AddressFamilies = []string{"AF_NETLINK"}
	return u
}

// requireRefused asserts that err is the node's refusal (DEY-N050) and that
// its reason names every part of want.
func requireRefused(t *testing.T, err error, want ...string) {
	t.Helper()
	e := requireCode(t, err, deyerr.N050)
	for _, w := range want {
		require.Contains(t, e.Why(), w)
	}
}

// withDrop returns u with option added to its DropHardening.
func withDrop(u backend.UnitSpec, option string) backend.UnitSpec {
	d := maps.Clone(u.DropHardening)
	if d == nil {
		d = map[string]string{}
	}
	d[option] = "the hub says so"
	u.DropHardening = d
	return u
}

// privilegedUnits are the units of every row of the allowlist plus an
// unprivileged one, keyed by backend/transport.
func privilegedUnits() map[string]backend.UnitSpec {
	return map[string]backend.UnitSpec{
		"wireguard/kernel": wgKernelUnit(privDir("wireguard", "kernel")),
		"awg/userspace":    awgUnit(privDir("awg", "userspace")),
		"rathole/noise":    ratholeUnit(privDir("rathole", "noise")),
		"direct/haproxy":   checkUnit(privDir("direct", "haproxy")),
		"xray/reality":     plainUnit("xray", privDir("xray", "reality")),
	}
}

// checkPriv runs checkCommands for unit u of transport id (backend/name).
func checkPriv(a *agent, id string, u backend.UnitSpec) error {
	b, tr, _ := strings.Cut(id, "/")
	return a.checkCommands(api.CmdBackendRender, b, privDir(b, tr), u)
}

func TestCheckCommandsRejectsRunAsRootForXray(t *testing.T) {
	a := privAgent()
	dir := privDir("xray", "reality")
	u := plainUnit("xray", dir)
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "xray", dir, u))

	u.RunAsRoot = true
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "xray", dir, u), "backend xray/reality may not run as root", "RunAsRoot")
	// Root alone is refused too, without any extra capability: User=root
	// keeps the template's bounding set but owns every file root owns.
	for _, b := range []string{"backhaul", "frp", "gost", "chisel", "hysteria2", "waterwall", "rathole"} {
		d := privDir(b, "tcp")
		root := plainUnit(b, d)
		root.RunAsRoot = true
		requireRefused(t, a.checkCommands(api.CmdBackendRender, b, d, root), "backend "+b+"/tcp may not run as root")
	}
}

func TestCheckCommandsRejectsDropCapabilityBoundingSet(t *testing.T) {
	a := privAgent()
	for id, u := range privilegedUnits() {
		require.NoError(t, checkPriv(a, id, u), id)
		// "~": every capability. No row may relax it, root or not.
		requireRefused(t, checkPriv(a, id, withDrop(u, "CapabilityBoundingSet")), `may not drop "CapabilityBoundingSet" (DropHardening)`)
	}
}

func TestCheckCommandsRejectsDropNoNewPrivileges(t *testing.T) {
	a := privAgent()
	for id, u := range privilegedUnits() {
		require.NoError(t, checkPriv(a, id, u), id)
		requireRefused(t, checkPriv(a, id, withDrop(u, "NoNewPrivileges")), `may not drop "NoNewPrivileges" (DropHardening)`)
	}
}

func TestCheckCommandsRejectsExtraCapsForFrp(t *testing.T) {
	a := privAgent()
	dir := privDir("frp", "tcp")
	u := backend.UnitSpec{ExecStart: []string{config.BinDir + "/frp/v0.71.0/frpc", "-c", dir + "/frpc.toml"}, WorkingDirectory: dir}
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "frp", dir, u))
	// The template's own capability adds nothing.
	u.ExtraCaps = []string{"CAP_NET_BIND_SERVICE"}
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "frp", dir, u))

	for _, c := range []string{"CAP_NET_ADMIN", "CAP_SYS_ADMIN", "CAP_DAC_OVERRIDE"} {
		u.ExtraCaps = []string{"CAP_NET_BIND_SERVICE", c}
		requireRefused(t, a.checkCommands(api.CmdBackendRender, "frp", dir, u), "backend frp/tcp may not add the capability", c, "ExtraCaps")
	}
}

func TestCheckCommandsAllowsWireguardRoot(t *testing.T) {
	a := privAgent()
	wg := privDir("wireguard", "kernel")
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "wireguard", wg, wgKernelUnit(wg)))
	awg := privDir("awg", "userspace")
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "awg", awg, awgUnit(awg)))
	// A canary directory names no transport: the backend's row applies.
	can := render.CanaryConfigDir("wireguard", "main")
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "wireguard", can, wgKernelUnit(can)))

	// Root with CAP_NET_ADMIN, nothing more.
	more := wgKernelUnit(wg)
	more.ExtraCaps = []string{"CAP_NET_ADMIN", "CAP_SYS_MODULE"}
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "wireguard", wg, more), "backend wireguard/kernel may not add the capability", "CAP_SYS_MODULE")
	fam := wgKernelUnit(wg)
	fam.AddressFamilies = []string{"AF_NETLINK", "AF_PACKET"}
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "wireguard", wg, fam), "AF_PACKET", "AddressFamilies")
	// awg keeps ProtectKernelTunables: only the kernel flavour sets
	// route_localnet.
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "awg", awg, withDrop(awgUnit(awg), "ProtectKernelTunables")),
		`backend awg/userspace may not drop "ProtectKernelTunables"`)
}

func TestCheckCommandsAllowsRatholeMDWE(t *testing.T) {
	a := privAgent()
	for _, tr := range []string{"noise", "tcp", "tls"} {
		dir := privDir("rathole", tr)
		require.NoError(t, a.checkCommands(api.CmdBackendRender, "rathole", dir, ratholeUnit(dir)), tr)
		// MemoryDenyWriteExecute is the only option rathole relaxes.
		requireRefused(t, a.checkCommands(api.CmdBackendRender, "rathole", dir, withDrop(ratholeUnit(dir), "ProtectSystem")),
			"backend rathole/"+tr+` may not drop "ProtectSystem"`)
	}
	// Other backends keep MemoryDenyWriteExecute.
	for _, b := range []string{"backhaul", "xray", "gost", "waterwall"} {
		dir := privDir(b, "tcp")
		requireRefused(t, a.checkCommands(api.CmdBackendRender, b, dir, withDrop(plainUnit(b, dir), "MemoryDenyWriteExecute")),
			"backend "+b+`/tcp may not drop "MemoryDenyWriteExecute" (DropHardening)`)
	}
}

func TestCheckCommandsAllowsDirectHAProxyNetlink(t *testing.T) {
	a := privAgent()
	hap := privDir("direct", "haproxy")
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "direct", hap, checkUnit(hap)))
	native := privDir("direct", "native")
	require.NoError(t, a.checkCommands(api.CmdBackendRender, "direct", native, relayUnit(native)))

	// The row is per transport: the relay of direct/native has no netlink
	// socket, and neither has the canary (its directory names no
	// transport; direct/haproxy is never a canary since its targets cannot
	// be loopback).
	nl := relayUnit(native)
	nl.AddressFamilies = []string{"AF_NETLINK"}
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "direct", native, nl),
		"backend direct/native may not add the address family", "AF_NETLINK", "AddressFamilies")
	can := render.CanaryConfigDir("direct", "main")
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "direct", can, checkUnit(can)), "backend direct may not add the address family")
	// AF_NETLINK and nothing else.
	pk := checkUnit(hap)
	pk.AddressFamilies = []string{"AF_NETLINK", "AF_PACKET"}
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "direct", hap, pk), "AF_PACKET")
}

func TestCheckCommandsRejectsReadWritePathsForXray(t *testing.T) {
	a := privAgent()
	dir := privDir("xray", "reality")
	for _, p := range []string{"/etc", "/", "-/run/amneziawg", "/etc/deyroute/backends"} {
		u := plainUnit("xray", dir)
		u.ReadWritePaths = []string{p}
		requireRefused(t, a.checkCommands(api.CmdBackendRender, "xray", dir, u), "backend xray/reality may not make", "ReadWritePaths")
	}
	// awg may write its UAPI socket directory, and only that.
	awg := privDir("awg", "userspace")
	u := awgUnit(awg)
	u.ReadWritePaths = []string{"-/run/amneziawg", "/etc"}
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "awg", awg, u), `"/etc"`, "ReadWritePaths")
	u.ReadWritePaths = []string{"/run/amneziawg"} // without "-": another entry
	requireRefused(t, a.checkCommands(api.CmdBackendRender, "awg", awg, u), "ReadWritePaths")
}

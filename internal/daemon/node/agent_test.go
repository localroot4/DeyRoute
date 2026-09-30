package node

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/health"
	"github.com/localroot4/deyroute/internal/systemd"
	"github.com/localroot4/deyroute/internal/version"
)

const backhaulBin = "/var/lib/deyroute/bin/backhaul/v0.7.2/backhaul"

// renderArgs is a node-side backend.render payload like render.NodePayload
// produces it.
func renderArgs(tunnel, b, tr string) api.BackendRenderArgs {
	dir := "/etc/deyroute/backends/" + b + "/" + tunnel + "/" + testNode + "/" + tr
	return api.BackendRenderArgs{
		Instance:  systemd.InstanceName(tunnel, testNode, b+"/"+tr),
		Tunnel:    tunnel,
		ConfigDir: dir,
		Files: map[string][]byte{
			"client.toml": []byte("[client]\nremote_addr = \"5.6.7.8:30001\"\n"),
			"ca.crt":      []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"),
		},
		Unit: backend.UnitSpec{ExecStart: []string{backhaulBin, "-c", dir + "/client.toml"}},
	}
}

func unitOf(args api.BackendRenderArgs) string { return systemd.UnitName(args.Instance) }

func TestRunRefusesHubConfigAndMissingConfig(t *testing.T) {
	root := t.TempDir()
	err := Run(context.Background(), Options{Root: root, Runner: newFakeSys().runner()})
	requireCode(t, err, deyerr.C014)

	cfg := config.NewHub("ir-1", "5.6.7.8", 44433)
	require.NoError(t, os.MkdirAll(filepath.Join(root, config.EtcDir), 0o700))
	require.NoError(t, config.Save(filepath.Join(root, config.DefaultPath), cfg))
	err = Run(context.Background(), Options{Root: root, Runner: newFakeSys().runner()})
	e := requireCode(t, err, deyerr.X009)
	require.Contains(t, e.Message(), "needs a node")
}

func TestHelloAndHeartbeat(t *testing.T) {
	e := newEnv(t)
	unit := "deyroute-tun@main.de-1.backhaul-wssmux.service"
	e.sys.setState(unit, "active")
	s := e.start()
	require.Equal(t, testNode, s.NodeID)
	require.Equal(t, version.Version, s.Hello.Version)
	require.Equal(t, "amd64", s.Hello.Arch)
	require.Equal(t, "6.1.0-test", s.Hello.Kernel)
	require.Equal(t, "Debian GNU/Linux 12 (bookworm)", s.Hello.OS)

	eventually(t, func() bool {
		select {
		case hb := <-s.Heartbeats():
			return hb.Units[unit] == "active" && hb.RAMBytes == 2048*1024+1000000 && !hb.At.IsZero()
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, "heartbeat with units and RAM")
}

func TestRenderStartStopRemove(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	args := renderArgs("main", "backhaul", "wssmux")
	_, err := call[json.RawMessage](t, s, api.CmdBackendRender, args)
	require.NoError(t, err)

	dir := e.path(args.ConfigDir)
	data, err := os.ReadFile(filepath.Join(dir, "client.toml"))
	require.NoError(t, err)
	require.Equal(t, string(args.Files["client.toml"]), string(data))
	fi, err := os.Stat(filepath.Join(dir, "client.toml"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), fi.Mode().Perm())
	di, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o750), di.Mode().Perm())
	dropIn, err := os.ReadFile(e.path("/etc/systemd/system/" + unitOf(args) + ".d/10-deyroute.conf"))
	require.NoError(t, err)
	require.Contains(t, string(dropIn), backhaulBin)
	require.Contains(t, string(dropIn), "append:/var/log/deyroute/tunnels/main.log")
	reloads := e.runner.Count("systemctl daemon-reload")
	require.Equal(t, 1, reloads)
	_, err = os.Stat(e.path(systemd.TunnelLogDir))
	require.NoError(t, err)

	// Rendering the same payload again changes nothing: no reload.
	_, err = call[json.RawMessage](t, s, api.CmdBackendRender, args)
	require.NoError(t, err)
	require.Equal(t, reloads, e.runner.Count("systemctl daemon-reload"))

	// A file that is no longer rendered disappears.
	args2 := args
	args2.Files = map[string][]byte{"client.toml": args.Files["client.toml"]}
	_, err = call[json.RawMessage](t, s, api.CmdBackendRender, args2)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, "ca.crt"))
	require.True(t, os.IsNotExist(err))

	st, err := call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	require.Equal(t, "active", st.ActiveState)
	require.Equal(t, unitOf(args), st.Unit)
	require.Positive(t, st.MainPID)
	require.Contains(t, e.hooks.list(), "pre backhaul "+args.ConfigDir)

	st, err = call[api.UnitStatus](t, s, api.CmdUnitStatus, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	require.Equal(t, "active", st.ActiveState)
	require.Empty(t, st.LogTail)

	st, err = call[api.UnitStatus](t, s, api.CmdUnitRestart, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	require.Equal(t, "active", st.ActiveState)

	st, err = call[api.UnitStatus](t, s, api.CmdUnitStop, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	require.Equal(t, "inactive", st.ActiveState)
	require.Contains(t, e.hooks.list(), "stop backhaul "+args.ConfigDir)
	// No NAT anywhere: the node table was never touched by these units.
	require.Empty(t, e.sys.nftScripts())

	_, err = call[json.RawMessage](t, s, api.CmdBackendRemove, api.BackendRemoveArgs{Instance: args.Instance, ConfigDir: args.ConfigDir})
	require.NoError(t, err)
	_, err = os.Stat(dir)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(e.path("/etc/systemd/system/" + unitOf(args) + ".d"))
	require.True(t, os.IsNotExist(err))
	// Stopping a unit systemd does not know is fine.
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStop, api.UnitArgs{Instance: "gone.de-1.backhaul-tcp"})
	require.NoError(t, err)
}

func TestRenderRejectsUnsafePayloads(t *testing.T) {
	e := newEnv(t)
	// The node's public address (a WireGuard target may be the node's own
	// public IP).
	e.opts.LocalAddrs = func() ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
	}
	s := e.start()
	base := renderArgs("main", "backhaul", "wssmux")
	mod := func(f func(a *api.BackendRenderArgs)) api.BackendRenderArgs {
		a := base
		a.Files = map[string][]byte{}
		for k, v := range base.Files {
			a.Files[k] = v
		}
		a.Unit.ExecStart = append([]string(nil), base.Unit.ExecStart...)
		f(&a)
		return a
	}
	many := map[string][]byte{}
	for i := 0; i <= MaxRenderFiles; i++ {
		many[fmt.Sprintf("f%d", i)] = []byte("x")
	}
	cases := map[string]api.BackendRenderArgs{
		"dir traversal":      mod(func(a *api.BackendRenderArgs) { a.ConfigDir = "/etc/deyroute/backends/../../../etc/cron.d/x" }),
		"dir outside":        mod(func(a *api.BackendRenderArgs) { a.ConfigDir = "/tmp/backhaul/main/de-1/wssmux" }),
		"dir relative":       mod(func(a *api.BackendRenderArgs) { a.ConfigDir = "etc/deyroute/backends/backhaul/main/de-1/wssmux" }),
		"dir other tunnel":   mod(func(a *api.BackendRenderArgs) { a.ConfigDir = "/etc/deyroute/backends/backhaul/other/de-1/wssmux" }),
		"dir other rung":     mod(func(a *api.BackendRenderArgs) { a.ConfigDir = "/etc/deyroute/backends/backhaul/main/de-1/tcpmux" }),
		"file dotdot":        mod(func(a *api.BackendRenderArgs) { a.Files["../../../../etc/passwd"] = []byte("x") }),
		"file absolute":      mod(func(a *api.BackendRenderArgs) { a.Files["/etc/passwd"] = []byte("x") }),
		"file hidden up":     mod(func(a *api.BackendRenderArgs) { a.Files["a/../../b"] = []byte("x") }),
		"other node":         mod(func(a *api.BackendRenderArgs) { a.Instance = "main.nl-1.backhaul-wssmux" }),
		"bad instance":       mod(func(a *api.BackendRenderArgs) { a.Instance = "../x" }),
		"bad tunnel":         mod(func(a *api.BackendRenderArgs) { a.Tunnel = "Main!" }),
		"tunnel mismatch":    mod(func(a *api.BackendRenderArgs) { a.Tunnel = "other" }),
		"shell":              mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{"/bin/sh", "-c", "id"} }),
		"relative binary":    mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{"backhaul"} }),
		"sneaky binary":      mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{"/var/lib/deyroute/bin/../../../bin/sh"} }),
		"pre command":        mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStartPre = [][]string{{"/usr/bin/curl", "x"}} }),
		"no exec":            mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = nil }),
		"too many files":     mod(func(a *api.BackendRenderArgs) { a.Files = many }),
		"file too large":     mod(func(a *api.BackendRenderArgs) { a.Files["big"] = make([]byte, MaxRenderFileBytes+1) }),
		"bad NAT":            mod(func(a *api.BackendRenderArgs) { a.NAT = []backend.NATRule{{Proto: "tcp", DportLow: 0, ToPort: 1}} }),
		"bad unit settings":  mod(func(a *api.BackendRenderArgs) { a.Unit.Env = map[string]string{"BAD KEY": "x"} }),
		"working dir":        mod(func(a *api.BackendRenderArgs) { a.Unit.WorkingDirectory = "/root" }),
		"deyroute uninstall": mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{config.BinaryPath, "uninstall", "--yes"} }),
		"deyroute bare":      mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{config.BinaryPath} }),
		"deyroute stop cmd":  mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStop = [][]string{{config.BinaryPath, "restore", "x"}} }),
		"previous binary":    mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{config.PrevBinaryPath, "relay"} }),
		"other backend bin":  mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{"/var/lib/deyroute/bin/xray/v1/xray"} }),
		"bin dir file":       mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{"/var/lib/deyroute/bin/backhaul/backhaul"} }),
		"hidden bin":         mod(func(a *api.BackendRenderArgs) { a.Unit.ExecStart = []string{"/var/lib/deyroute/bin/backhaul/v1/.x"} }),
		"LD_PRELOAD":         mod(func(a *api.BackendRenderArgs) { a.Unit.Env = map[string]string{"LD_PRELOAD": base.ConfigDir + "/x.so"} }),
		"GLIBC_TUNABLES":     mod(func(a *api.BackendRenderArgs) { a.Unit.Env = map[string]string{"GLIBC_TUNABLES": "glibc.x=1"} }),
		"NAT hijacks SSH": mod(func(a *api.BackendRenderArgs) {
			a.NAT = []backend.NATRule{{Proto: "tcp", DportLow: 1, DportHigh: 1000, ToAddr: "127.0.0.1", ToPort: 2222}}
		}),
		"NAT to another host": mod(func(a *api.BackendRenderArgs) {
			a.NAT = []backend.NATRule{{Proto: "tcp", DportLow: 443, DportHigh: 443, ToAddr: "8.8.8.8", ToPort: 443, Iface: "dey-main"}}
		}),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := call[json.RawMessage](t, s, api.CmdBackendRender, args)
			e := requireCode(t, err, deyerr.N050)
			require.Contains(t, e.Message(), api.CmdBackendRender)
		})
	}
	_, err := os.Stat(e.path("/etc/passwd"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(e.path("/etc/cron.d"))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(e.path(base.ConfigDir))
	require.True(t, os.IsNotExist(err), "nothing may be written for a refused payload")
	require.Zero(t, e.runner.Count("systemctl daemon-reload"))

	// What the backends really render is accepted: the deyroute relay and
	// WireGuard setup, harmless environment, SSH-port NAT on a tunnel
	// interface only.
	ok := map[string]api.BackendRenderArgs{
		"relay": mod(func(a *api.BackendRenderArgs) {
			a.Unit.ExecStart = []string{config.BinaryPath, "relay", "--tunnel", "main", "--config", a.ConfigDir + "/relay.json"}
		}),
		"wg": mod(func(a *api.BackendRenderArgs) {
			a.Unit = backend.UnitSpec{Type: "oneshot", RemainAfterExit: true, RunAsRoot: true,
				ExecStart: []string{config.BinaryPath, "wg", "up", "--config", a.ConfigDir + "/wg.json"},
				ExecStop:  [][]string{{config.BinaryPath, "wg", "down", "--config", a.ConfigDir + "/wg.json"}}}
			a.NAT = []backend.NATRule{
				{Proto: "tcp", DportLow: 22, DportHigh: 22, ToAddr: "127.0.0.1", ToPort: 22, Iface: "dey-main"},
				{Proto: "tcp", DportLow: 8443, DportHigh: 8443, ToAddr: "203.0.113.7", ToPort: 8443, Iface: "dey-main"},
			}
		}),
		"env": mod(func(a *api.BackendRenderArgs) { a.Unit.Env = map[string]string{"RUST_LOG": "info"} }),
	}
	for name, args := range ok {
		_, err := call[json.RawMessage](t, s, api.CmdBackendRender, args)
		require.NoError(t, err, name)
	}
}

func TestStartFailureReportsLogTail(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	args := renderArgs("main", "backhaul", "wssmux")
	_, err := call[json.RawMessage](t, s, api.CmdBackendRender, args)
	require.NoError(t, err)
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, "log line "+strconv.Itoa(i))
	}
	lines = append(lines, "config error: token = \"c2VjcmV0LXRva2VuLXZhbHVlLTEyMzQ1Njc4OTBhYmNkZWY\"")
	writeFile(t, e.path(systemd.TunnelLogFile("main")), strings.Join(lines, "\n")+"\n", 0o600)

	e.sys.mu.Lock()
	e.sys.failStart[unitOf(args)] = true
	e.sys.mu.Unlock()
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: args.Instance})
	de := requireCode(t, err, deyerr.B003)
	require.Contains(t, de.Detail, "log line 50")
	require.NotContains(t, de.Detail, "log line 11\n")
	require.Len(t, strings.Split(de.Detail, "\n"), systemd.FailureLogLines)
	require.NotContains(t, de.Detail, "c2VjcmV0LXRva2VuLXZhbHVlLTEyMzQ1Njc4OTBhYmNkZWY")

	// The unit starts but dies at once.
	e.sys.mu.Lock()
	e.sys.failStart[unitOf(args)] = false
	e.sys.crash[unitOf(args)] = true
	e.sys.mu.Unlock()
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: args.Instance})
	requireCode(t, err, deyerr.B003)
	st, err := call[api.UnitStatus](t, s, api.CmdUnitStatus, api.UnitArgs{Instance: args.Instance})
	require.NoError(t, err)
	require.Equal(t, "failed", st.ActiveState)
	require.Len(t, st.LogTail, systemd.FailureLogLines)
	require.Contains(t, e.hooks.list(), "pre backhaul "+args.ConfigDir)
}

// natArgs renders an instance with node-side NAT (WireGuard-like).
func natArgs(b, tr, iface string, port int) api.BackendRenderArgs {
	a := renderArgs("main", b, tr)
	cfgFile := a.ConfigDir + "/wg.json"
	a.Files = map[string][]byte{"wg.json": []byte(`{"mode":"kernel"}`)}
	if b == "awg" {
		a.Unit = backend.UnitSpec{ExecStart: []string{"/var/lib/deyroute/bin/awg/v1.0.4/amneziawg-go", "-f", iface}, RunAsRoot: true,
			ExtraCaps: []string{"CAP_NET_ADMIN"}, AddressFamilies: []string{"AF_NETLINK"}}
	} else {
		a.Unit = backend.UnitSpec{Type: "oneshot", RemainAfterExit: true, RunAsRoot: true,
			ExecStart: []string{config.BinaryPath, "wg", "up", "--config", cfgFile},
			ExecStop:  [][]string{{config.BinaryPath, "wg", "down", "--config", cfgFile}}}
	}
	a.NAT = []backend.NATRule{{Proto: "tcp", DportLow: port, DportHigh: port, ToAddr: "127.0.0.1", ToPort: port, Iface: iface}}
	return a
}

func TestNATFollowsStartedInstances(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	eventually(t, func() bool { return e.sys.deletes() >= 1 }, "the node table is brought in line at start")
	startDeletes := e.sys.deletes()

	wg := natArgs("wireguard", "kernel", "dey-main", 443)
	awg := natArgs("awg", "userspace", "deyc-1", 2053)
	for _, a := range []api.BackendRenderArgs{wg, awg} {
		_, err := call[json.RawMessage](t, s, api.CmdBackendRender, a)
		require.NoError(t, err)
	}
	require.Empty(t, e.sys.nftScripts(), "warm instances have no NAT")

	_, err := call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: wg.Instance})
	require.NoError(t, err)
	scripts := e.sys.nftScripts()
	require.Len(t, scripts, 1)
	require.Contains(t, scripts[0], "dnat ip to 127.0.0.1:443")
	require.NotContains(t, scripts[0], "2053")
	require.NotContains(t, scripts[0], "chain input", "the node table never filters ports")

	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: awg.Instance})
	require.NoError(t, err)
	scripts = e.sys.nftScripts()
	require.Len(t, scripts, 2)
	require.Contains(t, scripts[1], "127.0.0.1:443")
	require.Contains(t, scripts[1], "127.0.0.1:2053")
	calls := e.hooks.list()
	require.Contains(t, calls, "pre awg "+awg.ConfigDir)
	require.Contains(t, calls, "post awg "+awg.ConfigDir)
	require.NotContains(t, calls, "post wireguard "+wg.ConfigDir)

	// Restarting a started instance does not touch the table.
	_, err = call[api.UnitStatus](t, s, api.CmdUnitRestart, api.UnitArgs{Instance: awg.Instance})
	require.NoError(t, err)
	require.Len(t, e.sys.nftScripts(), 2)

	// Re-rendering a started instance with other NAT updates the table.
	awg2 := awg
	awg2.NAT = []backend.NATRule{{Proto: "udp", DportLow: 2053, DportHigh: 2053, ToAddr: "127.0.0.1", ToPort: 2053, Iface: "deyc-1"}}
	_, err = call[json.RawMessage](t, s, api.CmdBackendRender, awg2)
	require.NoError(t, err)
	scripts = e.sys.nftScripts()
	require.Len(t, scripts, 3)
	require.Contains(t, scripts[2], "udp dport 2053")

	_, err = call[api.UnitStatus](t, s, api.CmdUnitStop, api.UnitArgs{Instance: wg.Instance})
	require.NoError(t, err)
	scripts = e.sys.nftScripts()
	require.Len(t, scripts, 4)
	require.NotContains(t, scripts[3], "127.0.0.1:443")
	require.Contains(t, e.hooks.list(), "stop wireguard "+wg.ConfigDir)

	_, err = call[api.UnitStatus](t, s, api.CmdUnitStop, api.UnitArgs{Instance: awg.Instance})
	require.NoError(t, err)
	require.Equal(t, startDeletes+1, e.sys.deletes(), "an empty NAT set removes the table")

	// Hub-supplied NAT (Hysteria2 port hopping) through firewall.apply.
	hop := api.NodeFirewallArgs{NAT: []backend.NATRule{{Proto: "udp", DportLow: 20000, DportHigh: 20999, ToPort: 30123}}}
	_, err = call[json.RawMessage](t, s, api.CmdNodeFirewall, hop)
	require.NoError(t, err)
	scripts = e.sys.nftScripts()
	require.Contains(t, scripts[len(scripts)-1], "redirect to :30123")
	_, err = call[json.RawMessage](t, s, api.CmdNodeFirewall, api.NodeFirewallArgs{})
	require.NoError(t, err)
	require.Equal(t, startDeletes+2, e.sys.deletes())
	_, err = call[json.RawMessage](t, s, api.CmdNodeFirewall, api.NodeFirewallArgs{NAT: []backend.NATRule{{Proto: "icmp"}}})
	requireCode(t, err, deyerr.N050)
	// The hub can never redirect the node's SSH port.
	nft := len(e.sys.nftScripts())
	_, err = call[json.RawMessage](t, s, api.CmdNodeFirewall, api.NodeFirewallArgs{NAT: []backend.NATRule{{Proto: "tcp", DportLow: 22, ToPort: 30001}}})
	requireCode(t, err, deyerr.N050)
	require.Len(t, e.sys.nftScripts(), nft)

	// State survives in the state file.
	data, err := os.ReadFile(e.path(StateFile))
	require.NoError(t, err)
	require.Contains(t, string(data), wg.Instance)
	fi, err := os.Stat(e.path(StateFile))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

func TestProbesAndPortCheck(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer tlsSrv.Close()
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer httpSrv.Close()

	r, err := call[api.ProbeResultDTO](t, s, api.CmdProbeTCP, api.ProbeArgs{Target: ln.Addr().String(), TimeoutMs: 2000})
	require.NoError(t, err)
	require.True(t, r.OK, r.Error)
	r, err = call[api.ProbeResultDTO](t, s, api.CmdProbeTLS, api.ProbeArgs{Target: tlsSrv.Listener.Addr().String()})
	require.NoError(t, err)
	require.True(t, r.OK, r.Error)
	r, err = call[api.ProbeResultDTO](t, s, api.CmdProbeHTTP, api.ProbeArgs{Target: httpSrv.Listener.Addr().String(), Path: "/x"})
	require.NoError(t, err)
	require.True(t, r.OK, r.Error)
	r, err = call[api.ProbeResultDTO](t, s, api.CmdProbeHTTP, api.ProbeArgs{Target: tlsSrv.URL + "/health"})
	require.NoError(t, err)
	require.True(t, r.OK, r.Error)

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closedAddr := closed.Addr().String()
	require.NoError(t, closed.Close())
	r, err = call[api.ProbeResultDTO](t, s, api.CmdProbeTCP, api.ProbeArgs{Target: closedAddr, TimeoutMs: 1000})
	require.NoError(t, err)
	require.False(t, r.OK)
	require.Equal(t, health.ReasonRefused, r.Error)

	_, err = call[api.ProbeResultDTO](t, s, api.CmdProbeTCP, api.ProbeArgs{Target: "no-port"})
	requireCode(t, err, deyerr.N050)

	host, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	p, err := strconv.Atoi(port)
	require.NoError(t, err)
	r, err = call[api.ProbeResultDTO](t, s, api.CmdPortCheckRemote, api.PortCheckArgs{IP: host, Port: p, Proto: "tcp", TimeoutMs: 2000})
	require.NoError(t, err)
	require.True(t, r.OK, r.Error)
	r, err = call[api.ProbeResultDTO](t, s, api.CmdPortCheckRemote, api.PortCheckArgs{IP: host, Port: p, Proto: "udp"})
	require.NoError(t, err)
	require.False(t, r.OK)
	require.Contains(t, r.Error, "not supported")
	_, err = call[api.ProbeResultDTO](t, s, api.CmdPortCheckRemote, api.PortCheckArgs{IP: "example.com", Port: p})
	requireCode(t, err, deyerr.N050)
	_, err = call[api.ProbeResultDTO](t, s, api.CmdPortCheckRemote, api.PortCheckArgs{IP: host, Port: 70000})
	requireCode(t, err, deyerr.N050)
	_, err = call[api.ProbeResultDTO](t, s, api.CmdPortCheckRemote, api.PortCheckArgs{IP: host, Port: p, Proto: "sctp"})
	requireCode(t, err, deyerr.N050)
}

// freePort returns a port that was free a moment ago.
func freePort(t *testing.T, network string) int {
	t.Helper()
	if network == "udp" {
		c, err := net.ListenPacket("udp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = c.Close() }()
		return c.LocalAddr().(*net.UDPAddr).Port
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestProbeHelpers(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	ctx := context.Background()

	// UDP echo (section 10 UDP probe).
	up := freePort(t, "udp")
	_, err := call[json.RawMessage](t, s, api.CmdProbeUDPListen, api.UDPListenArgs{Port: up, Seconds: 30})
	require.NoError(t, err)
	res := health.UDPEcho(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(up)), 3, time.Second)
	require.True(t, res.OK, res.Err)
	_, err = call[json.RawMessage](t, s, api.CmdProbeUDPListen, api.UDPListenArgs{Port: up, Seconds: 30})
	require.NoError(t, err, "asking again extends the running echo")
	_, err = call[json.RawMessage](t, s, api.CmdProbeUDPListen, api.UDPListenArgs{Port: 0})
	requireCode(t, err, deyerr.N050)

	// Canary loopback echo.
	echo, err := call[api.EchoResult](t, s, api.CmdEchoStart, api.EchoArgs{})
	require.NoError(t, err)
	require.Positive(t, echo.Port)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(echo.Port))
	res = health.TCPEcho(ctx, addr, 2*time.Second)
	require.True(t, res.OK, res.Err)
	again, err := call[api.EchoResult](t, s, api.CmdEchoStart, api.EchoArgs(echo))
	require.NoError(t, err)
	require.Equal(t, echo.Port, again.Port)
	_, err = call[json.RawMessage](t, s, api.CmdEchoStop, api.EchoArgs(echo))
	require.NoError(t, err)
	res = health.TCPEcho(ctx, addr, time.Second)
	require.False(t, res.OK)
	_, err = call[json.RawMessage](t, s, api.CmdEchoStop, api.EchoArgs(echo))
	require.NoError(t, err, "stopping twice is fine")
	_, err = call[json.RawMessage](t, s, api.CmdEchoStop, api.EchoArgs{Port: -1})
	requireCode(t, err, deyerr.N050)

	// A port someone else holds.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = busy.Close() }()
	_, err = call[api.EchoResult](t, s, api.CmdEchoStart, api.EchoArgs{Port: busy.Addr().(*net.TCPAddr).Port})
	requireCode(t, err, deyerr.P012)

	// Speed generator.
	sp := freePort(t, "tcp")
	_, err = call[json.RawMessage](t, s, api.CmdSpeedServe, api.SpeedServeArgs{Port: sp, Seconds: 1})
	require.NoError(t, err)
	_, err = call[json.RawMessage](t, s, api.CmdSpeedServe, api.SpeedServeArgs{Port: sp, Seconds: 1})
	require.NoError(t, err)
	sr, err := health.MeasureSpeed(ctx, net.JoinHostPort("127.0.0.1", strconv.Itoa(sp)), 1)
	require.NoError(t, err)
	require.Positive(t, sr.DownloadBytes)
	_, err = call[json.RawMessage](t, s, api.CmdSpeedServe, api.SpeedServeArgs{Port: 0})
	requireCode(t, err, deyerr.N050)
}

func TestFetchProxy(t *testing.T) {
	payload := []byte("backend archive bytes")
	sum := sha256.Sum256(payload)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	e := newEnv(t)
	e.opts.HTTPClient = srv.Client()
	s := e.start()

	res, err := call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: srv.URL + "/backhaul.tar.gz", SHA256: hex.EncodeToString(sum[:]), UploadID: "up-1"})
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), res.Bytes)
	require.Equal(t, hex.EncodeToString(sum[:]), res.SHA256)
	got, ok := e.hub.upload("up-1")
	require.True(t, ok)
	require.Equal(t, payload, got)

	// Without a sha256 (a signed file the hub verifies itself).
	res, err = call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: srv.URL + "/SHA256SUMS", UploadID: "up-2", MaxBytes: 1024})
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(sum[:]), res.SHA256)
	got, _ = e.hub.upload("up-2")
	require.Equal(t, payload, got)

	_, err = call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: srv.URL + "/x", SHA256: strings.Repeat("ab", 32), UploadID: "up-3"})
	requireCode(t, err, deyerr.S001)
	_, ok = e.hub.upload("up-3")
	require.False(t, ok, "unverified bytes are never uploaded")

	_, err = call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: srv.URL + "/missing", SHA256: hex.EncodeToString(sum[:]), UploadID: "up-4"})
	de := requireCode(t, err, deyerr.N051)
	require.Contains(t, de.Detail, "404")

	_, err = call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: strings.Replace(srv.URL, "https", "http", 1) + "/x", UploadID: "up-5"})
	requireCode(t, err, deyerr.N050)
	_, err = call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: "file:///etc/passwd", UploadID: "up-6"})
	requireCode(t, err, deyerr.N050)
	_, err = call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: srv.URL + "/x", SHA256: "zz", UploadID: "up-7"})
	requireCode(t, err, deyerr.N050)
	_, err = call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: srv.URL + "/x", UploadID: "up-8", MaxBytes: 4})
	requireCode(t, err, deyerr.N051)
}

// telegramClient sends every request to srv whatever the URL host.
func telegramClient(srv *httptest.Server) *http.Client {
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig.ServerName = "example.com"
	return &http.Client{Transport: tr}
}

func TestHTTPPostOnlyTelegram(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b := make([]byte, 100)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	e := newEnv(t)
	e.opts.HTTPClient = telegramClient(srv)
	s := e.start()

	token := "123456789:AAH4dQx9JtestTokenValue_abcdefghijk"
	res, err := call[api.HTTPPostResult](t, s, api.CmdHTTPPost, api.HTTPPostArgs{
		URL: "https://api.telegram.org/bot" + token + "/sendMessage", ContentType: "application/json", Body: []byte(`{"text":"hi"}`)})
	require.NoError(t, err)
	require.Equal(t, 200, res.Status)
	require.Equal(t, `{"ok":true}`, res.Body)
	require.Equal(t, "/bot"+token+"/sendMessage", gotPath)
	require.Equal(t, `{"text":"hi"}`, gotBody)
	require.NotContains(t, e.logs.String(), token)

	for _, u := range []string{"https://example.com/x", "http://api.telegram.org/bot1/x", "https://api.telegram.org:8443/x", "https://user:pw@api.telegram.org/x"} {
		_, err = call[api.HTTPPostResult](t, s, api.CmdHTTPPost, api.HTTPPostArgs{URL: u})
		requireCode(t, err, deyerr.N050)
	}
	_, err = call[api.HTTPPostResult](t, s, api.CmdHTTPPost, api.HTTPPostArgs{URL: "https://api.telegram.org/x", Body: make([]byte, MaxHTTPPostBody+1)})
	requireCode(t, err, deyerr.N050)
}

func TestHTTPPostTransportErrorHidesToken(t *testing.T) {
	e := newEnv(t)
	e.opts.HTTPClient = &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Err: os.ErrDeadlineExceeded}
		},
	}}
	s := e.start()
	token := "987654321:BBHsecretTokenValue_zyxwvutsrqpo"
	_, err := call[api.HTTPPostResult](t, s, api.CmdHTTPPost, api.HTTPPostArgs{URL: "https://api.telegram.org/bot" + token + "/sendMessage"})
	de := requireCode(t, err, deyerr.X050)
	require.NotContains(t, de.Message()+de.Why()+de.Detail, token)
	require.NotContains(t, e.logs.String(), token)
}

func TestSysinfoMetricsDoctorSysctl(t *testing.T) {
	e := newEnv(t)
	tcp := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:01BB 0100007F:D431 01 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 20 4 30 10 -1\n" +
		"   1: 0100007F:01BB 0100007F:D432 01 00000000:00000000 00:00000000 00000000     0        0 2 1 0000000000000000 20 4 30 10 -1\n" +
		"   2: 00000000:01BB 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 3 1 0000000000000000 20 4 30 10 -1\n" +
		"   3: 0100007F:D431 0100007F:01BB 01 00000000:00000000 00:00000000 00000000     0        0 4 1 0000000000000000 20 4 30 10 -1\n"
	writeFile(t, e.path("/proc/net/tcp"), tcp, 0o644)
	s := e.start()

	info, err := call[map[string]string](t, s, api.CmdSysinfo, nil)
	require.NoError(t, err)
	require.Equal(t, "node-host", info["hostname"])
	require.Equal(t, "6.1.0-test", info["kernel"])
	require.Equal(t, "Debian GNU/Linux 12 (bookworm)", info["os"])
	require.Equal(t, "amd64", info["arch"])
	require.Equal(t, "1024000", info["mem_total"])
	require.Equal(t, "12345", info["uptime"])
	require.Equal(t, version.Version, info["version"])
	require.Equal(t, testNode, info["node_id"])

	m, err := call[api.MetricsResult](t, s, api.CmdMetrics, api.MetricsArgs{Ports: []int{443}})
	require.NoError(t, err)
	require.Equal(t, 2, m.ActiveConns)
	m, err = call[api.MetricsResult](t, s, api.CmdMetrics, api.MetricsArgs{})
	require.NoError(t, err)
	require.Zero(t, m.ActiveConns)

	dd, err := call[api.DoctorData](t, s, api.CmdDoctorCollect, nil)
	require.NoError(t, err)
	require.Equal(t, "node", dd.Role)
	require.Contains(t, dd.Sections, "status")
	require.Contains(t, dd.Sections, "os")
	require.Contains(t, dd.Sections["status"], `"role": "node"`)

	_, err = call[json.RawMessage](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: "balanced"})
	require.NoError(t, err)
	_, err = os.Stat(e.path(config.SysctlConfPath))
	require.NoError(t, err)
	_, err = call[json.RawMessage](t, s, api.CmdSysctlApply, api.SysctlArgs{Profile: "turbo"})
	requireCode(t, err, deyerr.C013)
}

func TestLogsTailAndFollow(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	logFile := e.path(systemd.TunnelLogFile("main"))
	writeFile(t, logFile, "one\ntwo\nthree password=hunter2hunter2\n", 0o600)

	lines, err := call[[]string](t, s, api.CmdLogsTail, api.LogsArgs{Target: "main", Lines: 2})
	require.NoError(t, err)
	require.Len(t, lines, 2)
	require.Equal(t, "two", lines[0])
	require.NotContains(t, lines[1], "hunter2hunter2")

	lines, err = call[[]string](t, s, api.CmdLogsTail, api.LogsArgs{Target: "nothing-yet"})
	require.NoError(t, err)
	require.Empty(t, lines)
	_, err = call[[]string](t, s, api.CmdLogsTail, api.LogsArgs{Target: "../../etc/shadow"})
	requireCode(t, err, deyerr.N050)

	nodeLines, err := call[[]string](t, s, api.CmdLogsTail, api.LogsArgs{Target: "node", Lines: 5})
	require.NoError(t, err)
	require.NotNil(t, nodeLines)

	// Follow: backlog plus appended lines, until the hub cancels.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan string, 100)
	done := make(chan error, 1)
	go func() {
		done <- s.Stream(ctx, api.CmdLogsTail, api.LogsArgs{Target: "main", Lines: 1, Follow: true}, func(ls []string) {
			for _, l := range ls {
				got <- l
			}
		})
	}()
	require.Contains(t, <-got, "three")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = f.WriteString("four\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	select {
	case l := <-got:
		require.Equal(t, "four", l)
	case <-time.After(10 * time.Second):
		t.Fatal("followed line not streamed")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("follow did not end")
	}
}

func TestSelfUpdate(t *testing.T) {
	e := newEnv(t)
	newBin := fakeELF(elf.EM_X86_64, elf.ET_EXEC)
	e.hub.asset, e.hub.assetV = newBin, "9.9.9"
	writeFile(t, e.path(config.BinaryPath), "old binary", 0o755)
	s := e.start()

	sum := sha256.Sum256(newBin)
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "9.9.9", SHA256: hex.EncodeToString(sum[:])})
	require.NoError(t, err)
	data, err := os.ReadFile(e.path(config.BinaryPath))
	require.NoError(t, err)
	require.Equal(t, newBin, data)
	prev, err := os.ReadFile(e.path(config.PrevBinaryPath))
	require.NoError(t, err)
	require.Equal(t, "old binary", string(prev))
	select {
	case <-e.restarts:
	case <-time.After(10 * time.Second):
		t.Fatal("the service was not restarted after self.update")
	}

	// The hub announcing another checksum or version than it serves.
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{SHA256: strings.Repeat("00", 32)})
	requireCode(t, err, deyerr.S001)
	_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{Version: "1.0.0"})
	requireCode(t, err, deyerr.S001)

	// A binary this node cannot run is never installed: it would leave the
	// node restarting into a dead service that the hub cannot reach.
	for name, asset := range map[string][]byte{
		"wrong arch": fakeELF(elf.EM_AARCH64, elf.ET_EXEC),
		"not ELF":    []byte("#!/bin/sh\necho hi\n"),
		"object":     fakeELF(elf.EM_X86_64, elf.ET_REL),
	} {
		e.hub.mu.Lock()
		e.hub.asset = asset
		e.hub.mu.Unlock()
		_, err = call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{})
		de := requireCode(t, err, deyerr.S001)
		require.Contains(t, de.Why(), "cannot run", name)
		data, err := os.ReadFile(e.path(config.BinaryPath))
		require.NoError(t, err)
		require.Equal(t, newBin, data, "%s: the installed binary is unchanged", name)
	}
	select {
	case <-e.restarts:
		t.Fatal("a refused update must not restart the service")
	case <-time.After(100 * time.Millisecond):
	}
	entries, err := os.ReadDir(e.path(DownloadDir))
	require.NoError(t, err)
	require.Empty(t, entries, "downloads are cleaned up")
}

func TestStaleDownloadsAreRemovedAtStart(t *testing.T) {
	e := newEnv(t)
	dir := e.path(DownloadDir)
	writeFile(t, filepath.Join(dir, "fetch-123/payload"), "half a download", 0o600)
	writeFile(t, filepath.Join(dir, "deyroute-update-456"), "half a binary", 0o600)
	writeFile(t, filepath.Join(dir, "other"), "not ours", 0o600)
	e.start()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "other", entries[0].Name())
}

func TestInterfaceAddrs(t *testing.T) {
	addrs, err := interfaceAddrs()
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(addrs, netip.Addr.IsLoopback), "%v", addrs)
}

func TestVerifyELF(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, data, 0o600))
		return p
	}
	amd := write("amd", fakeELF(elf.EM_X86_64, elf.ET_EXEC))
	pie := write("pie", fakeELF(elf.EM_AARCH64, elf.ET_DYN))
	require.NoError(t, VerifyELF(amd, "amd64"))
	require.NoError(t, VerifyELF(pie, "arm64"))
	require.NoError(t, VerifyELF(amd, "mips"), "unknown arch: any executable")
	require.ErrorContains(t, VerifyELF(amd, "arm64"), "EM_X86_64")
	require.ErrorContains(t, VerifyELF(write("rel", fakeELF(elf.EM_X86_64, elf.ET_REL)), "amd64"), "not an executable")
	require.ErrorContains(t, VerifyELF(write("txt", []byte("hello")), "amd64"), "not an ELF")
	require.Error(t, VerifyELF(filepath.Join(dir, "missing"), "amd64"))
}

func TestIncompatibleHubRefusesCommands(t *testing.T) {
	e := newEnv(t)
	e.hub.hello = api.Hello{Version: "99.0.0", Compatible: false}
	e.hub.asset, e.hub.assetV = fakeELF(elf.EM_X86_64, elf.ET_EXEC), "99.0.0"
	s := e.start()
	eventually(t, func() bool {
		_, err := call[map[string]string](t, s, api.CmdSysinfo, nil)
		return deyerr.HasCode(err, deyerr.N004)
	}, "commands are refused with N004")
	// self.update is still allowed: it is how the node becomes compatible.
	_, err := call[json.RawMessage](t, s, api.CmdSelfUpdate, api.SelfUpdateArgs{})
	require.NoError(t, err)
	l, err := api.Dial(e.socket, api.DialOptions{Service: ServiceName})
	require.NoError(t, err)
	st, err := l.Status(context.Background())
	require.NoError(t, err)
	require.False(t, st.NodeSelf.Compatible)
	require.Equal(t, "99.0.0", st.NodeSelf.HubVersion)
	codes := []string{}
	for _, w := range st.Warnings {
		codes = append(codes, w.Code)
	}
	require.Contains(t, codes, string(deyerr.N004))
}

func TestCommandErrors(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	_, err := call[json.RawMessage](t, s, "no.such.command", nil)
	requireCode(t, err, deyerr.N015)
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, nil)
	requireCode(t, err, deyerr.N050)
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, json.RawMessage(`"not an object"`))
	requireCode(t, err, deyerr.N015)
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStart, api.UnitArgs{Instance: "x;rm -rf"})
	requireCode(t, err, deyerr.N050)
	_, err = call[api.UnitStatus](t, s, api.CmdUnitStatus, api.UnitArgs{Instance: "main.nl-1.backhaul-tcp"})
	requireCode(t, err, deyerr.N050)
	_, err = call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "../x"})
	requireCode(t, err, deyerr.N050)
	_, err = call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "a", Entry: backend.ManifestEntry{Name: "b"}})
	requireCode(t, err, deyerr.N050)
	// A pinned backend without checksum is never installed (S006).
	_, err = call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "backhaul",
		Entry: backend.ManifestEntry{Version: "v0.7.2", URLs: map[string]string{"amd64": "https://example.com/b.tar.gz"}, Archive: "tar.gz"}})
	requireCode(t, err, deyerr.S006)
	// Builtin backends need nothing.
	r, err := call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "direct", Entry: backend.ManifestEntry{Version: "builtin", Builtin: true}})
	require.NoError(t, err)
	require.Empty(t, r.BinDir)
	eventually(t, func() bool {
		select {
		case hb := <-s.Heartbeats():
			return strings.Contains(hb.LastError, "DEY-")
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}, "the last error travels in the heartbeat")
}

func TestUninstallCommand(t *testing.T) {
	e := newEnv(t)
	s := e.start()
	_, err := call[json.RawMessage](t, s, api.CmdUninstall, nil)
	require.NoError(t, err)
	// The hub may repeat the command (a retry after a timeout): the removal
	// still runs once.
	_, err = call[json.RawMessage](t, s, api.CmdUninstall, nil)
	require.NoError(t, err)
	select {
	case <-e.unins:
	case <-time.After(10 * time.Second):
		t.Fatal("uninstall did not run")
	}
	select {
	case <-e.unins:
		t.Fatal("uninstall ran twice")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestCanaryEchoSurvivesAgentRestart(t *testing.T) {
	e := newEnv(t)
	s, stop := e.startStoppable()
	echo, err := call[api.EchoResult](t, s, api.CmdEchoStart, api.EchoArgs{})
	require.NoError(t, err)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(echo.Port))
	stop()
	require.False(t, health.TCPEcho(context.Background(), addr, time.Second).OK, "the echo stops with the agent")

	// self.update restarts the agent while the canary unit keeps running.
	s = e.start()
	eventually(t, func() bool { return health.TCPEcho(context.Background(), addr, time.Second).OK },
		"the canary echo is open again after the restart")
	_, err = call[json.RawMessage](t, s, api.CmdEchoStop, api.EchoArgs(echo))
	require.NoError(t, err)
	data, err := os.ReadFile(e.path(StateFile))
	require.NoError(t, err)
	var st nodeState
	require.NoError(t, json.Unmarshal(data, &st))
	require.Empty(t, st.Echo, "a stopped echo is forgotten")
}

func TestFetchProxyRefusesPlainHTTPRedirect(t *testing.T) {
	payload := []byte("payload")
	sum := sha256.Sum256(payload)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer plain.Close()
	var hits int
	var mu sync.Mutex
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		if r.URL.Path == "/final" {
			_, _ = w.Write(payload)
			return
		}
		http.Redirect(w, r, plain.URL+"/x", http.StatusFound)
	}))
	defer tlsSrv.Close()
	e := newEnv(t)
	e.opts.HTTPClient = tlsSrv.Client()
	s := e.start()

	for _, sha := range []string{hex.EncodeToString(sum[:]), ""} {
		_, err := call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: tlsSrv.URL + "/start", SHA256: sha, UploadID: "up-" + strconv.Itoa(len(sha))})
		de := requireCode(t, err, deyerr.N051)
		require.Contains(t, de.Detail, "non-https")
		_, ok := e.hub.upload("up-" + strconv.Itoa(len(sha)))
		require.False(t, ok)
	}
	mu.Lock()
	require.Equal(t, 2, hits, "a refused redirect is not retried")
	mu.Unlock()

	// https → https redirects (GitHub release assets) are followed.
	res, err := call[api.FetchResult](t, s, api.CmdFetchProxy, api.FetchArgs{URL: tlsSrv.URL + "/hop", SHA256: hex.EncodeToString(sum[:]), UploadID: "up-ok"})
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), res.Bytes)
}

// tarGz builds a tar.gz holding one executable.
func tarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}))
	_, err := tw.Write(data)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestBackendInstall(t *testing.T) {
	archive := tarGz(t, "backhaul", []byte("\x7fELF fake backhaul"))
	sum := sha256.Sum256(archive)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	defer srv.Close()
	e := newEnv(t)
	e.opts.HTTPClient = srv.Client()
	s := e.start()
	entry := backend.ManifestEntry{Version: "v0.7.2", Archive: "tar.gz", Binaries: []string{"backhaul"},
		URLs: map[string]string{"amd64": srv.URL + "/backhaul_linux_amd64.tar.gz"}, SHA256: map[string]string{"amd64": hex.EncodeToString(sum[:])}}
	res, err := call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "backhaul", Entry: entry})
	require.NoError(t, err)
	require.Equal(t, "/var/lib/deyroute/bin/backhaul/v0.7.2", res.BinDir)
	require.Equal(t, backhaulBin, res.Binary)
	data, err := os.ReadFile(e.path(backhaulBin))
	require.NoError(t, err)
	require.Equal(t, "\x7fELF fake backhaul", string(data))

	// A wrong checksum never installs anything.
	bad := entry
	bad.Version = "v0.7.3"
	bad.SHA256 = map[string]string{"amd64": strings.Repeat("ab", 32)}
	_, err = call[api.BackendInstallResult](t, s, api.CmdBackendInstall, api.BackendInstallArgs{Name: "backhaul", Entry: bad})
	requireCode(t, err, deyerr.S001)
	_, err = os.Stat(e.path("/var/lib/deyroute/bin/backhaul/v0.7.3"))
	require.True(t, os.IsNotExist(err))
}

func TestHTTPPostDoesNotFollowRedirects(t *testing.T) {
	var hits int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "https://evil.example/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	e := newEnv(t)
	e.opts.HTTPClient = telegramClient(srv)
	s := e.start()
	res, err := call[api.HTTPPostResult](t, s, api.CmdHTTPPost, api.HTTPPostArgs{URL: "https://api.telegram.org/bot1:x/sendMessage"})
	require.NoError(t, err)
	require.Equal(t, http.StatusTemporaryRedirect, res.Status)
	require.Equal(t, 1, hits)
}

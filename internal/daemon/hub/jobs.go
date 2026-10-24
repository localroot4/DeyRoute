package hub

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// Defaults of the background jobs (sections 5, 7.4, 10, 12).
const (
	// DefaultDecoyInterval re-tests the decoy SNIs (section 7.4).
	DefaultDecoyInterval = 6 * time.Hour
	// DefaultTLSRenewInterval runs the tunnel certificate renewal pass
	// (section 10: renewed 30 days before expiry; ACME retried daily).
	DefaultTLSRenewInterval = 24 * time.Hour
	// DefaultUpdateCheckInterval is the optional daily release check
	// (section 5: hub.update_check, off by default; it never installs).
	DefaultUpdateCheckInterval = 24 * time.Hour
	// DefaultMetricsInterval counts the active connections of every tunnel.
	DefaultMetricsInterval = 30 * time.Second
	// DefaultJobStartDelay delays the first TLS renewal pass and update
	// check after a start (the startup reconcile goes first).
	DefaultJobStartDelay = 10 * time.Minute
	// DefaultBackendProbeWait is the probe window of `update backends`
	// (section 5: rolled back when the probe is not green within 60 s).
	DefaultBackendProbeWait = 60 * time.Second
	// DefaultRestartDelay lets the answer of update reach the CLI before
	// deyroute-hub restarts.
	DefaultRestartDelay = time.Second
	// DefaultNodeReconnectWait bounds the wait for a node that reconnects
	// with its new certificate (rotate-ca).
	DefaultNodeReconnectWait = 30 * time.Second

	// decoyTimeout bounds one decoy check (the lookup and the handshake).
	decoyTimeout = 5 * time.Second
	// decoyMinConnect is the least time a connect to one of several decoy
	// addresses gets, as in Go's own dialer.
	decoyMinConnect = 2 * time.Second
	// nodeUpdateTimeout bounds one self.update of a node.
	nodeUpdateTimeout = 10 * time.Minute
	// metricsCallTimeout bounds the node metrics command.
	metricsCallTimeout = 10 * time.Second
	// UpdateAvailableEvent is emitted by the optional daily release check.
	UpdateAvailableEvent = "update_available"
)

// startJobs runs the background jobs of the operations until ctx ends
// (Serve owns them through run).
func (h *Hub) startJobs(ctx context.Context, run func(func())) {
	run(func() { h.decoyLoop(ctx) })
	run(func() { h.tlsRenewLoop(ctx) })
	run(func() { h.updateCheckLoop(ctx) })
	run(func() { h.autoUpdateLoop(ctx) })
	run(func() { h.metricsLoop(ctx) })
	run(func() { h.nodeUpdateLoop(ctx) })
	run(func() { h.restartLoop(ctx) })
	run(func() { h.trafficLoop(ctx) })
	run(func() { h.tuneCheckLoop(ctx) })
}

// every runs fn after first and then every interval until ctx ends.
func every(ctx context.Context, first, interval time.Duration, fn func()) {
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
			t.Reset(interval)
		}
	}
}

// ---------------------------------------------------------------- decoy SNI

// decoyLoop checks the decoy SNIs at start, every DecoyInterval and when
// the owner changed the decoy list (requestDecoyCheck).
func (h *Hub) decoyLoop(ctx context.Context) {
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-h.ops.decoyKick:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
		h.checkDecoys(ctx)
		t.Reset(h.o.DecoyInterval)
	}
}

// requestDecoyCheck asks the decoy job for a check now (the decoy list
// changed: the first reachable one is used, section 7.4).
func (h *Hub) requestDecoyCheck() {
	select {
	case h.ops.decoyKick <- struct{}{}:
	default:
	}
}

// checkDecoys tests hub.decoy_snis (or the built-in list) in order with a
// TLS 1.3 + HTTP/2 handshake from the hub; the first that answers becomes the decoy
// of the Reality transports (section 7.4). The check dials the public
// addresses the name resolves to from the hub, over the family of
// hub.public_ip; a name that resolves to private addresses only fails
// before the dial. When it changed, the tunnels are rendered again. When
// none answers the last choice stays and a warning event (DEY-B042) is
// emitted with the reason of every decoy.
func (h *Hub) checkDecoys(ctx context.Context) {
	cfg := h.Config()
	cands := backend.DecoyCandidates(cfg.Hub.DecoySNIs)
	if len(cands) == 0 {
		return
	}
	network := decoyNetwork(cfg)
	var chosen string
	var reasons []string
	for _, sni := range cands {
		cctx, cancel := context.WithTimeout(ctx, decoyTimeout)
		addrs, err := h.decoyAddrs(cctx, network, sni)
		if err == nil {
			err = h.o.DecoyCheck(cctx, network, sni, addrs)
		}
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			chosen = sni
			break
		}
		reasons = append(reasons, sni+": "+dlog.Redact(err.Error()))
	}
	if chosen == "" {
		e := deyerr.New(deyerr.B042, deyerr.Params{"decoys": strings.Join(cands, ", ")})
		h.log.Warn("no decoy SNI is reachable; the Reality transports keep the last choice", dlog.Code(e.Code),
			slog.String("reasons", strings.Join(reasons, "; ")))
		h.Emit(state.Event{Type: state.EvProbeError, Level: state.LevelWarn, Code: string(e.Code),
			Reason: strings.Join(reasons, "; "), Message: e.Message()})
		return
	}
	before := h.currentDecoy(cfg)
	h.ops.decoyMu.Lock()
	prev := h.ops.decoy
	h.ops.decoyMu.Unlock()
	if prev != chosen {
		// Saved first: a restart right after renders with the same choice.
		if err := h.st.PutMeta(metaDecoy, chosen); err != nil {
			h.log.Warn("cannot save the decoy choice", dlog.Err(err))
		}
		h.ops.decoyMu.Lock()
		h.ops.decoy = chosen
		h.ops.decoyMu.Unlock()
	}
	if chosen == before {
		return
	}
	h.log.Info("decoy SNI changed; rendering the tunnels again", slog.String("from", before), slog.String("to", chosen))
	if err := h.reconcileAll(ctx); err != nil && ctx.Err() == nil {
		h.log.Warn("reconcile after a decoy change finished with errors", dlog.Err(err))
	}
}

// decoyNetwork is the network of the decoy check: "tcp4" when
// hub.public_ip is IPv4, so that a dual-stack hub tests the decoy over the
// family of its Reality paths instead of the IPv6 Go may prefer; "tcp" on
// an IPv6 hub.
func decoyNetwork(cfg *config.Config) string {
	if a, err := netip.ParseAddr(cfg.Hub.PublicIP); err == nil && a.Unmap().Is4() {
		return "tcp4"
	}
	return "tcp"
}

// decoyAddrs looks sni up as the hub sees it and returns its public answers
// (decoyAddrPublic), which the decoy check dials: the address tested is the
// one checked here, and the name is not looked up twice within
// decoyTimeout. It fails when no answer is public: DNS tampering answers
// blocked names with private or reserved addresses, and the handshake
// would only fail with an unclear connect or TLS error. A failed or empty
// lookup returns no addresses: the check then dials the name and reports
// the lookup error itself.
func (h *Hub) decoyAddrs(ctx context.Context, network, sni string) ([]netip.Addr, error) {
	ipNet := "ip"
	if network == "tcp4" {
		ipNet = "ip4"
	}
	answers, err := h.o.DecoyResolver(ctx, ipNet, sni)
	if err != nil || len(answers) == 0 {
		return nil, nil
	}
	var public []netip.Addr
	shown := make([]string, 0, len(answers))
	for _, a := range answers {
		a = a.Unmap()
		if decoyAddrPublic(a) {
			public = append(public, a)
		}
		shown = append(shown, a.String())
	}
	if len(public) == 0 {
		return nil, fmt.Errorf("resolves to a private address from the hub (%s; DNS tampering?)", strings.Join(shown, ", "))
	}
	return public, nil
}

var (
	// global6 is the IPv6 global unicast space every public address is
	// allocated from (RFC 4291).
	global6 = netip.MustParsePrefix("2000::/3")
	// nat64 and nat64Local are the NAT64 prefixes (RFC 6052, RFC 8215):
	// DNS64 answers an IPv4-only name with them on an IPv6-only hub.
	nat64      = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local = netip.MustParsePrefix("64:ff9b:1::/48")
)

// decoyAddrPublic reports whether a decoy answer is a public address:
// setup.IsPublicIP, and for IPv6 also inside 2000::/3, since the reserved
// addresses DNS tampering injects as AAAA answers (d0::11, the
// IPv4-compatible ::a0a:2223 of 10.10.34.35) are global unicast to Go. A
// well-known NAT64 address counts by the IPv4 address it embeds; a
// local-use one (whose embedding varies) counts as public.
func decoyAddrPublic(a netip.Addr) bool {
	a = a.Unmap()
	switch {
	case a.Is4():
		return setup.IsPublicIP(a)
	case nat64.Contains(a):
		b := a.As16()
		return setup.IsPublicIP(netip.AddrFrom4([4]byte(b[12:])))
	case nat64Local.Contains(a):
		return true
	}
	return global6.Contains(a) && setup.IsPublicIP(a)
}

// checkDecoyTLS is the default Options.DecoyCheck: a TLS 1.3 handshake with
// sni on port 443 over network whose certificate verifies against the
// system roots and that agrees on HTTP/2, as REALITY needs of its target.
// It dials addrs (the public answers of decoyAddrs) in order, or the name
// when the lookup gave none.
func checkDecoyTLS(ctx context.Context, network, sni string, addrs []netip.Addr) error {
	dial := []string{net.JoinHostPort(sni, "443")}
	if len(addrs) > 0 {
		dial = make([]string, len(addrs))
		for i, a := range addrs {
			dial[i] = netip.AddrPortFrom(a, 443).String()
		}
	}
	return decoyHandshake(ctx, network, sni, dial, nil)
}

// decoyHandshake runs the decoy check against the first of addrs (at least
// one) that answers over network, and returns the first error when none
// does. As in Go's own dialer, a connect gets an equal share of the time
// left (at least decoyMinConnect) and the handshake the rest. nil roots
// means the system roots.
func decoyHandshake(ctx context.Context, network, sni string, addrs []string, roots *x509.CertPool) error {
	cfg := &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2", "http/1.1"}, RootCAs: roots}
	var first error
	for i, addr := range addrs {
		err := decoyHandshakeAt(ctx, network, addr, len(addrs)-i, cfg)
		if err == nil {
			return nil
		}
		if first == nil {
			first = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return first
}

// decoyHandshakeAt is decoyHandshake for one address, with left addresses
// (this one included) still to try.
func decoyHandshakeAt(ctx context.Context, network, addr string, left int, cfg *tls.Config) error {
	cctx, cancel := ctx, context.CancelFunc(func() {})
	if dl, ok := ctx.Deadline(); ok && left > 1 {
		remain := time.Until(dl)
		share := remain / time.Duration(left)
		if share < decoyMinConnect {
			share = min(remain, decoyMinConnect)
		}
		cctx, cancel = context.WithTimeout(ctx, share)
	}
	var d net.Dialer
	raw, err := d.DialContext(cctx, network, addr)
	cancel()
	if err != nil {
		return err
	}
	conn := tls.Client(raw, cfg)
	defer func() { _ = conn.Close() }()
	if err := conn.HandshakeContext(ctx); err != nil {
		return err
	}
	if p := conn.ConnectionState().NegotiatedProtocol; p != "h2" {
		return fmt.Errorf("no HTTP/2 (ALPN %q)", p)
	}
	return nil
}

// ---------------------------------------------------------------- TLS renewal

// tlsRenewLoop runs the certificate renewal pass daily.
func (h *Hub) tlsRenewLoop(ctx context.Context) {
	every(ctx, h.o.JobStartDelay, h.o.TLSRenewInterval, func() {
		if err := h.waitTunnelsReady(ctx); err != nil {
			return
		}
		h.renewDue(ctx)
	})
}

// ---------------------------------------------------------------- update check

// updateCheckLoop checks for a new release once a day when hub.update_check
// is on (section 5): an event and a dashboard line, never an installation.
func (h *Hub) updateCheckLoop(ctx context.Context) {
	every(ctx, h.o.JobStartDelay, h.o.UpdateCheckInterval, func() {
		cfg := h.Config()
		if cfg.Hub == nil || !cfg.Hub.UpdateCheck {
			return
		}
		h.dailyUpdateCheck(ctx)
	})
}

// dailyUpdateCheck runs one release check and reports a new release.
func (h *Hub) dailyUpdateCheck(ctx context.Context) {
	info, err := h.checkRelease(ctx)
	if err != nil {
		h.log.Info("release check failed", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return
	}
	if !info.Available {
		return
	}
	var last api.UpdateInfo
	if ok, err := h.st.GetMeta(metaUpdateCheck+"/announced", &last); err == nil && ok && last.Latest == info.Latest {
		return
	}
	_ = h.st.PutMeta(metaUpdateCheck+"/announced", info)
	h.Emit(state.Event{Type: UpdateAvailableEvent, Level: state.LevelInfo,
		Message: "deyroute " + info.Latest + " is available (running " + info.Current + "): run deyroute update"})
}

// ---------------------------------------------------------------- metrics

// metricsLoop records the active connections of every running tunnel every
// MetricsInterval (section 4: metrics/<tunnel>).
func (h *Hub) metricsLoop(ctx context.Context) {
	every(ctx, h.o.MetricsInterval, h.o.MetricsInterval, func() { h.collectMetrics(ctx) })
}

// collectMetrics records, for every tunnel whose engine runs a candidate,
// the established TCP connections on its listen ports on the hub and its
// byte counters (the accounting table, Source "nft"; "ss" when accounting
// is unavailable), all tunnels in one transaction. /proc is read once per
// pass and the counts are handed to the traffic sampler, which copies them
// into its points. The hub side of a NAT transport (WireGuard) forwards in
// the kernel without a socket, so the node counts the connections to the
// targets instead (metrics command); when it does not answer the record is
// marked stale. A tunnel with UDP ports only has no connection count.
func (h *Hub) collectMetrics(ctx context.Context) {
	var established map[int]int
	batch := map[string]state.Metrics{}
	for _, c := range h.tun.all() {
		st, ok := c.liveState()
		if !ok || st.Active.IsZero() || !runningState(st.State) {
			continue
		}
		t, plan := c.snapshot()
		var tcpPorts []int
		for _, pm := range t.Ports {
			if pm.Proto == config.ProtoTCP {
				tcpPorts = append(tcpPorts, pm.Listen)
			}
		}
		m := state.Metrics{At: h.now(), Source: state.MetricsSourceSS}
		counted, countedOK := h.traffic.counter(t.ID)
		pc, planned := plan.Candidate(st.Active.Node, st.Active.Transport)
		switch {
		case len(tcpPorts) == 0:
			m.ConnsUnknown = true
			h.traffic.noteConns(t.ID, 0, false)
		case planned && len(pc.Hub.NAT) > 0:
			targets := targetPorts(t)
			var res api.MetricsResult
			cctx, cancel := context.WithTimeout(ctx, metricsCallTimeout)
			err := h.Call(cctx, st.Active.Node, api.CmdMetrics, api.MetricsArgs{Ports: targets}, &res)
			cancel()
			if err != nil {
				// Not the old numbers silently: the record says they are
				// not current.
				m.Stale, m.ConnsUnknown = true, true
				h.traffic.noteConns(t.ID, 0, false)
				h.log.Debug("node metrics unavailable", dlog.Tunnel(t.ID), dlog.Node(st.Active.Node), dlog.Err(err))
				break
			}
			m.ActiveConns, m.BytesIn, m.BytesOut = res.ActiveConns, res.BytesIn, res.BytesOut
			h.traffic.noteConns(t.ID, res.ActiveConns, true)
		default:
			if established == nil {
				established = h.establishedByPort(ctx)
			}
			for _, p := range tcpPorts {
				m.ActiveConns += established[p]
			}
			h.traffic.noteConns(t.ID, m.ActiveConns, true)
		}
		if countedOK {
			m.BytesIn, m.BytesOut, m.BytesSince, m.Source = counted.In, counted.Out, counted.Since, state.MetricsSourceNFT
		} else if len(tcpPorts) == 0 {
			continue // nothing measured at all
		}
		batch[t.ID] = m
	}
	if err := h.st.PutMetricsBatch(batch); err != nil {
		h.log.Debug("cannot store tunnel metrics", dlog.Err(err))
	}
}

// targetPorts returns the distinct TCP target ports of a tunnel.
func targetPorts(t config.Tunnel) []int {
	seen := map[int]bool{}
	var out []int
	for _, pm := range t.Ports {
		if pm.Proto != config.ProtoTCP {
			continue
		}
		_, p, err := net.SplitHostPort(pm.Target)
		if err != nil {
			continue
		}
		if n, err := strconv.Atoi(p); err == nil && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// establishedByPort counts the ESTABLISHED TCP sockets of the hub per local
// port: /proc/net/tcp and tcp6 (below Options.ProcRoot), or `ss` when they
// cannot be read.
func (h *Hub) establishedByPort(ctx context.Context) map[int]int {
	out := map[int]int{}
	read := false
	for _, f := range []string{"tcp", "tcp6"} {
		data, err := os.ReadFile(filepath.Join(h.o.ProcRoot, "proc", "net", f)) // #nosec G304 -- procfs below ProcRoot
		if err != nil {
			continue
		}
		read = true
		countProcNet(data, out)
	}
	if read {
		return out
	}
	cctx, cancel := context.WithTimeout(ctx, metricsCallTimeout)
	defer cancel()
	stdout, _, err := h.o.Runner.Run(cctx, "ss", []string{"-Htn", "state", "established"}, nil)
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	for sc.Scan() {
		for _, f := range strings.Fields(sc.Text()) {
			i := strings.LastIndexByte(f, ':')
			if i < 0 {
				continue
			}
			if p, err := strconv.Atoi(f[i+1:]); err == nil {
				out[p]++
			}
			break // the first address is the local one
		}
	}
	return out
}

// countProcNet adds the ESTABLISHED (state 01) sockets of one
// /proc/net/tcp{,6} file to out by local port.
func countProcNet(data []byte, out map[int]int) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	first := true
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != "01" {
			continue
		}
		_, portHex, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		b, err := hex.DecodeString(portHex)
		if err != nil || len(b) != 2 {
			continue
		}
		out[int(b[0])<<8|int(b[1])]++
	}
}

// ---------------------------------------------------------------- restart

// scheduleRestart asks for a restart of deyroute-hub once the current Local
// API answer is out (update, rollback). Tunnels keep running: their units
// are separate (section 5).
func (h *Hub) scheduleRestart() {
	select {
	case h.ops.restart <- struct{}{}:
	default:
	}
}

// restartLoop runs `systemctl restart --no-block deyroute-hub` when asked.
func (h *Hub) restartLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.ops.restart:
		}
		if err := sleepCtx(ctx, h.o.RestartDelay); err != nil {
			return
		}
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, _, err := h.o.Runner.Run(rctx, "systemctl", []string{"restart", "--no-block", ServiceName + ".service"}, nil)
		cancel()
		if err != nil {
			h.log.Error("cannot restart the hub service; restart it by hand: systemctl restart "+ServiceName,
				dlog.Err(err), dlog.Code(deyerr.As(err).Code))
			continue
		}
		h.log.Info("hub service restart requested")
	}
}

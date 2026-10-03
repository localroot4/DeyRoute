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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
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

	// decoyTimeout bounds one decoy handshake.
	decoyTimeout = 5 * time.Second
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
// of the Reality transports (section 7.4). When it changed, the tunnels are
// rendered again. When none answers the last choice stays and a warning
// event (DEY-B042) is emitted.
func (h *Hub) checkDecoys(ctx context.Context) {
	cfg := h.Config()
	cands := backend.DecoyCandidates(cfg.Hub.DecoySNIs)
	if len(cands) == 0 {
		return
	}
	var chosen string
	var reasons []string
	for _, sni := range cands {
		cctx, cancel := context.WithTimeout(ctx, decoyTimeout)
		err := h.o.DecoyCheck(cctx, sni)
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

// checkDecoyTLS is the default Options.DecoyCheck: a TLS 1.3 handshake with
// sni:443 whose certificate verifies against the system roots and that
// agrees on HTTP/2, as REALITY needs of its target.
func checkDecoyTLS(ctx context.Context, sni string) error {
	return decoyHandshake(ctx, sni, net.JoinHostPort(sni, "443"), nil)
}

// decoyHandshake runs the decoy check against addr; nil roots means the
// system roots.
func decoyHandshake(ctx context.Context, sni, addr string, roots *x509.CertPool) error {
	d := tls.Dialer{Config: &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2", "http/1.1"}, RootCAs: roots}}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if p := conn.(*tls.Conn).ConnectionState().NegotiatedProtocol; p != "h2" {
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

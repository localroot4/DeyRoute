package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
)

// Firewall manager limits.
const (
	// firewallApplyTimeout bounds one `nft -f -`.
	firewallApplyTimeout = 30 * time.Second
	// joinWindowSlack is added to the last token expiry before the join
	// window is closed, so the token is certainly expired by then.
	joinWindowSlack = 100 * time.Millisecond
)

// FirewallState is the result of the last firewall computation.
type FirewallState struct {
	// Spec is the table inet deyroute the hub wants (applied when Managed).
	Spec firewall.Spec
	// Managed is security.firewall_managed; when false nothing is applied
	// and the spec only yields suggestions (section 11).
	Managed bool
	// Applied is when the spec was last applied successfully (zero when
	// never, or when the firewall is not managed or disabled).
	Applied time.Time
	// Err is the last apply error (DEY-P019), nil when it succeeded.
	Err error
	// Computed is false before the first computation.
	Computed bool
}

// requestFirewall asks for a firewall re-apply after the debounce delay
// (nodes, tunnels, active candidates or the join window changed).
func (h *Hub) requestFirewall() {
	select {
	case h.fwKick <- struct{}{}:
	default:
	}
}

// wakeFirewall makes the firewall loop re-read the join token expiry (after
// a synchronous apply that opened the join window).
func (h *Hub) wakeFirewall() {
	select {
	case h.fwWake <- struct{}{}:
	default:
	}
}

// firewallLoop applies the firewall when asked (debounced) and when the
// join window closes because the last join token expired.
func (h *Hub) firewallLoop(ctx context.Context) {
	var (
		debounce, expiry   *time.Timer
		debounceC, expiryC <-chan time.Time
		expiryAt           time.Time
	)
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
		if expiry != nil {
			expiry.Stop()
		}
	}()
	schedule := func() {
		next := h.joins.Expiry()
		if next.Equal(expiryAt) && (expiry != nil || next.IsZero()) {
			return
		}
		if expiry != nil {
			expiry.Stop()
			expiry, expiryC = nil, nil
		}
		expiryAt = next
		if next.IsZero() {
			return
		}
		expiry = time.NewTimer(max(next.Sub(h.now())+joinWindowSlack, 0))
		expiryC = expiry.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.fwKick:
			if debounce == nil {
				debounce = time.NewTimer(h.o.FirewallDebounce)
				debounceC = debounce.C
			}
		case <-h.fwWake:
			schedule()
		case <-debounceC:
			debounce, debounceC = nil, nil
			_ = h.applyFirewall(ctx)
			// Tunnels, ports or rungs changed: the accounting table follows
			// (rebuilt only when its ports changed).
			h.traffic.requestCheck()
			schedule()
		case <-expiryC:
			expiry, expiryC, expiryAt = nil, nil, time.Time{}
			if err := h.joins.Prune(); err != nil {
				h.log.Warn("cannot prune the join tokens", dlog.Err(err))
			}
			_ = h.applyFirewall(ctx)
			schedule()
		}
	}
}

// applyFirewall computes table inet deyroute from the configuration, the
// active candidates and the join window and applies it with nft unless
// security.firewall_managed is false (suggestions only) or the firewall is
// disabled (tests). Applies are serialised and the table is computed under
// the same lock, so a slower apply can never install an older table over a
// newer one. When firewall_managed was switched off (config apply, or
// while the hub was not running), the table deyroute applied is removed: a
// stale @nodes restriction would otherwise keep blocking new nodes. Until the startup reconcile adopted
// the running candidates, the NAT rules of the table the previous hub
// process applied stay in place (a hub restart never interrupts a NAT
// transport, sections 3 and 5). Errors are logged, remembered for the
// dashboard (DEY-P019) and returned.
func (h *Hub) applyFirewall(ctx context.Context) error {
	h.fwApplyMu.Lock()
	defer h.fwApplyMu.Unlock()
	return h.applyFirewallLocked(ctx)
}

// applyFirewallLocked is applyFirewall with fwApplyMu held. After an apply
// it takes the fingerprint of the live table the periodic check compares
// (reassertFirewall), unless that check is off.
func (h *Hub) applyFirewallLocked(ctx context.Context) error {
	cfg := h.Config()
	sides := h.activeHubSides()
	if !h.tunnelsReady() {
		if carry, ok := h.natCarry(); ok {
			sides = append(sides, carry)
		}
	}
	spec := render.FirewallSpec(cfg, sides, h.joins.Active(), render.DefaultUnknownControlRate)
	if h.ctlPort != 0 {
		// The port the control listener is bound to, not a changed
		// hub.control_port that takes effect only at the next restart.
		spec.ControlPort = h.ctlPort
	}
	// Likewise the front: the port its listener is really bound to (none
	// while it is disabled or could not start), opened to the CDN ranges or,
	// with hub.front.cf_only false, to everyone.
	spec.FrontPort = h.frontBoundPort()
	spec.FrontOpen = spec.FrontPort != 0 && cfg.Hub != nil && !cfg.Hub.Front.CFOnlyOrDefault()
	managed := firewallManaged(cfg)
	h.fwMu.Lock()
	first := !h.fw.done
	wasManaged := h.fw.done && h.fw.managed
	h.fwMu.Unlock()
	var err error
	switch {
	case managed && !h.o.DisableFirewall:
		actx, cancel := context.WithTimeout(ctx, firewallApplyTimeout)
		err = firewall.Apply(actx, h.o.Runner, spec)
		cancel()
		if err != nil {
			h.log.Error("firewall apply failed", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		} else {
			h.log.Debug("firewall applied")
		}
	case !managed && (wasManaged || first) && !h.o.DisableFirewall:
		err = h.removeFirewall(ctx, first)
	}
	if managed && err == nil {
		h.saveNATCarry(spec)
	}
	h.fwLive = ""
	if managed && err == nil && !h.o.DisableFirewall && cfg.FirewallReassertInterval() > 0 {
		// Unknown ("") when nft cannot list it now: the next check takes it.
		h.fwLive, _ = firewall.Fingerprint(ctx, h.o.Runner)
	}
	h.fwMu.Lock()
	defer h.fwMu.Unlock()
	h.fw.spec, h.fw.managed, h.fw.done, h.fw.err = spec, managed, true, err
	if err == nil && managed && !h.o.DisableFirewall {
		h.fw.applied = h.now()
	}
	return err
}

// firewallReassertLoop runs reassertFirewall every
// security.firewall_reassert_s seconds (Options.FirewallReassertTick in
// tests). While the check is off it looks at the configuration again after
// the default interval, so switching it on takes effect without a restart.
func (h *Hub) firewallReassertLoop(ctx context.Context) {
	for {
		every := h.o.FirewallReassertTick
		if every <= 0 {
			every = h.Config().FirewallReassertInterval()
		}
		wait := every
		if wait <= 0 {
			wait = config.DefaultFirewallReassertS * time.Second
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if every > 0 {
			h.reassertFirewall(ctx)
		}
	}
}

// Reasons of reassertFirewall (event firewall_reapplied).
const (
	reassertMissing = firewall.ReasonMissing
	reassertDrift   = firewall.ReasonDrift
)

// reassertFirewall compares the live table inet deyroute with the one the
// hub applied last and applies it again when it is missing or changed
// (another tool, `nft flush ruleset`, an admin; DR-14). It returns the
// reason ("missing", "drift") or "" when nothing was applied. It runs
// under the apply lock, so it never races a real apply, and only while
// the firewall is managed, the check is on (security.firewall_reassert_s)
// and the last apply succeeded: a failed apply is already reported
// (DEY-P019) and retried by the next change. The table is computed anew,
// as by every apply. Event firewall_reapplied is emitted at most once per
// 10 minutes per reason; the other re-applies are logged.
func (h *Hub) reassertFirewall(ctx context.Context) string {
	h.fwApplyMu.Lock()
	reason := h.reassertFirewallLocked(ctx)
	report := reason != "" && h.fwReport.Due(reason, h.now())
	h.fwApplyMu.Unlock()
	if reason == "" {
		return ""
	}
	what := "was missing"
	if reason == reassertDrift {
		what = "was changed by something else"
	}
	msg := "table " + firewall.TableRef + " " + what + "; deyroute applied it again"
	if !report {
		h.log.Info(msg+" (reported less often)", slog.String("event", state.EvFirewallReapplied), slog.String("reason", reason))
		return reason
	}
	h.Emit(state.Event{Type: state.EvFirewallReapplied, Level: state.LevelWarn, Reason: reason, Message: msg})
	return reason
}

// reassertFirewallLocked is reassertFirewall's check and apply (fwApplyMu
// held).
func (h *Hub) reassertFirewallLocked(ctx context.Context) string {
	cfg := h.Config()
	if h.o.DisableFirewall || !firewallManaged(cfg) || cfg.FirewallReassertInterval() <= 0 {
		return ""
	}
	h.fwMu.Lock()
	ok := h.fw.done && h.fw.managed && h.fw.err == nil
	h.fwMu.Unlock()
	if !ok {
		return ""
	}
	qctx, cancel := context.WithTimeout(ctx, firewallApplyTimeout)
	live, err := firewall.Fingerprint(qctx, h.o.Runner)
	cancel()
	var reason string
	switch {
	case err != nil:
		h.log.Warn("cannot list table "+firewall.TableRef+" to check it", dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return ""
	case live == "":
		reason = reassertMissing
	case h.fwLive == "":
		h.fwLive = live // not listed after the apply: this is the table
		return ""
	case live != h.fwLive:
		reason = reassertDrift
	default:
		return ""
	}
	if err := h.applyFirewallLocked(ctx); err != nil {
		return "" // logged and shown as DEY-P019
	}
	return reason
}

// removeFirewall deletes table inet deyroute once security.firewall_managed is
// false (fwApplyMu held). After a switch within this process (config
// apply) the table deyroute applied goes. At the first apply of a hub
// process (atStart) the switch happened while the hub was not running (a
// manual edit and a restart, a restore): a table an earlier hub process
// applied, with its stale @nodes, control-range drop and NAT, is removed
// when it exists; nothing is run or logged otherwise.
func (h *Hub) removeFirewall(ctx context.Context, atStart bool) error {
	rctx, cancel := context.WithTimeout(ctx, firewallApplyTimeout)
	defer cancel()
	if atStart {
		out, err := firewall.Show(rctx, h.o.Runner)
		if (err == nil && out == "") || deyerr.HasCode(err, deyerr.X030) {
			return nil // no table, or no nft to have made one
		}
	}
	if err := firewall.Remove(rctx, h.o.Runner); err != nil {
		h.log.Error("table inet deyroute could not be removed after firewall management was switched off",
			dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return err
	}
	h.log.Warn("firewall management switched off: table inet deyroute removed", dlog.Code(deyerr.P031))
	return nil
}

// metaFirewallNAT is the state.db record of the NAT and masquerade rules of
// the last table the hub applied (natCarry).
const metaFirewallNAT = "firewall/nat"

// natRecord is the metaFirewallNAT record.
type natRecord struct {
	NAT        []backend.NATRule `json:"nat,omitempty"`
	Masquerade []string          `json:"masquerade,omitempty"`
}

// tunnelsReady reports whether the startup reconcile finished (the
// controllers know which candidates run).
func (h *Hub) tunnelsReady() bool {
	select {
	case <-h.tun.ready:
		return true
	default:
		return false
	}
}

// natCarry returns the NAT rules of the last applied table as one extra
// hub side (used until the startup reconcile finished).
func (h *Hub) natCarry() (render.Side, bool) {
	var rec natRecord
	ok, err := h.st.GetMeta(metaFirewallNAT, &rec)
	if err != nil || !ok || (len(rec.NAT) == 0 && len(rec.Masquerade) == 0) {
		return render.Side{}, false
	}
	return render.Side{NAT: rec.NAT, Masquerade: rec.Masquerade}, true
}

// saveNATCarry remembers the NAT rules of an applied table (fwApplyMu held;
// written only when they changed).
func (h *Hub) saveNATCarry(spec firewall.Spec) {
	rec := natRecord{NAT: spec.NAT, Masquerade: spec.Masquerade}
	data, err := json.Marshal(rec)
	if err != nil || string(data) == h.fwCarry {
		return
	}
	if len(rec.NAT) == 0 && len(rec.Masquerade) == 0 {
		err = h.st.DeleteMeta(metaFirewallNAT)
	} else {
		err = h.st.PutMeta(metaFirewallNAT, rec)
	}
	if err != nil {
		h.log.Warn("cannot record the NAT rules of the firewall", dlog.Err(err))
		return
	}
	h.fwCarry = string(data)
}

// firewallManaged is security.firewall_managed (default true).
func firewallManaged(cfg *config.Config) bool {
	return cfg == nil || cfg.Security == nil || cfg.Security.FirewallManaged
}

// Firewall returns the last firewall computation.
func (h *Hub) Firewall() FirewallState {
	h.fwMu.Lock()
	defer h.fwMu.Unlock()
	return FirewallState{Spec: h.fw.spec, Managed: h.fw.managed, Applied: h.fw.applied, Err: h.fw.err, Computed: h.fw.done}
}

package hub

import (
	"context"
	"encoding/json"
	"time"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/render"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/firewall"
	dlog "github.com/localroot4/deyroute/internal/log"
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
// newer one. When firewall_managed was switched off (config apply), the
// table deyroute applied is removed: a stale @nodes restriction would
// otherwise keep blocking new nodes. Until the startup reconcile adopted
// the running candidates, the NAT rules of the table the previous hub
// process applied stay in place (a hub restart never interrupts a NAT
// transport, sections 3 and 5). Errors are logged, remembered for the
// dashboard (DEY-P019) and returned.
func (h *Hub) applyFirewall(ctx context.Context) error {
	h.fwApplyMu.Lock()
	defer h.fwApplyMu.Unlock()
	cfg := h.Config()
	sides := h.activeHubSides()
	if !h.tunnelsReady() {
		if carry, ok := h.natCarry(); ok {
			sides = append(sides, carry)
		}
	}
	spec := render.FirewallSpec(cfg, sides, h.joins.Active(), render.DefaultUnknownControlRate)
	managed := firewallManaged(cfg)
	h.fwMu.Lock()
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
	case !managed && wasManaged && !h.o.DisableFirewall:
		rctx, cancel := context.WithTimeout(ctx, firewallApplyTimeout)
		err = firewall.Remove(rctx, h.o.Runner)
		cancel()
		if err != nil {
			h.log.Error("table inet deyroute could not be removed after firewall management was switched off",
				dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		} else {
			h.log.Warn("firewall management switched off: table inet deyroute removed", dlog.Code(deyerr.P031))
		}
	}
	if managed && err == nil {
		h.saveNATCarry(spec)
	}
	h.fwMu.Lock()
	defer h.fwMu.Unlock()
	h.fw.spec, h.fw.managed, h.fw.done, h.fw.err = spec, managed, true, err
	if err == nil && managed && !h.o.DisableFirewall {
		h.fw.applied = h.now()
	}
	return err
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

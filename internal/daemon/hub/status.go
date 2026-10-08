package hub

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/secrets"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// Status limits.
const (
	// statusEvents is the number of events in Status (section 6: LAST EVENTS).
	statusEvents = 10
	// certWarnTTL caches the certificate expiry warnings.
	certWarnTTL = time.Minute
)

// Client IP visibility of the active transport (section 10).
const (
	ClientIPPreserved = "preserved"
	ClientIPMasked    = "masked"
)

// Firewall modes of api.HubStatus.
const (
	FirewallModeManaged     = "managed"
	FirewallModeSuggestOnly = "suggest-only"
)

// Status implements api.Local: the dashboard (section 6) and `deyroute
// status --json`: hub, tunnels (config + state), nodes (config + live
// registry), the last 10 events and the yellow warnings.
func (l *local) Status(context.Context) (api.Status, error) {
	h := l.h
	cfg := h.Config()
	evs, err := h.st.Events(state.EventFilter{Limit: statusEvents})
	if err != nil {
		return api.Status{}, withLog(err)
	}
	if evs == nil {
		evs = []state.Event{}
	}
	st := api.Status{
		Schema:      api.JSONSchemaVersion,
		Role:        config.RoleHub,
		Version:     version.Version,
		GeneratedAt: h.now(),
		Hub:         h.hubStatus(cfg),
		Tunnels:     h.tunnelInfos(cfg),
		Nodes:       h.nodeInfos(cfg),
		Events:      evs,
	}
	st.Warnings = h.warnings(cfg)
	return st, nil
}

// NodeList implements api.Local: every joined node with its live state.
func (l *local) NodeList(context.Context) ([]api.NodeInfo, error) {
	return l.h.nodeInfos(l.h.Config()), nil
}

// hubStatus is the hub part of Status.
func (h *Hub) hubStatus(cfg *config.Config) *api.HubStatus {
	hs := &api.HubStatus{
		Name:        cfg.Hub.Name,
		PublicIP:    cfg.Hub.PublicIP,
		ControlPort: cfg.Hub.ControlPort,
		Domain:      cfg.Hub.Domain,
		UIMode:      firstNonEmpty(cfg.Hub.UIMode, config.DefaultUIMode),
		Language:    firstNonEmpty(cfg.Hub.Language, "en"),
		Firewall:    FirewallModeManaged,
	}
	if !firewallManaged(cfg) {
		hs.Firewall = FirewallModeSuggestOnly
	}
	hs.Front = h.frontStatus(cfg)
	hs.ACMEChallenge = acmeChallenge(cfg)
	if a := cfg.Hub.ACME; a != nil {
		hs.ACMEEmail = a.Email
	}
	if tg := cfg.Hub.Notify.Telegram; tg.Enabled || tg.ChatID != "" {
		hs.Telegram = api.TelegramStatus{Enabled: tg.Enabled, ChatID: tg.ChatID, TokenFile: tg.BotTokenFile,
			Events: append([]string(nil), tg.Events...)}
	}
	return hs
}

// nodeInfos returns the nodes of cfg (config order) with their live state.
func (h *Hub) nodeInfos(cfg *config.Config) []api.NodeInfo {
	out := make([]api.NodeInfo, 0, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		ns, _ := h.nodeState(n.ID)
		out = append(out, api.NodeInfo{
			ID:            n.ID,
			Name:          n.Name,
			PublicIP:      n.PublicIP,
			Online:        ns.Online,
			ControlRTTms:  ns.ControlRTTms,
			Version:       ns.AgentVersion,
			Compatible:    ns.Compatible,
			CPUPercent:    ns.CPUPercent,
			RAMBytes:      ns.RAMBytes,
			LastHeartbeat: ns.LastHeartbeat,
			Country:       ns.Country,
			UDPOK:         ns.UDPOK,
			Tags:          append([]string(nil), n.Tags...),
			Fingerprint:   n.CertFingerprint,
			Tunnels:       cfg.TunnelsUsingNode(n.ID),
			Route:         n.Route,
			Via:           h.nodeVia(n.ID),
			LastError:     ns.LastError,
		})
	}
	return out
}

// tunnelInfos returns the tunnels of cfg (config order) with their state.
func (h *Hub) tunnelInfos(cfg *config.Config) []api.TunnelInfo {
	out := make([]api.TunnelInfo, 0, len(cfg.Tunnels))
	for i := range cfg.Tunnels {
		out = append(out, h.tunnelInfo(cfg, &cfg.Tunnels[i]))
	}
	return out
}

// supports is the protocol filter of config.ResolveLadder.
func supports(id, proto string) bool {
	_, tr, err := backend.Lookup(id)
	return err == nil && tr.Supports(proto)
}

// tunnelInfo is one dashboard row: configuration plus the tunnel state.
func (h *Hub) tunnelInfo(cfg *config.Config, t *config.Tunnel) api.TunnelInfo {
	ts, known := h.tunnelState(t.ID)
	ti := api.TunnelInfo{
		ID:         t.ID,
		Name:       t.Name,
		Enabled:    t.Enabled,
		State:      state.StateInit,
		Nodes:      append([]string{}, t.Nodes...),
		LadderName: t.Ladder.Name,
		Policy:     firstNonEmpty(t.Failover.Policy, config.DefaultPolicy),
		Ports:      make([]api.PortMapDTO, 0, len(t.Ports)),
	}
	ti.ProxyProtocol = t.Advanced != nil && t.Advanced.ProxyProtocol
	for _, p := range t.Ports {
		ti.Ports = append(ti.Ports, api.PortMapDTO{Listen: p.Listen, Proto: p.Proto, Target: p.Target, Probe: p.Probe})
	}
	if ladder, err := cfg.ResolveLadder(t, supports); err == nil {
		ti.Ladder = ladder
	} else {
		ti.Ladder = []string{}
	}
	if known && ts.State != "" {
		ti.State = ts.State
	}
	if !t.Enabled {
		ti.State = state.StateDisabled
	}
	if known {
		ti.ActiveNode = ts.Active.Node
		ti.ActiveTransport = ts.Active.Transport
		ti.RTTms = ts.LastRTTms
		ti.UpSince = ts.UpSince
		ti.Paused = ts.Paused
		ti.ServiceDown = ts.ServiceDown
		for _, key := range sortedKeys(ts.Skipped) {
			sk := ts.Skipped[key]
			ti.Warnings = append(ti.Warnings, key+": "+firstNonEmpty(sk.Reason, sk.Code))
		}
	}
	if c := h.tun.lookup(t.ID); c != nil && t.Enabled {
		ti.Warnings = append(ti.Warnings, c.warnings()...)
	}
	if n, ok := cfg.NodeByID(ti.ActiveNode); ok {
		ti.ActiveNodeName = n.Name
	}
	// From memory only: Status never reads state.db for traffic.
	ti.Traffic = h.traffic.tunnelNow(t.ID, h.now())
	if ti.ActiveTransport != "" {
		ti.ClientIP = ClientIPMasked
		if _, tr, err := backend.Lookup(ti.ActiveTransport); err == nil && tr.ClientIPPreserved &&
			t.Advanced != nil && t.Advanced.ProxyProtocol {
			ti.ClientIP = ClientIPPreserved
		}
	}
	return ti
}

// warnings are the yellow dashboard lines: incompatible node versions
// (DEY-N004 with the update suggestion), certificates expiring within 14
// days (T006/T001), rungs skipped from a ladder, and the firewall state
// (not managed: P031; last apply failed: P019).
func (h *Hub) warnings(cfg *config.Config) []api.Warning {
	var out []api.Warning
	for _, n := range cfg.Nodes {
		ns, _ := h.nodeState(n.ID)
		if ns.AgentVersion == "" || ns.Compatible {
			continue
		}
		e := deyerr.New(deyerr.N004, deyerr.Params{"node": n.ID, "node_version": ns.AgentVersion, "hub_version": version.Version})
		out = append(out, api.Warning{Code: string(e.Code), Message: e.Message() + "; " + e.Fix(), Node: n.ID})
	}
	out = append(out, h.certWarnings(cfg)...)
	for _, t := range cfg.Tunnels {
		ts, ok := h.tunnelState(t.ID)
		if !ok {
			continue
		}
		if ts.Paused && t.Enabled {
			e := deyerr.New(deyerr.F004, deyerr.Params{"tunnel": t.ID})
			out = append(out, api.Warning{Code: string(e.Code), Message: e.Message() + "; " + e.Fix(), Tunnel: t.ID})
		}
		for _, key := range sortedKeys(ts.Skipped) {
			sk := ts.Skipped[key]
			c, err := parseCandidateKey(key)
			if err != nil {
				continue
			}
			msg := c.Transport + " on " + c.Node + " is skipped"
			if sk.Reason != "" {
				msg += ": " + sk.Reason
			}
			out = append(out, api.Warning{Code: sk.Code, Message: msg, Tunnel: t.ID, Node: c.Node})
		}
	}
	out = append(out, h.updateWarnings()...)
	fw := h.Firewall()
	switch {
	case !firewallManaged(cfg):
		e := deyerr.New(deyerr.P031, nil)
		out = append(out, api.Warning{Code: string(e.Code), Message: e.Message() + "; " + e.Fix()})
	case fw.Err != nil:
		e := deyerr.As(fw.Err)
		out = append(out, api.Warning{Code: string(e.Code), Message: e.Message() + "; " + e.Fix()})
	}
	return out
}

// parseCandidateKey splits a "node/backend/transport" key (state.Candidate.Key).
func parseCandidateKey(key string) (state.Candidate, error) {
	node, tr, ok := strings.Cut(key, "/")
	if !ok || node == "" || tr == "" {
		return state.Candidate{}, deyerr.Plain("invalid candidate key")
	}
	return state.Candidate{Node: node, Transport: tr}, nil
}

// certWarnings checks the CA, the hub certificate and every tunnel
// certificate for expiry within 14 days (section 10); cached for a minute.
func (h *Hub) certWarnings(cfg *config.Config) []api.Warning {
	now := h.now()
	h.warnMu.Lock()
	defer h.warnMu.Unlock()
	if !h.certAt.IsZero() && now.Sub(h.certAt) < certWarnTTL && now.After(h.certAt) {
		return append([]api.Warning(nil), h.certWarns...)
	}
	var out []api.Warning
	check := func(label, file, tunnel string) {
		data, err := os.ReadFile(h.path(file)) // #nosec G304 -- certificate paths below Root
		if err != nil {
			return
		}
		cert, err := tlsutil.ParseCert(data)
		if err != nil {
			return
		}
		if w := tlsutil.CheckExpiry(label, cert, now); w != nil {
			e := deyerr.As(w)
			out = append(out, api.Warning{Code: string(e.Code), Message: e.Message(), Tunnel: tunnel})
		}
	}
	check(filepath.Join(config.SecretsDir, setup.FileCACert), filepath.Join(config.SecretsDir, setup.FileCACert), "")
	check(filepath.Join(config.SecretsDir, setup.FileHubCert), filepath.Join(config.SecretsDir, setup.FileHubCert), "")
	for _, t := range cfg.Tunnels {
		var file string
		switch t.TLS.Mode {
		case config.TLSModeCustom:
			file = t.TLS.CertFile
		case config.TLSModeACME:
			file = filepath.Join(config.SecretsDir, secrets.TLSDir, t.ID, secrets.ACMEDir, secrets.CertFile)
			if !regularFile(h.path(file)) {
				file = filepath.Join(config.SecretsDir, secrets.TLSDir, t.ID, secrets.CertFile)
			}
		default:
			file = filepath.Join(config.SecretsDir, secrets.TLSDir, t.ID, secrets.CertFile)
		}
		if file != "" {
			check(file, file, t.ID)
		}
	}
	h.certWarns, h.certAt = out, now
	return append([]api.Warning(nil), out...)
}

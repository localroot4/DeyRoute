package hub

import (
	"context"
	stderrors "errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	dlog "github.com/localroot4/deyroute/internal/log"
	"github.com/localroot4/deyroute/internal/state"
	"github.com/localroot4/deyroute/internal/tlsutil"
	"github.com/localroot4/deyroute/internal/version"
)

// controlHandler adapts the Hub to api.ControlHandler.
type controlHandler struct{ h *Hub }

var _ api.ControlHandler = controlHandler{}

// Join implements api.ControlHandler.
func (c controlHandler) Join(ctx context.Context, req api.JoinRequest, remoteIP string) (api.JoinResponse, error) {
	return c.h.join(ctx, req, remoteIP)
}

// Authenticate implements api.ControlHandler.
func (c controlHandler) Authenticate(nodeID, certFingerprint, remoteIP string) error {
	return c.h.authenticate(nodeID, certFingerprint, remoteIP)
}

// Session implements api.ControlHandler.
func (c controlHandler) Session(s *api.Session) { c.h.serveSession(s) }

// Upload implements api.ControlHandler.
func (c controlHandler) Upload(ctx context.Context, id, nodeID string, body io.Reader) error {
	return c.h.upload(ctx, id, nodeID, body)
}

// Asset implements api.ControlHandler.
func (c controlHandler) Asset(ctx context.Context, arch string) (api.AssetInfo, error) {
	return c.h.asset(ctx, arch)
}

// errNoChange aborts a config mutation that would not change anything.
var errNoChange = deyerr.Plain("hub: no change")

// defaultNodeBase is the id stem of a node whose hostname gives no usable
// slug.
const defaultNodeBase = "node"

// join handles POST /v1/join (section 3, Join 1-4; section 11): the
// one-time token is consumed first (with the per-IP failure limiter: DEY-
// N001 / N007), then the node id is chosen (requested: C007 invalid, N010
// taken; otherwise derived from the hostname and made unique), the CSR is
// signed for 10 years with CN = node id, the node is added to config.yaml
// with its public IP and certificate fingerprint, and the firewall is
// re-applied (the node IP joins @nodes; the join window closes when no
// token remains). The token is spent even when a later step fails.
func (h *Hub) join(_ context.Context, req api.JoinRequest, remoteIP string) (api.JoinResponse, error) {
	ip := normalizeIP(remoteIP)
	if err := h.joins.Consume(req.Token, ip); err != nil {
		return api.JoinResponse{}, err
	}
	requested := strings.TrimSpace(req.NodeID)
	if requested != "" && !config.ValidNodeID(requested) {
		return api.JoinResponse{}, deyerr.New(deyerr.C007, deyerr.Params{"kind": "node", "id": cleanName(requested)})
	}
	if _, err := h.autoBackup(); err != nil {
		h.log.Warn("joining without an automatic backup", dlog.Err(err))
	}
	var (
		nodeID  string
		certPEM []byte
	)
	cfg, err := h.mutate(func(c *config.Config) error {
		nodeID = requested
		if nodeID != "" {
			if _, taken := c.NodeByID(nodeID); taken {
				return deyerr.New(deyerr.N010, deyerr.Params{"node": nodeID})
			}
		} else {
			nodeID = config.UniqueID(nodeBase(req.Hostname), func(id string) bool {
				_, taken := c.NodeByID(id)
				return taken || !config.ValidNodeID(id)
			})
			if nodeID == "" {
				return deyerr.New(deyerr.C007, deyerr.Params{"kind": "node", "id": cleanName(req.Hostname)})
			}
		}
		signed, err := h.currentCA().SignCSR([]byte(req.CSRPEM), nodeID, api.NodeCertValidity)
		if err != nil {
			return err
		}
		cert, err := tlsutil.ParseCert(signed)
		if err != nil {
			return err
		}
		certPEM = signed
		name := cleanName(req.Name)
		if name == "" {
			name = nodeID
		}
		return c.AddNode(config.Node{ID: nodeID, Name: name, PublicIP: ip, CertFingerprint: tlsutil.Fingerprint(cert.Raw)})
	})
	if err != nil {
		return api.JoinResponse{}, err
	}
	h.recordNodeCert(nodeID, certPEM)
	h.nodesMu.Lock()
	nr := h.runtimeLocked(nodeID)
	nr.st.RemoteIP = ip
	nr.st.AgentVersion, nr.st.Arch, nr.st.OS = req.Version, req.Arch, req.OS
	nr.st.Compatible = version.Compatible(version.Version, req.Version)
	h.nodesMu.Unlock()
	h.requestFirewall()
	h.log.Info("node joined", dlog.Node(nodeID), slog.String("remote_ip", ip),
		slog.String("version", req.Version), slog.String("arch", req.Arch))
	return api.JoinResponse{
		NodeID:     nodeID,
		CertPEM:    string(certPEM),
		CAPEM:      string(h.trustPEM()),
		HubName:    cfg.Hub.Name,
		HubVersion: version.Version,
		HubAddr:    net.JoinHostPort(cfg.Hub.PublicIP, strconv.Itoa(cfg.Hub.ControlPort)),
		PublicIP:   ip,
	}, nil
}

// authenticate checks every non-join Control API request: the certificate
// CN must name a node of config.yaml whose cert_fingerprint matches
// (otherwise DEY-N008: unknown, removed or re-joined node). A node that
// connects from a new public IP is updated in config.yaml, @nodes follows
// and node_ip_changed is emitted (section 11, S27).
func (h *Hub) authenticate(nodeID, certFingerprint, remoteIP string) error {
	n, ok := h.Config().NodeByID(nodeID)
	if !ok {
		return deyerr.New(deyerr.N008, deyerr.Params{"node": nodeID})
	}
	if !sameFingerprint(n.CertFingerprint, certFingerprint) {
		// rotate-ca: the node may come back with the certificate the hub
		// just signed for it before config.yaml records it.
		if !h.acceptPendingCert(nodeID, certFingerprint) {
			return deyerr.New(deyerr.N008, deyerr.Params{"node": nodeID})
		}
		if n, ok = h.Config().NodeByID(nodeID); !ok {
			return deyerr.New(deyerr.N008, deyerr.Params{"node": nodeID})
		}
	}
	ip := normalizeIP(remoteIP)
	if ip != "" && !sameIP(n.PublicIP, ip) {
		h.nodeIPChanged(nodeID, ip)
	}
	return nil
}

// nodeIPChanged records the new public IP of a node (once, even when
// several requests from the new address arrive together).
func (h *Hub) nodeIPChanged(id, ip string) {
	var old string
	_, err := h.mutate(func(c *config.Config) error {
		n, ok := c.NodeByID(id)
		if !ok {
			return deyerr.New(deyerr.N008, deyerr.Params{"node": id})
		}
		if sameIP(n.PublicIP, ip) {
			return errNoChange
		}
		old, n.PublicIP = n.PublicIP, ip
		return nil
	})
	if stderrors.Is(err, errNoChange) {
		return
	}
	if err != nil {
		h.log.Error("cannot record the new IP of a node", dlog.Node(id), slog.String("remote_ip", ip),
			dlog.Err(err), dlog.Code(deyerr.As(err).Code))
		return
	}
	h.setNodeIP(id, ip)
	h.requestFirewall()
	h.log.Warn("node public IP changed", dlog.Node(id), slog.String("old_ip", old), slog.String("new_ip", ip))
	h.Emit(state.Event{
		Type: state.EvNodeIPChanged, Level: state.LevelWarn, Node: id,
		Reason:  "control connection from " + ip,
		Message: "node " + id + " public IP changed from " + old + " to " + ip,
	})
}

// normalizeIP returns the canonical text of an address (IPv4-mapped IPv6
// unmapped, port and zone dropped); "" when s is not an address.
func normalizeIP(s string) string {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return ""
	}
	return a.Unmap().WithZone("").String()
}

// sameIP compares two addresses canonically.
func sameIP(a, b string) bool {
	na, nb := normalizeIP(a), normalizeIP(b)
	return na != "" && na == nb
}

// sameFingerprint compares two certificate fingerprints canonically.
func sameFingerprint(a, b string) bool {
	na, ok1 := tlsutil.NormalizeFingerprint(a)
	nb, ok2 := tlsutil.NormalizeFingerprint(b)
	return ok1 && ok2 && na == nb
}

// nodeBase derives the id stem of a node from its hostname.
func nodeBase(hostname string) string {
	hn := strings.TrimSpace(hostname)
	if i := strings.IndexByte(hn, '.'); i > 0 {
		hn = hn[:i]
	}
	slug := config.Slugify(hn)
	if slug == config.FallbackID && !strings.Contains(strings.ToLower(hn), config.FallbackID) {
		return defaultNodeBase
	}
	if !config.ValidNodeID(slug) {
		return defaultNodeBase
	}
	return slug
}

// cleanName makes a display name safe for config.yaml: printable
// characters only, at most config.MaxNameLen runes.
func cleanName(s string) string {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if !unicode.IsPrint(r) {
			continue
		}
		if n == config.MaxNameLen {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

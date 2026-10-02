package node

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/front"
	dlog "github.com/localroot4/deyroute/internal/log"
)

// Front mode on the node: the control channel (and uploads, asset and
// self-update downloads, which use the same api client) reaches the hub
// through the hub's CDN front instead of a direct TCP connection. The mode
// is node.front.secret_file != ""; everything it needs is re-read from
// config.yaml on every (re)connect, so set-hub, a new edge_ip or a rotated
// secret apply without a restart.

// ConnectivitySection is the name of the node doctor section that reports
// how the node reaches the hub.
const ConnectivitySection = "connectivity"

// dialFailure is the last failed attempt to open the front.
type dialFailure struct {
	err    *deyerr.Error
	reason string // one line, never the path or the secret
	status int    // HTTP status the front answered, 0 if none
	at     time.Time
}

// permanentError marks a configuration problem that retrying cannot fix: the
// control client backs off slowly and logs it once (api.RetryHinter).
type permanentError struct{ err error }

func (e *permanentError) Error() string             { return e.err.Error() }
func (e *permanentError) Unwrap() error             { return e.err }
func (e *permanentError) RetryAfter() time.Duration { return 0 }
func (e *permanentError) Permanent() bool           { return true }

var _ api.RetryHinter = (*permanentError)(nil)

// frontSettings returns node.front as currently stored in config.yaml (so a
// set-hub or an edited edge_ip takes effect on the next reconnect), else
// the last known value.
func (a *agent) frontSettings() config.NodeFront {
	cfg, err := config.Load(a.cfgPath)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil && cfg.Node != nil {
		a.front = cfg.Node.Front
	}
	return a.front
}

// frontMode reports whether the node was last seen in front mode (cheap,
// no file access: status and the heartbeat use it).
func (a *agent) frontMode() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.front.SecretFile != ""
}

// applyFront gives c the front dial when this node is in front mode. The
// dial is rebuilt from the current configuration on every connect; in direct
// mode c is left exactly as it was (plain TCP, the default open timeout).
func (a *agent) applyFront(c *api.ControlClient) {
	if a.frontSettings().SecretFile == "" {
		return
	}
	c.DialFunc = a.frontDial
}

// frontDial is api.ControlClient.DialFunc: the dial hook for the next
// connect, nil when the node is (no longer) in front mode.
func (a *agent) frontDial() func(ctx context.Context) (net.Conn, error) {
	fs := a.frontSettings()
	if fs.SecretFile == "" {
		return nil
	}
	t, err := a.frontTarget(fs)
	if err != nil {
		// A broken configuration is reported by the dial itself, so it shows
		// up as the connection error, once, and the node keeps retrying
		// slowly (the file may be fixed any moment).
		perm := &permanentError{err: err}
		return func(context.Context) (net.Conn, error) {
			a.noteDial(perm)
			return nil, perm
		}
	}
	dial := setup.DialFront(t, a.o.FrontDial)
	return func(ctx context.Context) (net.Conn, error) {
		conn, err := dial(ctx)
		a.noteDial(err)
		return conn, err
	}
}

// frontTarget builds the dial target from node.hub_addr ("DOMAIN:PORT"),
// node.front and the secret file. Errors never contain the secret.
func (a *agent) frontTarget(fs config.NodeFront) (front.Target, error) {
	secret, err := a.readFrontSecret(fs.SecretFile)
	if err != nil {
		return front.Target{}, err
	}
	addr := a.hubAddress()
	t, err := front.NewTarget(addr, fs.Scheme, fs.EdgeIP, secret)
	if err != nil {
		return front.Target{}, deyerr.Wrap(deyerr.C013, err, deyerr.Params{
			"field": "node.hub_addr", "value": addr,
			"allowed": "DOMAIN:PORT of the front (a Cloudflare port, or set node.front.scheme)",
		})
	}
	return t, nil
}

// readFrontSecret reads the path secret (a file name below the secrets
// directory) and registers it with the log redactor.
func (a *agent) readFrontSecret(name string) (string, error) {
	p := filepath.Join(config.SecretsDir, name)
	data, err := os.ReadFile(a.path(p)) // #nosec G304 -- a plain file name from config.yaml below Root/etc/deyroute/secrets
	if err != nil {
		return "", deyerr.Wrap(deyerr.T008, err, deyerr.Params{"path": p, "reason": err.Error()})
	}
	secret := strings.TrimSpace(string(data))
	if !front.ValidSecret(secret) {
		return "", deyerr.New(deyerr.T008, deyerr.Params{"path": p,
			"reason": "the file does not hold a front path secret (letters, digits, - and _)"})
	}
	dlog.RegisterSecret(secret)
	return secret, nil
}

// registerFrontSecret registers the secret with the log redactor at start,
// before any line can mention it. A missing file is reported by the dial.
func (a *agent) registerFrontSecret() {
	if fs := a.frontSettings(); fs.SecretFile != "" {
		_, _ = a.readFrontSecret(fs.SecretFile)
	}
}

// noteDial records the outcome of opening the front for the status, the
// heartbeat and the doctor: a failure (N016/N017) is kept until the next
// success.
func (a *agent) noteDial(err error) {
	if err == nil {
		a.mu.Lock()
		// The heartbeat must not keep reporting the failure that just ended.
		if f := a.dialFail; f != nil && a.lastErrorCode == f.err.Code {
			a.lastError, a.lastErrorCode = "", ""
		}
		a.dialFail = nil
		a.mu.Unlock()
		return
	}
	f := &dialFailure{err: deyerr.As(err), at: a.o.Now().UTC()}
	if de := front.AsDialError(err); de != nil {
		f.reason, f.status = de.Reason, de.Status
	} else {
		var pe *permanentError
		if errors.As(err, &pe) {
			f.reason = deyerr.As(pe.err).Message()
		}
	}
	a.mu.Lock()
	a.dialFail = f
	a.mu.Unlock()
	a.setLastError(err)
}

// warning is the status warning for a failed front dial: the DEY code
// and one line, no path, no secret.
func (f *dialFailure) warning(node string) api.Warning {
	msg := f.err.Message()
	if f.reason != "" {
		msg += " (" + f.reason + ")"
	}
	return api.Warning{Code: string(f.err.Code), Message: dlog.Redact(msg), Node: node}
}

// connectivitySection is the node doctor section "connectivity": how the
// node reaches the hub and why it cannot, without the secret path.
func (a *agent) connectivitySection() string {
	st := a.status()
	var b strings.Builder
	ns := st.NodeSelf
	if ns == nil {
		return ""
	}
	fmt.Fprintf(&b, "hub: %s\n", ns.HubAddr)
	if ns.Front {
		fs := a.frontSettings()
		scheme := fs.Scheme
		if scheme == "" {
			scheme = "from the port"
		}
		fmt.Fprintf(&b, "route: via front (scheme: %s)\n", scheme)
		if fs.EdgeIP != "" {
			fmt.Fprintf(&b, "edge_ip: %s\n", fs.EdgeIP)
		}
	} else {
		b.WriteString("route: direct\n")
	}
	fmt.Fprintf(&b, "connected: %t\n", ns.Connected)
	a.mu.Lock()
	f := a.dialFail
	a.mu.Unlock()
	if f != nil {
		line := string(f.err.Code)
		if f.status > 0 {
			line += fmt.Sprintf(" HTTP %d", f.status)
		}
		fmt.Fprintf(&b, "last dial error: %s at %s\n", line, f.at.Format(time.RFC3339))
		if f.reason != "" {
			fmt.Fprintf(&b, "reason: %s\n", f.reason)
		}
	} else if ns.Front {
		b.WriteString("last dial error: none\n")
	}
	return dlog.Redact(b.String())
}

// logHubChange logs a hub address change without the secret of a front
// target.
func (a *agent) logHubChange(t setup.HubTarget) {
	if t.Front {
		a.log.Info("hub address changed; the node now connects through the front; reconnecting", slog.String("hub", t.Addr))
		return
	}
	a.log.Info("hub address changed; reconnecting", slog.String("hub", t.Addr))
}

package direct

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// interfaceAddrs lists the local addresses (replaced in tests).
var interfaceAddrs = net.InterfaceAddrs

// runCheck is the node half of direct/haproxy: it verifies that every
// target answers on an address the hub can reach (the node's public IP or,
// for 0.0.0.0/:: targets, any non-loopback local address) and returns
// DEY-B062 for the first one that does not.
func runCheck(ctx context.Context, cfg *RelayConfig, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	dialer := net.Dialer{Timeout: cfg.DialTimeout()}
	for _, p := range cfg.Ports {
		host, port, _ := backend.SplitTarget(p.Target)
		candidates := checkCandidates(cfg.NodeIP, host, port)
		reached := ""
		for _, addr := range candidates {
			if ctx.Err() != nil {
				return deyerr.Wrap(deyerr.B062, ctx.Err(), deyerr.Params{"target": p.Target, "tried": strings.Join(candidates, ", ")})
			}
			c, err := dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				logger.Debug("haproxy check: no answer", "addr", addr, "err", err.Error())
				continue
			}
			_ = c.Close()
			reached = addr
			break
		}
		if reached == "" {
			return deyerr.New(deyerr.B062, deyerr.Params{"target": p.Target, "tried": strings.Join(candidates, ", ")})
		}
		logger.Info("haproxy check: service reachable", "target", p.Target, "addr", reached)
	}
	return nil
}

// checkCandidates returns the addresses to try for a target, the node's
// public address first.
func checkCandidates(nodeIP, host string, port int) []string {
	out := []string{net.JoinHostPort(nodeIP, strconv.Itoa(port))}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsUnspecified() {
		return out
	}
	// The public IP is not always configured on an interface (1:1 NAT);
	// a service bound to the unspecified address answers on every local
	// non-loopback address too.
	v4only := ip.To4() != nil
	addrs, err := interfaceAddrs()
	if err != nil {
		return out
	}
	seen := map[string]bool{out[0]: true}
	for _, a := range addrs {
		pfx, ok := a.(*net.IPNet)
		if !ok || pfx.IP.IsLoopback() || pfx.IP.IsLinkLocalUnicast() || pfx.IP.IsMulticast() {
			continue
		}
		if v4only && pfx.IP.To4() == nil {
			continue
		}
		addr := net.JoinHostPort(pfx.IP.String(), strconv.Itoa(port))
		if !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	return out
}

// checkTimeout bounds the whole check run.
const checkTimeout = 60 * time.Second

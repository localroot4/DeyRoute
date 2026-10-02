package setup

import (
	"context"
	"errors"
	"net"

	"github.com/localroot4/deyroute/internal/api"
	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/front"
)

// FrontDialFunc opens the control connection through the front t (tests
// replace front.DialControl with a dialer that trusts a fake CDN).
type FrontDialFunc func(ctx context.Context, t front.Target) (net.Conn, error)

// DialFront returns the dial hook (api.ControlClient.Dial, api.JoinVia) for
// the front target t: dial (nil = front.DialControl) with every failure
// turned into DEY-N016 (the front could not be reached) or DEY-N017 (it did
// not accept the node). The *front.DialError stays reachable with
// errors.As, so the control client sees its Retry-After and Permanent
// hints. The target's secret never appears in an error.
func DialFront(t front.Target, dial FrontDialFunc) func(ctx context.Context) (net.Conn, error) {
	if dial == nil {
		dial = front.DialControl
	}
	return func(ctx context.Context) (net.Conn, error) {
		conn, err := dial(ctx, t)
		if err != nil {
			return nil, FrontError(t, err)
		}
		return conn, nil
	}
}

// FrontError maps a failed front dial to DEY-N016/N017. A *front.DialError
// converts with its own classification; any other error is DEY-N016.
func FrontError(t front.Target, err error) error {
	if de := front.AsDialError(err); de != nil {
		return de.ToDEY()
	}
	if deyerr.As(err).Code != deyerr.X000 {
		return err // already a catalog error
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	reason := "the connection to the front failed"
	return deyerr.Wrap(deyerr.N016, err, deyerr.Params{"addr": t.Addr(), "reason": reason})
}

// linkScheme is the scheme a join link asks for: "wss" or "ws" with ?tls=1
// or ?tls=0, "" to derive it from the port.
func linkScheme(l api.JoinLink) string {
	switch {
	case l.TLS == nil:
		return ""
	case *l.TLS:
		return config.FrontSchemeWSS
	}
	return config.FrontSchemeWS
}

// linkTarget is a front link turned into a dial target.
type linkTarget struct {
	target front.Target
	dial   FrontDialFunc
}

// frontTarget builds the front.Target of a front link. A link whose port is
// not a Cloudflare port and has no ?tls= cannot name a scheme: DEY-N006.
func frontTarget(l api.JoinLink, dial FrontDialFunc) (linkTarget, error) {
	t, err := front.NewTarget(l.Addr(), linkScheme(l), "", l.Secret)
	if err != nil {
		return linkTarget{}, deyerr.Wrap(deyerr.N006, err, nil)
	}
	return linkTarget{target: t, dial: dial}, nil
}

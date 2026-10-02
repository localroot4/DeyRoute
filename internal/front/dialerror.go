package front

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Error classes of a DialError.
const (
	ClassDial     = "dial"     // DNS, TCP connect, a connection lost or timed out before an answer
	ClassTLS      = "tls"      // the outer TLS handshake (certificate, clock, middlebox)
	ClassStatus   = "status"   // the front answered an HTTP status other than 101
	ClassProtocol = "protocol" // an answer that is not a valid WebSocket upgrade
)

// maxRetryAfter bounds a Retry-After hint; callers cap it further.
const maxRetryAfter = 24 * time.Hour

// DialError is the failure of DialControl. It never contains the path secret.
type DialError struct {
	Class  string // ClassDial, ClassTLS, ClassStatus or ClassProtocol
	Status int    // the HTTP status for ClassStatus (and ClassProtocol when one was read), else 0
	Reason string // one line for the owner: what is wrong and what to check
	Addr   string // the front, host:port
	// CFCode is the Cloudflare "Error 1xxx" code found in the body of the answer (0 = none).
	CFCode int
	// Challenge is set when the answer carried a cf-mitigated header.
	Challenge bool

	retry time.Duration
	Err   error // the underlying error, for errors.Is/As (context.Canceled, a net error, an x509 error)
}

// Error returns one line: the front address, the class and the reason.
func (e *DialError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("front %s: HTTP %d: %s", e.Addr, e.Status, e.Reason)
	}
	return fmt.Sprintf("front %s: %s: %s", e.Addr, e.Class, e.Reason)
}

// Unwrap returns the underlying error.
func (e *DialError) Unwrap() error { return e.Err }

// RetryAfter returns the delay the front asked for (Retry-After), 0 when it
// did not.
func (e *DialError) RetryAfter() time.Duration { return e.retry }

// Permanent reports whether retrying with the same settings is pointless: a
// 404 (wrong path, front off), a challenge (403 or 503 with cf-mitigated),
// 400 or 426 (WebSockets disabled) and the redirects 301/302/307/308. The
// reconnect loop uses a long delay and a single warning for these.
func (e *DialError) Permanent() bool {
	if e.Class != ClassStatus {
		return false
	}
	switch e.Status {
	case http.StatusNotFound, http.StatusBadRequest, http.StatusUpgradeRequired,
		http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	case http.StatusForbidden, http.StatusServiceUnavailable:
		return e.Challenge
	}
	return false
}

// ToDEY converts the error to the catalog error: DEY-N017 when the front
// answered (a status, or an answer that is not an upgrade), DEY-N016 when it
// could not be reached. The DialError stays reachable with errors.As.
func (e *DialError) ToDEY() error {
	if e.Class == ClassStatus || (e.Class == ClassProtocol && e.Status > 0) {
		return deyerr.Wrap(deyerr.N017, e, deyerr.Params{"addr": e.Addr, "status": e.Status, "reason": e.Reason})
	}
	return deyerr.Wrap(deyerr.N016, e, deyerr.Params{"addr": e.Addr, "reason": e.Reason})
}

// AsDialError returns the *DialError inside err, nil if there is none.
func AsDialError(err error) *DialError {
	var de *DialError
	if errors.As(err, &de) {
		return de
	}
	return nil
}

// ---------------------------------------------------------------------------
// Reasons

// cloudflareCodes explains the "Error 1xxx" pages that matter here.
var cloudflareCodes = map[int]string{
	1001: "DNS resolution error: the domain is not served by Cloudflare or its record is wrong",
	1003: "direct IP access is not allowed: the request must use the proxied domain name",
	1004: "the host is not configured to serve web traffic through Cloudflare",
	1006: "access denied: this address is banned",
	1007: "access denied: this address is banned",
	1008: "access denied: this address is banned",
	1010: "the browser integrity check blocked the request: turn off Browser Integrity Check for this host or add a WAF skip rule",
	1012: "access denied by a Cloudflare rule",
	1015: "rate limited by Cloudflare",
	1016: "origin DNS error: the proxied record points to a name that does not resolve, or to another proxied host",
	1020: "access denied by a WAF or firewall rule: add a skip rule for the front path",
	1033: "tunnel error: the record points to a tunnel that is not running",
}

var cfCodeRe = regexp.MustCompile(`(?i)error(?:\s+code)?[:\s]+(1[0-9]{3})\b`)

// cfErrorCode extracts "Error 1xxx" from the start of an error page.
func cfErrorCode(body []byte) int {
	m := cfCodeRe.FindSubmatch(body)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// statusReason is the owner-facing reason of a non-101 answer.
func statusReason(status int, challenge bool, cfCode int, retry time.Duration) string {
	var s string
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		s = "redirected: a Cloudflare redirect rule or Always Use HTTPS rewrites the request; turn it off for this host and port"
	case http.StatusBadRequest, http.StatusUpgradeRequired:
		s = "WebSockets are refused: turn on Network -> WebSockets for the zone in Cloudflare"
	case http.StatusForbidden, http.StatusServiceUnavailable:
		switch {
		case challenge:
			s = "blocked by a Cloudflare challenge (WAF rule, Bot Fight Mode, Security Level or Browser Integrity Check): add a skip rule for the front path"
		case status == http.StatusForbidden:
			s = "forbidden: a firewall or WAF in front of the hub refused the request"
		default:
			s = "service unavailable"
		}
	case http.StatusNotFound:
		s = "not found: the secret path in the join link is wrong, the hub front is off, or the domain does not lead to this hub"
	case http.StatusTooManyRequests:
		s = "rate limited by Cloudflare"
	case 520:
		s = "the hub front closed the connection or sent an invalid answer (Cloudflare 520)"
	case 521:
		s = "the hub front refused the connection (Cloudflare 521): it is not running or the firewall blocks Cloudflare"
	case 522:
		s = "the connection to the hub timed out (Cloudflare 522): the hub is unreachable from Cloudflare or its firewall drops it"
	case 523:
		s = "the hub is unreachable (Cloudflare 523): the DNS record points to the wrong address"
	case 524:
		s = "the hub accepted the connection but did not answer in time (Cloudflare 524)"
	case 525:
		s = "the TLS handshake with the hub failed (Cloudflare 525): the hub front tls mode does not match the Cloudflare SSL mode (Full needs auto or custom, Flexible needs off)"
	case 526:
		s = "invalid origin certificate (Cloudflare 526): Full (strict) rejects the self-signed front certificate; use SSL mode Full, or tls custom with a Cloudflare Origin CA certificate"
	case 527:
		s = "Cloudflare 527 origin error"
	case 530:
		s = "Cloudflare refused the request (530)"
	default:
		s = "unexpected answer instead of a WebSocket upgrade"
	}
	if cfCode > 0 {
		if hint, ok := cloudflareCodes[cfCode]; ok && (status == 530 || status == http.StatusForbidden || status == http.StatusTooManyRequests) {
			s += fmt.Sprintf(" [Cloudflare error %d: %s]", cfCode, hint)
		} else {
			s += fmt.Sprintf(" [Cloudflare error %d]", cfCode)
		}
	}
	if retry > 0 {
		s += fmt.Sprintf("; retry after %s", retry.Round(time.Second))
	}
	return s
}

// parseRetryAfter parses a Retry-After value (seconds or an HTTP date).
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n < 0 {
			return 0
		}
		if n > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		d = time.Duration(n) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	}
	return min(max(d, 0), maxRetryAfter)
}

// dialReason is the reason for a failure before or around the TCP connect.
func dialReason(err error, host string) string {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &dnsErr):
		if dnsErr.IsTimeout {
			return "the DNS lookup of " + host + " timed out"
		}
		return "the domain " + host + " does not resolve (DNS); set the node's front edge_ip to a Cloudflare address if DNS is filtered"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "the TCP connection was refused: wrong port, or something blocks it"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		return "no route to the front (network unreachable)"
	case errors.Is(err, syscall.ECONNRESET):
		return "the connection was reset (a middlebox may be blocking it)"
	case errors.Is(err, context.DeadlineExceeded), isTimeoutErr(err):
		return "the TCP connection timed out"
	}
	return "the TCP connection failed"
}

func isTimeoutErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// tlsReason is the reason for a failed outer TLS handshake.
func tlsReason(err error, host string) string {
	var inv x509.CertificateInvalidError
	var unk x509.UnknownAuthorityError
	var hn x509.HostnameError
	var recHdr tls.RecordHeaderError
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &inv) && (inv.Reason == x509.Expired):
		return "the front's TLS certificate is expired or not yet valid: check the system clock (date) and that this server's time is correct"
	case errors.As(err, &unk):
		return "the front's TLS certificate is not trusted: the system CA certificates are missing or too old (install ca-certificates), or something intercepts the connection"
	case errors.As(err, &hn):
		return "the front's TLS certificate does not match the domain " + host
	case errors.As(err, &inv):
		return "the front's TLS certificate is invalid"
	case errors.As(err, &recHdr):
		return "the front does not speak TLS on this port: use a ws:// scheme, or a Cloudflare HTTPS port"
	case errors.Is(err, context.DeadlineExceeded), isTimeoutErr(err):
		return "the TLS handshake timed out (a middlebox may be dropping it)"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return "the connection was closed during the TLS handshake (a middlebox may be blocking it)"
	}
	return "the TLS handshake failed"
}

// ctxReason is the reason when the caller's context ended the dial.
func ctxReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return "canceled"
}

// ioReason is the reason for a transport failure while the upgrade request
// is sent or the answer is read.
func ioReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), isTimeoutErr(err):
		return "timed out waiting for the front to answer"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return "the front closed the connection before it answered"
	}
	return "the connection to the front failed"
}

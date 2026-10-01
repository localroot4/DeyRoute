package ports

import (
	"context"
	stderrors "errors"
	"io"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
)

// ssTimeout bounds the `ss` fallback.
const ssTimeout = 5 * time.Second

// BindResult is stage 1 of the port check (section 10): can deyroute bind
// the port on every address, and if not, who holds it.
type BindResult struct {
	Free  bool   `json:"free"`
	Proto string `json:"proto"`
	// Addr is the address the owner is bound to ("0.0.0.0:443", "[::]:443",
	// "127.0.0.1:443"); when the owner is unknown, the address our bind
	// attempt failed on.
	Addr string `json:"addr,omitempty"`
	PID  int    `json:"pid,omitempty"`
	// Process is "nginx (pid 1234)"; empty when the owner is unknown.
	Process string `json:"process,omitempty"`
	// Deyroute is true when the owner is a deyroute process (a deyroute-tun@ unit
	// of the system manager, or the deyroute binary outside any service); only
	// then may the UI offer to stop it. Stopping goes through Unit
	// (systemctl stop); when Unit is empty the process is not a unit and is
	// never killed by PID (the PID may have been reused since the check).
	Deyroute bool `json:"deyroute,omitempty"`
	// Unit is the owner's systemd unit when known.
	Unit string `json:"unit,omitempty"`
}

// Err returns nil when the port is free, DEY-P010 when port or protocol is
// invalid (CheckBind reports those as not free), else DEY-P012 for
// listen/proto with the owner in its Why line.
func (b BindResult) Err(port int) error {
	if b.Free {
		return nil
	}
	if !ValidPort(port) || !ValidProto(b.Proto) {
		return deyerr.New(deyerr.P010, deyerr.Params{"input": strconv.Itoa(port) + "/" + b.Proto})
	}
	proc := b.Process
	if proc == "" {
		proc = "another process"
	}
	addr := b.Addr
	if addr == "" {
		addr = net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	}
	return deyerr.New(deyerr.P012, deyerr.Params{
		"port": strconv.Itoa(port) + "/" + b.Proto, "process": proc, "addr": addr,
	})
}

// Checker runs bind checks. The zero value checks the real system without
// the `ss` fallback; set Runner to enable it.
type Checker struct {
	// Runner runs `ss -Hlntup` when /proc cannot name the owner; nil
	// disables the fallback.
	Runner exec.Runner
	// ProcFS finds the owner of a busy port.
	ProcFS ProcFS

	// listen overrides the bind attempt (tests).
	listen func(network, address string) (io.Closer, error)
}

// CheckBind is Checker{Runner: exec.NewRunner(), ProcFS: ProcFS{Root: "/"}}
// .CheckBind with a background context.
func CheckBind(port int, proto string) BindResult {
	ctx, cancel := context.WithTimeout(context.Background(), ssTimeout)
	defer cancel()
	return Checker{Runner: exec.NewRunner(), ProcFS: ProcFS{Root: "/"}}.CheckBind(ctx, port, proto)
}

// bindTarget is one address family the check binds on.
type bindTarget struct {
	network string // tcp4, tcp6, udp4, udp6
	addr    string
	v6      bool
}

// CheckBind tries to bind port/proto on 0.0.0.0 and [::] (tcp and udp are
// checked separately; [::] is skipped when the kernel has no IPv6). When
// a bind fails with "address in use", the owner is looked up in /proc and,
// failing that, with `ss`. When a bind fails for another reason (e.g. a
// non-root caller and a port below 1024) the port counts as busy only if an
// owner is found. An invalid port or protocol is reported as not free.
func (c Checker) CheckBind(ctx context.Context, port int, proto string) BindResult {
	p, ok := NormalizeProto(proto)
	res := BindResult{Free: true, Proto: p}
	if !ok || !ValidPort(port) {
		res.Free = false
		return res
	}
	ps := strconv.Itoa(port)
	targets := []bindTarget{
		{network: p + "4", addr: net.JoinHostPort("0.0.0.0", ps)},
		{network: p + "6", addr: net.JoinHostPort("::", ps), v6: true},
	}
	inUse, uncertain := false, false
	for _, t := range targets {
		err := c.tryBind(t.network, t.addr)
		switch {
		case err == nil:
			continue
		case stderrors.Is(err, syscall.EADDRINUSE):
			inUse = true
			res.Addr = t.addr
		case t.v6 && ipv6Unsupported(err):
			continue
		default:
			uncertain = true
			if res.Addr == "" {
				res.Addr = t.addr
			}
		}
		if inUse {
			break
		}
	}
	if !inUse && !uncertain {
		res.Addr = ""
		return res
	}

	pid, name, addr, err := c.ProcFS.Owner(port, p)
	found := err == nil
	if pid == 0 && c.Runner != nil {
		if sp, sn, sa, ok := c.ssOwner(ctx, port, p); ok {
			pid, name, found = sp, sn, true
			if addr == "" {
				addr = sa
			}
		}
	}
	if !inUse && !found {
		// Could not bind for an unrelated reason and nobody holds the port.
		res.Addr = ""
		return res
	}
	res.Free = false
	if addr != "" {
		res.Addr = addr
	}
	if pid > 0 {
		if name == "" {
			name = "unknown"
		}
		res.PID = pid
		res.Process = name + " (pid " + strconv.Itoa(pid) + ")"
		res.Deyroute = c.ProcFS.IsDeyroute(pid)
		res.Unit = c.ProcFS.Unit(pid)
	}
	return res
}

func (c Checker) tryBind(network, addr string) error {
	if c.listen != nil {
		cl, err := c.listen(network, addr)
		if err == nil {
			_ = cl.Close()
		}
		return err
	}
	var cl io.Closer
	var err error
	if strings.HasPrefix(network, "tcp") {
		cl, err = net.Listen(network, addr)
	} else {
		cl, err = net.ListenPacket(network, addr)
	}
	if err == nil {
		_ = cl.Close()
	}
	return err
}

// ipv6Unsupported reports whether err means the kernel has no IPv6.
func ipv6Unsupported(err error) bool {
	return stderrors.Is(err, syscall.EAFNOSUPPORT) ||
		stderrors.Is(err, syscall.EPROTONOSUPPORT) ||
		stderrors.Is(err, syscall.EADDRNOTAVAIL)
}

// ssOwner runs `ss -Hlntup` and returns the first process bound to
// port/proto.
func (c Checker) ssOwner(ctx context.Context, port int, proto string) (int, string, string, bool) {
	ctx, cancel := context.WithTimeout(ctx, ssTimeout)
	defer cancel()
	out, _, err := c.Runner.Run(ctx, "ss", []string{"-Hlntup"}, nil)
	if err != nil {
		return 0, "", "", false
	}
	return parseSS(string(out), port, proto)
}

// ssUserRe matches one process of the ss "users:" column.
var ssUserRe = regexp.MustCompile(`\("([^"]*)",pid=(\d+)`)

// parseSS finds the first socket bound to port/proto in `ss -Hlntup`
// output:
//
//	tcp LISTEN 0 511 0.0.0.0:443 0.0.0.0:* users:(("nginx",pid=1234,fd=6))
func parseSS(out string, port int, proto string) (pid int, name, addr string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != proto {
			continue
		}
		local := f[4]
		i := strings.LastIndexByte(local, ':')
		if i < 0 || local[i+1:] != strconv.Itoa(port) {
			continue
		}
		addr = normalizeSSAddr(local[:i], port)
		m := ssUserRe.FindStringSubmatch(line)
		if m == nil {
			return 0, "", addr, true
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return 0, "", addr, true
		}
		return n, m[1], addr, true
	}
	return 0, "", "", false
}

// normalizeSSAddr turns the ss host forms ("*", "[::]", "127.0.0.53%lo")
// into a host:port string.
func normalizeSSAddr(host string, port int) string {
	ps := strconv.Itoa(port)
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "*", "":
		return net.JoinHostPort("0.0.0.0", ps)
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return net.JoinHostPort(a.String(), ps)
	}
	return net.JoinHostPort(host, ps)
}

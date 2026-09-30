package setup

import (
	"context"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/ports"
)

// reservedControlPort reports ports the control port may never use: 22 and
// the backend control range 30000-31999 (config validation, section 4).
func reservedControlPort(p int) bool {
	return p == config.SSHPort || (p >= config.CtlRangeLow && p <= config.CtlRangeHigh)
}

// SuggestControlPort returns preferred (config.DefaultControlPort 44433
// when <= 0) when it is free, otherwise the next free port above it,
// wrapping around to 1024 after 65535. Port 22 and 30000-31999 are never
// suggested. busy may be nil (every port is free). It returns 0 when no
// port is free.
func SuggestControlPort(preferred int, busy func(port int) bool) int {
	if preferred <= 0 || preferred > 65535 {
		preferred = config.DefaultControlPort
	}
	free := func(p int) bool { return !reservedControlPort(p) && (busy == nil || !busy(p)) }
	for p := preferred; p <= 65535; p++ {
		if free(p) {
			return p
		}
	}
	for p := 1024; p < preferred; p++ {
		if free(p) {
			return p
		}
	}
	return 0
}

// PortBusy returns a busy callback for SuggestControlPort that tries to
// bind the TCP port on 0.0.0.0 and [::] (ports.Checker; the owner lookup
// reads Root/proc and falls back to `ss` through r).
func PortBusy(ctx context.Context, r exec.Runner, root string) func(port int) bool {
	c := portChecker(r, root)
	return func(port int) bool { return !c.CheckBind(ctx, port, config.ProtoTCP).Free }
}

// CheckControlPort returns nil when the TCP port is free, else DEY-P012
// naming the process that holds it.
func CheckControlPort(ctx context.Context, r exec.Runner, root string, port int) error {
	return portChecker(r, root).CheckBind(ctx, port, config.ProtoTCP).Err(port)
}

func portChecker(r exec.Runner, root string) ports.Checker {
	if root == "" {
		root = "/"
	}
	return ports.Checker{Runner: r, ProcFS: ports.ProcFS{Root: root}}
}

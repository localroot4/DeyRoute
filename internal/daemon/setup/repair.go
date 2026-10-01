package setup

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/localroot4/deyroute/internal/config"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/exec"
	"github.com/localroot4/deyroute/internal/systemd"
)

// RepairOptions configure Repair.
type RepairOptions struct {
	// Root is the filesystem root ("/" when empty).
	Root string
	// Runner runs systemctl and systemd-sysusers (exec.NewRunner() when nil).
	Runner        exec.Runner
	LookupGroup   func(name string) (gid int, err error)
	LookupUser    func(name string) (uid int, err error)
	Chown         func(path string, uid, gid int) error
	SocketPath    string
	SocketTimeout time.Duration
	Logger        *slog.Logger
}

// Repair is what running the installer again does on a server that is set
// up (spec section 5: "repairs/upgrades, config untouched"): it re-creates
// the system user and missing directories of the layout, re-installs the
// unit templates (daemon-reload when one changed), enables and restarts the
// service of the configured role and waits for its local API socket. The
// configuration is only read, for its role. It returns the restarted unit.
// Without a config it returns DEY-I023; with an unknown role DEY-C013.
func Repair(ctx context.Context, o RepairOptions) (string, error) {
	e := newEnv(envOptions{
		Root: o.Root, Runner: o.Runner, LookupGroup: o.LookupGroup, LookupUser: o.LookupUser, Chown: o.Chown,
		SocketPath: o.SocketPath, SocketTimeout: o.SocketTimeout, Logger: o.Logger,
	})
	data, err := os.ReadFile(e.configPath()) // #nosec G304 -- fixed deyroute path under Root
	if err != nil {
		return "", deyerr.Wrap(deyerr.I023, err, nil).WithDetail(e.configPath())
	}
	var probe struct {
		Role string `yaml:"role"`
	}
	_ = yaml.Unmarshal(data, &probe)
	unit := ""
	switch strings.TrimSpace(probe.Role) {
	case config.RoleHub:
		unit = systemd.HubUnit
	case config.RoleNode:
		unit = systemd.NodeUnit
	default:
		return "", deyerr.New(deyerr.C013, deyerr.Params{"field": "role", "value": probe.Role, "allowed": "hub, node"})
	}
	if err := e.ensureSystemUser(ctx); err != nil {
		e.log.Warn("repair: system user", slog.String("err", err.Error()))
	}
	if err := e.ensureLayout(); err != nil {
		return unit, err
	}
	return unit, e.startService(ctx, unit, true)
}

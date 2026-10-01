// Package systemd owns deyroute's systemd integration: the canonical unit
// files (embedded copies of deploy/systemd), per-instance drop-in rendering
// for deyroute-tun@.service, a systemctl wrapper that goes through the
// allow-listed exec.Runner, sd_notify/watchdog support and a log tail helper.
package systemd

import (
	"path"
	"regexp"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Unit names (section 2).
const (
	HubUnit     = "deyroute-hub.service"
	NodeUnit    = "deyroute-node.service"
	TunTemplate = "deyroute-tun@.service"
)

// Locations and limits.
const (
	// UnitDir is where deyroute installs unit files and drop-ins (under Root).
	UnitDir = "/etc/systemd/system"
	// DropInName is the per-instance drop-in file name.
	DropInName = "10-deyroute.conf"
	// TunnelLogDir holds one log per tunnel (section 7).
	TunnelLogDir = "/var/log/deyroute/tunnels"
	// MinVersion is the oldest supported systemd (section 2).
	MinVersion = 245
	// CanaryName is the last element of canary instances ("main.canary").
	CanaryName = "canary"

	tunPrefix   = "deyroute-tun@"
	unitSuffix  = ".service"
	maxInstance = 200
	maxUnitName = 255
)

// InstanceName returns the deyroute-tun@ instance of one warm unit:
// "<tunnel>.<node>.<backend>-<transport>", e.g. "main.de-1.backhaul-wssmux"
// (QUESTIONS.md C.4).
func InstanceName(tunnel, node, transportID string) string {
	return tunnel + "." + node + "." + strings.ReplaceAll(transportID, "/", "-")
}

// CanaryInstance returns the canary instance of a tunnel: "<tunnel>.canary".
func CanaryInstance(tunnel string) string { return tunnel + "." + CanaryName }

// UnitName returns the full unit name of an instance:
// "deyroute-tun@main.de-1.backhaul-wssmux.service".
func UnitName(instance string) string { return tunPrefix + instance + unitSuffix }

// InstanceOf extracts the instance from a deyroute-tun@ unit name; ok is false
// for any other unit.
func InstanceOf(unit string) (instance string, ok bool) {
	if !strings.HasPrefix(unit, tunPrefix) || !strings.HasSuffix(unit, unitSuffix) {
		return "", false
	}
	inst := strings.TrimSuffix(strings.TrimPrefix(unit, tunPrefix), unitSuffix)
	return inst, inst != ""
}

// TunnelLogFile returns /var/log/deyroute/tunnels/<tunnel>.log.
func TunnelLogFile(tunnel string) string { return path.Join(TunnelLogDir, tunnel+".log") }

// Instance is a parsed deyroute-tun@ instance name.
type Instance struct {
	Tunnel    string
	Node      string // empty for the canary
	Transport string // "backhaul/wssmux"; empty for the canary
	Canary    bool
}

// String renders the instance name again.
func (i Instance) String() string {
	if i.Canary {
		return CanaryInstance(i.Tunnel)
	}
	return InstanceName(i.Tunnel, i.Node, i.Transport)
}

var (
	idRe       = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)
	backendRe  = regexp.MustCompile(`^[a-z0-9]+$`)
	trNameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	instanceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	unitRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.@\\-]*\.(service|socket|timer|target|path|mount)$`)
)

// ParseInstance is the inverse of InstanceName and CanaryInstance. Tunnel
// and node ids never contain dots and backend names never contain dashes,
// so the split is unambiguous. Invalid names return DEY-X034.
func ParseInstance(instance string) (Instance, error) {
	bad := deyerr.New(deyerr.X034, deyerr.Params{"field": "instance", "value": instance})
	parts := strings.Split(instance, ".")
	switch len(parts) {
	case 2:
		if parts[1] != CanaryName || !idRe.MatchString(parts[0]) {
			return Instance{}, bad
		}
		return Instance{Tunnel: parts[0], Canary: true}, nil
	case 3:
		b, tr, ok := strings.Cut(parts[2], "-")
		if !ok || !idRe.MatchString(parts[0]) || !idRe.MatchString(parts[1]) ||
			!backendRe.MatchString(b) || !trNameRe.MatchString(tr) {
			return Instance{}, bad
		}
		return Instance{Tunnel: parts[0], Node: parts[1], Transport: b + "/" + tr}, nil
	}
	return Instance{}, bad
}

// ValidInstance reports whether s is safe to use as an instance name in a
// unit name and a file path (lowercase letters, digits, '.', '-'; no "..").
// It accepts any such name, not only ones ParseInstance understands.
func ValidInstance(s string) bool {
	return len(s) <= maxInstance && instanceRe.MatchString(s) && !strings.Contains(s, "..")
}

// ValidUnit reports whether unit is a plausible systemd unit name that can
// be passed to systemctl without being mistaken for an option (at most 255
// bytes: systemd's UNIT_NAME_MAX of 256 includes the terminating NUL).
func ValidUnit(unit string) bool {
	return len(unit) <= maxUnitName && unitRe.MatchString(unit)
}

func checkInstance(instance string) error {
	if !ValidInstance(instance) {
		return deyerr.New(deyerr.X034, deyerr.Params{"field": "instance", "value": instance})
	}
	return nil
}

func checkUnit(unit string) error {
	if !ValidUnit(unit) {
		return deyerr.New(deyerr.X034, deyerr.Params{"field": "unit", "value": unit})
	}
	return nil
}

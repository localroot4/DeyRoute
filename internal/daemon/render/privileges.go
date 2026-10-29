package render

import (
	"fmt"
	"slices"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/systemd"
)

// Privileges is one row of the privilege allowlist: what the
// deyroute-tun@ unit of a backend may ask for beyond the shared template
// (user deyroute, CAP_NET_BIND_SERVICE, AF_INET/AF_INET6/AF_UNIX, the full
// hardening). The names follow backend.UnitSpec.
type Privileges struct {
	RunAsRoot       bool
	ExtraCaps       []string // capabilities besides systemd.BaseCapability
	AddressFamilies []string // address families besides systemd.BaseAddressFamilies
	DropHardening   []string // template hardening options it may relax
	ReadWritePaths  []string // UnitSpec.ReadWritePaths entries, exactly as written
}

// neverRelaxed are the hardening options no backend may drop: without
// NoNewPrivileges a setuid binary would raise the unit's privileges, and a
// relaxed CapabilityBoundingSet ("~") lets it hold every capability.
var neverRelaxed = []string{"NoNewPrivileges", "CapabilityBoundingSet"}

// privilegeAllowlist is the fixed per-backend privilege allowlist (DR-04),
// compiled into the binary: the node refuses a backend.render whose unit asks
// for anything its row does not list, and the hub checks its own side the
// same way before writing it. Rows are keyed by backend, or by
// "backend/transport" where the transports of a backend differ; every
// backend and transport without a row runs with the template alone. A
// backend that needs a new privilege gets a row here (and a renderer that
// uses it: TestPrivilegeAllowlistCoversRenderers fails on a row no renderer
// needs). The values are spelled out instead of taken from the backend
// packages so a renderer change can never widen a row by itself.
var privilegeAllowlist = map[string]Privileges{
	// The kernel interface ("deyroute wg up/down") needs root, CAP_NET_ADMIN
	// and netlink; it sets net.ipv4.conf.<iface>.route_localnet when the node
	// DNATs to 127.0.0.1, which ProtectKernelTunables forbids.
	"wireguard": {RunAsRoot: true, ExtraCaps: []string{"CAP_NET_ADMIN"}, AddressFamilies: []string{"AF_NETLINK"},
		DropHardening: []string{"ProtectKernelTunables"}},
	// amneziawg-go creates a TUN device and its UAPI socket in
	// /run/amneziawg (optional: "-").
	"awg": {RunAsRoot: true, ExtraCaps: []string{"CAP_NET_ADMIN"}, AddressFamilies: []string{"AF_NETLINK"},
		ReadWritePaths: []string{"-/run/amneziawg"}},
	// The upstream release binaries are UPX-packed: the loader maps the
	// unpacked code executable at start.
	"rathole": {DropHardening: []string{"MemoryDenyWriteExecute"}},
	// The node side of direct/haproxy (the relay in check role) lists the
	// node's addresses over a netlink route socket.
	"direct/haproxy": {AddressFamilies: []string{"AF_NETLINK"}},
}

// privilegeRow returns the allowlist row of backend b and transport tr ("" =
// unknown: a canary directory names no transport, so the backend's row
// applies) and its key; an unlisted backend gets the empty row under the key
// "b/tr" (or "b").
func privilegeRow(b, tr string) (string, Privileges) {
	full := b
	if tr != "" {
		full = b + "/" + tr
		if p, ok := privilegeAllowlist[full]; ok {
			return full, p
		}
	}
	if p, ok := privilegeAllowlist[b]; ok {
		return b, p
	}
	return full, Privileges{}
}

// PrivilegeError is a unit privilege outside the allowlist.
type PrivilegeError struct {
	Backend string // "xray/reality", or the backend alone when the transport is unknown
	Field   string // the backend.UnitSpec field: RunAsRoot, ExtraCaps, AddressFamilies, DropHardening, ReadWritePaths
	Value   string // the refused value ("true" for RunAsRoot)
	what    string
	note    string
}

func (e *PrivilegeError) Error() string {
	return "backend " + e.Backend + " may not " + e.what + " (" + e.Field + ")" + e.note
}

// CheckPrivileges reports the first privilege of unit u that the allowlist
// row of backend b and transport tr ("" when unknown) does not grant: root,
// a capability or address family beyond the template's, a relaxed hardening
// option (NoNewPrivileges and CapabilityBoundingSet never) or a writable
// path. The error is a *PrivilegeError naming the backend.UnitSpec field.
func CheckPrivileges(b, tr string, u backend.UnitSpec) error {
	label := b
	if tr != "" {
		label = b + "/" + tr
	}
	_, row := privilegeRow(b, tr)
	refuse := func(field, value, what string) *PrivilegeError {
		return &PrivilegeError{Backend: label, Field: field, Value: value, what: what}
	}
	drops := backend.SortedKeys(u.DropHardening)
	for _, opt := range drops {
		if slices.Contains(neverRelaxed, opt) {
			e := refuse("DropHardening", opt, fmt.Sprintf("drop %q", opt))
			e.note = "; no backend may"
			return e
		}
	}
	if u.RunAsRoot && !row.RunAsRoot {
		return refuse("RunAsRoot", "true", "run as root")
	}
	for _, c := range u.ExtraCaps {
		if c != systemd.BaseCapability && !slices.Contains(row.ExtraCaps, c) {
			return refuse("ExtraCaps", c, fmt.Sprintf("add the capability %q", c))
		}
	}
	for _, af := range u.AddressFamilies {
		if !slices.Contains(systemd.BaseAddressFamilies, af) && !slices.Contains(row.AddressFamilies, af) {
			return refuse("AddressFamilies", af, fmt.Sprintf("add the address family %q", af))
		}
	}
	for _, opt := range drops {
		if !slices.Contains(row.DropHardening, opt) {
			return refuse("DropHardening", opt, fmt.Sprintf("drop %q", opt))
		}
	}
	for _, p := range u.ReadWritePaths {
		if !slices.Contains(row.ReadWritePaths, p) {
			return refuse("ReadWritePaths", p, fmt.Sprintf("make %q writable", p))
		}
	}
	return nil
}

// ConfigDirTransport returns the transport a backend config directory
// names: the last element of <BackendsConfDir>/<backend>/<tunnel>/<node>/
// <transport>. A canary directory (<backend>/<tunnel>/canary) and anything
// else names none ("").
func ConfigDirTransport(dir string) string {
	rel, ok := strings.CutPrefix(dir, config.BackendsConfDir+"/")
	if !ok {
		return ""
	}
	parts := strings.Split(rel, "/")
	if len(parts) != 4 {
		return ""
	}
	return parts[3]
}

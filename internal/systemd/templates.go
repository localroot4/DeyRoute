package systemd

import (
	"embed"
	"path"
)

// units holds byte-for-byte copies of deploy/systemd/*.service (a test
// enforces that they are identical).
//
//go:embed units/*.service
var units embed.FS

// UnitNames lists the embedded unit files in install order.
var UnitNames = []string{HubUnit, NodeUnit, TunTemplate}

// Templates returns a fresh copy of every embedded unit file keyed by name.
func Templates() map[string][]byte {
	out := make(map[string][]byte, len(UnitNames))
	for _, n := range UnitNames {
		if b, ok := Template(n); ok {
			out[n] = b
		}
	}
	return out
}

// Template returns one embedded unit file.
func Template(name string) ([]byte, bool) {
	b, err := units.ReadFile(path.Join("units", name))
	if err != nil {
		return nil, false
	}
	return b, true
}

// HardeningLines are the exact [Service] lines of section 11 that the
// deyroute-tun@.service template must contain (tests compare them with the
// spec and the template). Backends that cannot run under one of them relax it
// only in their own UnitSpec.DropHardening, never in the shared template.
var HardeningLines = []string{
	"User=deyroute",
	"Group=deyroute",
	"AmbientCapabilities=CAP_NET_BIND_SERVICE",
	"CapabilityBoundingSet=CAP_NET_BIND_SERVICE",
	"NoNewPrivileges=true",
	"ProtectSystem=strict",
	"ProtectHome=true",
	"PrivateTmp=true",
	"ProtectKernelTunables=true",
	"ProtectKernelModules=true",
	"ProtectControlGroups=true",
	"RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX",
	"RestrictNamespaces=true",
	"LockPersonality=true",
	"MemoryDenyWriteExecute=true",
	"SystemCallFilter=@system-service",
	"SystemCallArchitectures=native",
	"ReadWritePaths=/var/log/deyroute",
	"LimitNOFILE=1048576",
	"Restart=always",
	"RestartSec=2",
	"StartLimitIntervalSec=0",
	"StandardOutput=append:/var/log/deyroute/tunnels/%i.log",
	"StandardError=append:/var/log/deyroute/tunnels/%i.log",
}

// BaseAddressFamilies is the template's RestrictAddressFamilies list.
var BaseAddressFamilies = []string{"AF_INET", "AF_INET6", "AF_UNIX"}

// BaseCapability is the only capability of the template.
const BaseCapability = "CAP_NET_BIND_SERVICE"

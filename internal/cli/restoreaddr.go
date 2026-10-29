package cli

import (
	"context"
	stderrors "errors"
	"net/netip"
	"strings"
	"time"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/daemon/setup"
	deyerr "github.com/localroot4/deyroute/internal/errors"
	"github.com/localroot4/deyroute/internal/i18n"
)

// Where the address of a restored hub comes from (restore --json
// "address_source"; "" for a node backup).
const (
	addrFlag       = "flag"        // --public-ip [--public-ip6]
	addrDetected   = "detected"    // this server's detected public IP
	addrKept       = "kept"        // the backup's addresses, by --keep-address or the owner's answer
	addrSameServer = "same_server" // this server already is that hub
	addrAsked      = "asked"       // typed by the owner at the terminal question
)

// restoreFlags are the address flags of deyroute restore (DR-08).
type restoreFlags struct {
	yes, keep bool
	ip4, ip6  string
}

// check validates the flags on their own (DEY-C013) before the backup is
// read: --keep-address excludes --public-ip and --public-ip6, --public-ip6
// needs --public-ip, and each is an address of its family.
func (f *restoreFlags) check() error {
	f.ip4, f.ip6 = strings.TrimSpace(f.ip4), strings.TrimSpace(f.ip6)
	bad := func(field, value string, allowed i18n.Key) error {
		return deyerr.New(deyerr.C013, deyerr.Params{"field": field, "value": value, "allowed": i18n.T(allowed)})
	}
	if f.keep && (f.ip4 != "" || f.ip6 != "") {
		return bad("--keep-address", "true", i18n.CLIRestoreWantNoKeep)
	}
	if f.ip6 != "" && f.ip4 == "" {
		return bad("--public-ip6", f.ip6, i18n.CLIRestoreWantWithIP)
	}
	if f.ip4 != "" {
		a, err := netip.ParseAddr(f.ip4)
		if err != nil || !a.Is4() {
			return bad("--public-ip", f.ip4, i18n.CLIRestoreWantIPv4)
		}
		f.ip4 = a.String()
	}
	if f.ip6 != "" {
		a, err := netip.ParseAddr(f.ip6)
		if err != nil || !a.Is6() || a.Is4In6() || a.Zone() != "" {
			return bad("--public-ip6", f.ip6, i18n.CLIRestoreWantIPv6)
		}
		f.ip6 = a.String()
	}
	return nil
}

// addressDetection is what restoreAddress found for a backup. It decides
// nothing: the CLI (restorePolicy) and the menu (RestoreCheck) apply their
// own policy to it.
type addressDetection struct {
	// hub is set for a hub backup: its address must be decided.
	hub bool
	// sameServer: this server already is that hub (its config.yaml has
	// the backup's hub.public_ip); detection is not run.
	sameServer bool
	// ip, private and err are the result of DetectIP.
	ip      string
	private bool
	err     error
}

// unknown reports whether the detection gives no usable address for a
// moved hub, and why (the reason of DEY-I025 and of the question).
func (d addressDetection) unknown() (string, bool) {
	switch {
	case d.err != nil:
		var e *deyerr.Error
		reason := d.err.Error()
		if stderrors.As(d.err, &e) {
			reason = string(e.Code) + ": " + e.Why()
		}
		return i18n.T(i18n.CLIRestoreWhyFailed, clean(reason)), true
	case d.ip == "":
		return i18n.T(i18n.CLIRestoreWhyNone), true
	case d.private:
		return i18n.T(i18n.CLIRestoreWhyPrivate, d.ip), true
	}
	return "", false
}

// restoreAddress detects this server's public IP for a hub backup (spec
// section 5, "hub move"). A same-server restore (the local config already
// has the backup's hub.public_ip) skips the detection.
func (g *Globals) restoreAddress(ctx context.Context, info backupInfo) addressDetection {
	if info.Role != config.RoleHub {
		return addressDetection{}
	}
	d := addressDetection{hub: true}
	if c := g.localConfig(); c != nil && c.Hub != nil && c.Hub.PublicIP == info.PublicIP {
		d.sameServer = true
		return d
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	d.ip, d.private, d.err = g.DetectIP(dctx)
	cancel()
	return d
}

// restoreAddr is the address decision of deyroute restore.
type restoreAddr struct {
	// ip4 and ip6 move the hub ("" keeps the backup's addresses).
	ip4, ip6 string
	source   string
	warnings []string
}

// restorePolicy decides the address of a restored hub for deyroute
// restore (DR-08): the flags first, then a same-server restore, then the
// detection. A private or failed detection on another server is DEY-I025
// without a question (--yes or no terminal); on a terminal the owner types
// the address, the backup's one as default. A public detection is taken
// (on a terminal after a yes). Nothing is written here.
func (g *Globals) restorePolicy(ctx context.Context, info backupInfo, f restoreFlags) (restoreAddr, error) {
	if info.Role != config.RoleHub {
		if f.ip4 != "" {
			return restoreAddr{}, deyerr.New(deyerr.C013, deyerr.Params{
				"field": "--public-ip", "value": f.ip4, "allowed": i18n.T(i18n.CLIRestoreWantHubBackup),
			})
		}
		return restoreAddr{warnings: []string{}}, nil
	}
	kept := func(source string) (restoreAddr, error) {
		return restoreAddr{source: source, warnings: []string{}}, nil
	}
	switch {
	case f.ip4 != "":
		return movedTo(info, f.ip4, f.ip6, addrFlag), nil
	case f.keep:
		return kept(addrKept)
	}
	d := g.restoreAddress(ctx, info)
	if d.sameServer {
		return kept(addrSameServer)
	}
	if why, unknown := d.unknown(); unknown {
		if f.yes || !g.IsTTY {
			return restoreAddr{}, deyerr.New(deyerr.I025, deyerr.Params{"reason": why, "old_ip": info.PublicIP})
		}
		ip, err := g.askRestoreIP(info, why)
		if err != nil {
			return restoreAddr{}, err
		}
		if ip == info.PublicIP {
			return kept(addrKept)
		}
		return movedTo(info, ip, "", addrAsked), nil
	}
	if d.ip == info.PublicIP {
		return kept(addrDetected)
	}
	if g.IsTTY && !f.yes {
		use, err := g.askYesNo(i18n.T(i18n.CLIAskMovedHub, d.ip, info.PublicIP), true)
		if err != nil {
			return restoreAddr{}, err
		}
		if !use {
			return kept(addrKept)
		}
	}
	return movedTo(info, d.ip, "", addrDetected), nil
}

// movedTo is the decision to move the hub to ip4 (and ip6): a private
// address and a cleared IPv6 address of the backup are warnings.
func movedTo(info backupInfo, ip4, ip6, source string) restoreAddr {
	r := restoreAddr{ip4: ip4, ip6: ip6, source: source, warnings: []string{}}
	for _, ip := range []string{ip4, ip6} {
		if a, err := netip.ParseAddr(ip); err == nil && !setup.IsPublicIP(a) {
			r.warnings = append(r.warnings, i18n.T(i18n.CLIRestoreWarnPrivate, ip))
		}
	}
	if ip6 == "" && info.PublicIP6 != "" && info.PublicIP6 != ip4 {
		r.warnings = append(r.warnings, i18n.T(i18n.CLIRestoreWarnIP6Cleared, info.PublicIP6))
	}
	return r
}

// askRestoreIP asks for this server's public IP when the detection gave
// none (why), with the backup's address as default. It is separate from
// askPublicIP (setup), which detects by itself and has no old address.
func (g *Globals) askRestoreIP(info backupInfo, why string) (string, error) {
	ans, err := g.askQ(question{
		title: i18n.T(i18n.CLIAskRestoreIP),
		help:  []string{g.sym().warn + " " + why, i18n.T(i18n.CLIRestoreIPHelp, info.PublicIP)},
		def:   info.PublicIP,
		check: func(s string) error {
			if _, err := parseHubIP(s); err != nil {
				return deyerr.New(deyerr.C013, deyerr.Params{"field": "hub.public_ip", "value": s, "allowed": i18n.T(i18n.CLIWantIP)})
			}
			return nil
		},
	})
	if err != nil {
		return "", err
	}
	return parseHubIP(ans)
}

// parseHubIP accepts an IPv4 or IPv6 address without a zone (the values
// hub.public_ip allows) and returns it in canonical form.
func parseHubIP(s string) (string, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return "", err
	}
	if a.Zone() != "" {
		return "", deyerr.Plain("an address with a zone is not allowed")
	}
	return a.Unmap().String(), nil
}

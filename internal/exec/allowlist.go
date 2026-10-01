package exec

import "path/filepath"

// Allowed is the complete list of external programs deyroute may execute
// (section 15, extended by QUESTIONS.md C.15). A program is identified by its
// base name, so absolute paths to pinned backend binaries such as
// /var/lib/deyroute/bin/xray/v26.3.27/xray are accepted as "xray".
//
// Adding a name here is a security decision: it must be recorded in
// QUESTIONS.md and in the architecture guide.
var Allowed = []string{
	"systemctl",
	"nft",
	"ss",
	"ip",
	"xray",
	"rathole",
	"ufw",
	"firewall-cmd",
	"iptables",
	"systemd-sysusers",
	// uninstall removes the deyroute system user and group the installer
	// created (spec section 5: the system is restored; QUESTIONS.md C.30).
	"userdel",
	"groupdel",
	// direct/haproxy runs the distribution's haproxy under systemd; deyroute
	// only looks it up (LookPath) and may print its version.
	"haproxy",
}

// operations restricts the programs section 15 allows for one operation
// only ("xray x25519", "rathole --genkey"): their first argument must be the
// listed one. Backends themselves run under systemd (deyroute-tun@.service),
// never through this package, so e.g. "xray run" is refused.
var operations = map[string]string{
	"xray":    "x25519",
	"rathole": "--genkey",
	"haproxy": "-v",
}

// Permitted reports whether the command line may be executed: name is
// allow-listed (IsAllowed) and, for single-operation programs, args start
// with that operation. Runners refuse anything else with DEY-X004.
func Permitted(name string, args []string) bool {
	if !IsAllowed(name) {
		return false
	}
	op, restricted := operations[filepath.Base(filepath.Clean(name))]
	return !restricted || (len(args) > 0 && args[0] == op)
}

// IsAllowed reports whether name (a bare program name or an absolute path)
// refers to an allow-listed program. Relative paths containing a slash are
// never allowed. Runners additionally check the arguments (Permitted).
func IsAllowed(name string) bool {
	if name == "" {
		return false
	}
	base := name
	if containsSlash(name) {
		if !filepath.IsAbs(name) {
			return false
		}
		base = filepath.Base(filepath.Clean(name))
	}
	for _, a := range Allowed {
		if a == base {
			return true
		}
	}
	return false
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

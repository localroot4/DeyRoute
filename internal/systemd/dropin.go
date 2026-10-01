package systemd

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/localroot4/deyroute/internal/backend"
	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// DropInHeader is the first line of every rendered drop-in.
const DropInHeader = "# Managed by deyroute: rendered from /etc/deyroute/config.yaml on every apply; manual edits are overwritten."

var (
	capRe    = regexp.MustCompile(`^CAP_[A-Z0-9_]+$`)
	afRe     = regexp.MustCompile(`^AF_[A-Z0-9_]+$`)
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// permissiveValues maps a hardening option to the value that switches it
// off. An empty value is systemd's "reset" assignment, which for list
// options (RestrictAddressFamilies, SystemCallFilter, …) removes the
// restriction. Only these options can be relaxed through
// UnitSpec.DropHardening: any other name (User, ExecStart, StandardOutput,
// ReadWritePaths, …) is not rendered, because resetting it would silently
// change who runs the backend or what runs (ValidateUnitSpec reports it).
var permissiveValues = map[string]string{
	"NoNewPrivileges":         "false",
	"ProtectSystem":           "false",
	"ProtectHome":             "false",
	"PrivateTmp":              "false",
	"PrivateDevices":          "false",
	"PrivateNetwork":          "false",
	"PrivateUsers":            "false",
	"PrivateMounts":           "false",
	"ProtectKernelTunables":   "false",
	"ProtectKernelModules":    "false",
	"ProtectKernelLogs":       "false",
	"ProtectControlGroups":    "false",
	"ProtectClock":            "false",
	"ProtectHostname":         "false",
	"RestrictNamespaces":      "false",
	"RestrictRealtime":        "false",
	"RestrictSUIDSGID":        "false",
	"LockPersonality":         "false",
	"MemoryDenyWriteExecute":  "false",
	"RemoveIPC":               "false",
	"RestrictAddressFamilies": "",
	"SystemCallFilter":        "",
	"SystemCallArchitectures": "",
	"ProtectProc":             "default",
	"ProcSubset":              "all",
	// "~" with an empty list replaces the bounding set with every capability.
	"CapabilityBoundingSet": "~",
}

// PermissiveValue returns the value RenderDropIn writes when a backend drops
// the hardening option (e.g. "MemoryDenyWriteExecute" → "false").
func PermissiveValue(option string) string { return permissiveValues[option] }

// Relaxable reports whether option is a hardening option that
// UnitSpec.DropHardening may relax.
func Relaxable(option string) bool {
	_, ok := permissiveValues[option]
	return ok
}

// RenderDropIn renders the per-instance [Service] drop-in of
// deyroute-tun@.service (written to
// /etc/systemd/system/deyroute-tun@<instance>.service.d/10-deyroute.conf).
//
// workDir is used when spec.WorkingDirectory is empty (normally
// Paths.ConfigDir); logFile (normally /var/log/deyroute/tunnels/<tunnel>.log)
// overrides the template's per-instance log so logs stay per tunnel. The
// output is deterministic: maps are rendered sorted and every value is
// escaped for systemd ('%' → "%%", '$' → "$$" in command lines, quotes and
// backslashes escaped, control characters never reach the file, and no line
// ends in a backslash, which systemd would treat as a line continuation that
// swallows the next directive). Entries that systemd would misread or that
// would weaken more than intended (invalid environment names, capability or
// address family names, DropHardening names that are not relaxable
// hardening options) are skipped; ValidateUnitSpec reports them as errors.
//
// Environment values and arguments are readable by every local user
// (systemctl show, /proc/<pid>/cmdline): backends pass secrets in their
// configuration files, never here.
func RenderDropIn(spec backend.UnitSpec, workDir, logFile string) []byte {
	var b bytes.Buffer
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	line("%s", DropInHeader)
	line("[Service]")

	typ := strings.TrimSpace(spec.Type)
	if typ == "" {
		typ = "simple"
	}
	line("Type=%s", sanitize(typ))
	if typ == "oneshot" {
		// systemd refuses Restart=always (inherited from the template) for
		// oneshot units; a oneshot unit has no process that could crash.
		line("Restart=no")
		if spec.RemainAfterExit {
			line("RemainAfterExit=yes")
		}
	}

	wd := spec.WorkingDirectory
	if wd == "" {
		wd = workDir
	}
	if wd != "" {
		line("WorkingDirectory=%s", escapePath(wd))
	}

	if len(spec.ExecStartPre) > 0 {
		line("ExecStartPre=")
		for _, argv := range spec.ExecStartPre {
			if len(argv) > 0 {
				line("ExecStartPre=%s", QuoteCommand(argv))
			}
		}
	}
	line("ExecStart=")
	if len(spec.ExecStart) > 0 {
		line("ExecStart=%s", QuoteCommand(spec.ExecStart))
	}
	if len(spec.ExecStop) > 0 {
		line("ExecStop=")
		for _, argv := range spec.ExecStop {
			if len(argv) > 0 {
				line("ExecStop=%s", QuoteCommand(argv))
			}
		}
	}

	for _, k := range backend.SortedKeys(spec.Env) {
		if !envKeyRe.MatchString(k) {
			continue
		}
		line(`Environment="%s"`, escapeQuoted(k+"="+spec.Env[k], false))
	}

	if logFile != "" {
		lf := escapePath(logFile)
		line("StandardOutput=append:%s", lf)
		line("StandardError=append:%s", lf)
	}

	if spec.RunAsRoot {
		line("User=root")
		line("Group=root")
	}
	_, dropCaps := spec.DropHardening["CapabilityBoundingSet"]
	caps := capabilities(spec.ExtraCaps)
	if spec.RunAsRoot || len(caps) > 1 {
		joined := strings.Join(caps, " ")
		if !dropCaps {
			line("CapabilityBoundingSet=")
			line("CapabilityBoundingSet=%s", joined)
		}
		line("AmbientCapabilities=")
		line("AmbientCapabilities=%s", joined)
	}

	_, dropAF := spec.DropHardening["RestrictAddressFamilies"]
	if fams := addressFamilies(spec.AddressFamilies); len(fams) > len(BaseAddressFamilies) && !dropAF {
		line("RestrictAddressFamilies=")
		line("RestrictAddressFamilies=%s", strings.Join(fams, " "))
	}

	if rw := readWritePaths(spec.ReadWritePaths); rw != "" {
		line("ReadWritePaths=%s", rw)
	}

	for _, opt := range backend.SortedKeys(spec.DropHardening) {
		value, ok := permissiveValues[opt]
		if !ok {
			continue
		}
		reason := sanitize(strings.TrimSpace(spec.DropHardening[opt]))
		if reason == "" {
			reason = "required by this backend"
		}
		line("# %s", reason)
		line("%s=%s", opt, value)
	}
	return b.Bytes()
}

// ValidateUnitSpec reports problems RenderDropIn would silently skip or
// systemd would reject (DEY-X034, joined): ExecStart must be non-empty with
// an absolute program path, Type simple|exec|oneshot|notify|forking, valid
// environment names, CAP_* capabilities, AF_* address families, absolute
// paths, and DropHardening entries that name a relaxable hardening option
// and carry a reason (section 11).
func ValidateUnitSpec(spec backend.UnitSpec) error {
	var errs []error
	bad := func(field, value string) {
		errs = append(errs, deyerr.New(deyerr.X034, deyerr.Params{"field": field, "value": value}))
	}
	checkArgv := func(field string, argv []string) {
		if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
			bad(field, strings.Join(argv, " "))
		}
	}
	checkArgv("ExecStart", spec.ExecStart)
	for _, argv := range spec.ExecStartPre {
		checkArgv("ExecStartPre", argv)
	}
	for _, argv := range spec.ExecStop {
		checkArgv("ExecStop", argv)
	}
	switch spec.Type {
	case "", "simple", "exec", "oneshot", "notify", "forking":
	default:
		bad("Type", spec.Type)
	}
	if spec.RemainAfterExit && spec.Type != "oneshot" {
		bad("RemainAfterExit", "set without Type=oneshot")
	}
	if spec.WorkingDirectory != "" && (!filepath.IsAbs(spec.WorkingDirectory) ||
		hasControl(spec.WorkingDirectory) || continues(spec.WorkingDirectory)) {
		bad("WorkingDirectory", spec.WorkingDirectory)
	}
	for _, k := range backend.SortedKeys(spec.Env) {
		if !envKeyRe.MatchString(k) {
			bad("Environment", k)
		}
	}
	for _, c := range spec.ExtraCaps {
		if !capRe.MatchString(c) {
			bad("ExtraCaps", c)
		}
	}
	for _, a := range spec.AddressFamilies {
		if !afRe.MatchString(a) {
			bad("AddressFamilies", a)
		}
	}
	for _, p := range spec.ReadWritePaths {
		if !filepath.IsAbs(strings.TrimLeft(p, "-+")) || hasControl(p) {
			bad("ReadWritePaths", p)
		}
	}
	for _, opt := range backend.SortedKeys(spec.DropHardening) {
		if !Relaxable(opt) {
			bad("DropHardening", opt)
		}
		// Section 11: an option is only dropped "with a documented reason".
		if strings.TrimSpace(spec.DropHardening[opt]) == "" {
			bad("DropHardening", opt+" (reason missing)")
		}
	}
	return deyerr.Join(errs...)
}

// QuoteCommand renders argv as a systemd command line. Arguments containing
// whitespace or quotes (and empty ones) are double-quoted; backslashes,
// quotes and control characters are C-escaped; '%' becomes "%%" (specifiers)
// and '$' becomes "$$" (environment expansion); a lone ";" becomes "\;".
func QuoteCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = quoteWord(a, true)
	}
	return strings.Join(parts, " ")
}

func quoteWord(s string, dollar bool) string {
	if s == ";" {
		return `\;`
	}
	e := escapeQuoted(s, dollar)
	if s == "" || strings.ContainsAny(s, " \t\n\r\v\f\"'") {
		return `"` + e + `"`
	}
	return e
}

// escapeQuoted escapes s for use inside (or outside) a systemd double-quoted
// word that is C-unescaped.
func escapeQuoted(s string, dollar bool) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '%':
			b.WriteString("%%")
		case r == '$' && dollar:
			b.WriteString("$$")
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// escapePath prepares a single-line path value (WorkingDirectory=,
// append:<path>): control characters are replaced and '%' is doubled.
func escapePath(p string) string { return strings.ReplaceAll(sanitize(p), "%", "%%") }

// sanitize makes s safe as the rest of one directive (or comment) line:
// control characters are replaced with '_' so the value can never start a
// new directive, and so is an unescaped trailing backslash, which systemd
// treats as a line continuation (the next directive would be appended to
// this line, e.g. swallowed by a comment).
func sanitize(s string) string {
	if hasControl(s) {
		s = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return '_'
			}
			return r
		}, s)
	}
	if continues(s) {
		s = s[:len(s)-1] + "_"
	}
	return s
}

// continues reports whether s ends in an odd number of backslashes, i.e. a
// line ending with s would be continued by systemd's parser.
func continues(s string) bool {
	n := len(s) - len(strings.TrimRight(s, `\`))
	return n%2 == 1
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// capabilities returns CAP_NET_BIND_SERVICE followed by the valid, sorted,
// de-duplicated extra capabilities.
func capabilities(extra []string) []string {
	return mergeList([]string{BaseCapability}, extra, capRe)
}

// addressFamilies returns the template list followed by the valid, sorted,
// de-duplicated extra families.
func addressFamilies(extra []string) []string {
	return mergeList(BaseAddressFamilies, extra, afRe)
}

func mergeList(base, extra []string, valid *regexp.Regexp) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(extra))
	for _, v := range base {
		seen[v] = true
		out = append(out, v)
	}
	var add []string
	for _, v := range extra {
		v = strings.TrimSpace(v)
		if valid.MatchString(v) && !seen[v] {
			seen[v] = true
			add = append(add, v)
		}
	}
	sort.Strings(add)
	return append(out, add...)
}

func readWritePaths(paths []string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] || hasControl(p) {
			continue
		}
		seen[p] = true
		out = append(out, quoteWord(p, false))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

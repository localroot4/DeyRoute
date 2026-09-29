package errors

import (
	"fmt"
	"strings"
)

var categoryTitles = []struct{ prefix, title string }{
	{"DEY-I", "Installer / environment (DEY-I0xx)"},
	{"DEY-C", "Configuration (DEY-C0xx)"},
	{"DEY-N", "Node / control channel (DEY-N0xx)"},
	{"DEY-P", "Ports / firewall (DEY-P0xx)"},
	{"DEY-T", "TLS (DEY-T0xx)"},
	{"DEY-B", "Backends (DEY-B0xx)"},
	{"DEY-F", "Failover (DEY-F0xx)"},
	{"DEY-S", "Security / update (DEY-S0xx)"},
	{"DEY-X", "Internal (DEY-X0xx)"},
}

// MarkdownDoc renders docs/ERRORS.md from the catalog.
func MarkdownDoc() string {
	var b strings.Builder
	b.WriteString("# DEYROUTE error codes\n\n")
	b.WriteString("<!-- Generated from internal/errors/codes.go by `make docs`. Do not edit by hand. -->\n\n")
	b.WriteString("Every error is shown as three fixed lines plus the log path:\n\n")
	b.WriteString("```text\n✖ DEY-P012  Port 443/tcp is already in use\n  Why:  nginx (pid 1234) is listening on 0.0.0.0:443\n  Fix:  choose another port, or stop that service first\n  Log:  /var/log/deyroute/hub.log (search DEY-P012)\n```\n\n")
	b.WriteString("Placeholders such as `{port}` are filled at runtime. CLI exit codes: `1` for every category except `DEY-X` (`2`); `3` means a confirmation was required but `--yes` was not given.\n\n")
	all := All()
	for _, cat := range categoryTitles {
		fmt.Fprintf(&b, "## %s\n\n", cat.title)
		b.WriteString("| Code | Message | Why | Fix |\n| --- | --- | --- | --- |\n")
		for _, in := range all {
			if !strings.HasPrefix(string(in.Code), cat.prefix) {
				continue
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", in.Code, mdEscape(in.Message), mdEscape(in.Why), mdEscape(in.Fix))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func mdEscape(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

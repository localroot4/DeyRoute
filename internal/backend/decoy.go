package backend

import "strings"

// DefaultDecoySNIs is the built-in decoy list used by the Reality style
// transports (xray/reality, waterwall/reverse-reality) when the owner has not
// configured hub.decoy_snis (spec sections 7.4 and 19).
//
// PLACEHOLDERS: these three TLS 1.3 sites are generic examples only. The
// owner must replace them with three domains that are reachable from the hub
// datacenter in Iran and that are not blocked there (QUESTIONS.md, section B,
// "Decoy SNI list"). Before a Reality rung is activated the hub tests the
// candidates in order and passes the first reachable one as
// RenderInput.Decoy (DEY-B042 when none answers).
var DefaultDecoySNIs = []string{
	"dl.google.com",
	"www.speedtest.net",
	"www.samsung.com",
}

// DecoyCandidates returns the decoy SNIs the hub should test, in order:
// configured (hub.decoy_snis, config.HubInfo.DecoySNIs) when non-empty,
// otherwise DefaultDecoySNIs. Empty and duplicate entries are skipped; a
// configured list with only blank entries counts as empty.
func DecoyCandidates(configured []string) []string {
	if out := dedupDecoys(configured); len(out) > 0 {
		return out
	}
	return dedupDecoys(DefaultDecoySNIs)
}

func dedupDecoys(src []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(src))
	for _, d := range src {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// DecoyFor returns the decoy SNI a renderer must use: RenderInput.Decoy (the
// reachable decoy selected by the hub), otherwise the first hub decoy, i.e.
// the first entry of DecoyCandidates(in.Hub.DecoySNIs) (hub.decoy_snis, or
// DefaultDecoySNIs when the owner configured none, spec section 7.4: "default
// list of 3 domains"). It returns "" only when both lists are empty; Reality
// renderers then fail Validate with DEY-B006.
func DecoyFor(in RenderInput) string {
	if d := strings.TrimSpace(in.Decoy); d != "" {
		return d
	}
	if c := DecoyCandidates(in.Hub.DecoySNIs); len(c) > 0 {
		return c[0]
	}
	return ""
}

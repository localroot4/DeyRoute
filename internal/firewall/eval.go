package firewall

// A tiny rule-evaluation model shared by the ufw, iptables and nftables
// heuristics. The "packet" is always the first packet of a new IPv4
// connection from an arbitrary Internet address to one of this host's
// addresses on port/proto, arriving on a non-loopback interface.
//
// Every rule is reduced to how certainly it matches that packet (tri) and
// what it does (verdict). Conservative policy (section 10): a port is only
// reported blocked when the drop is certain; a rule that may accept the
// packet before the drop makes the result "not blocked, uncertain".

// tri is a three-valued match result, ordered no < maybe < yes.
type tri uint8

const (
	no tri = iota
	maybe
	yes
)

// and combines two match results (both must match).
func and(a, b tri) tri { return min(a, b) }

// not negates a match result (maybe stays maybe).
func not(a tri) tri { return yes - a }

// boolTri converts a certain boolean.
func boolTri(b bool) tri {
	if b {
		return yes
	}
	return no
}

// verdict is what a rule does with a matching packet.
type verdict uint8

const (
	vNone   verdict = iota // non-terminating (counter, log, …)
	vAccept                // accept (ends this chain only)
	vDrop                  // drop or reject (final)
	vReturn                // return to the calling chain
	vJump                  // evaluate target, then continue
	vGoto                  // evaluate target, then return
)

type evalRule struct {
	match   tri
	verdict verdict
	target  string // chain for vJump/vGoto
	text    string // for details
}

type evalChain struct {
	rules  []evalRule
	policy verdict // vAccept or vDrop for base chains
}

// ruleset maps chain names to chains of one table.
type ruleset map[string]*evalChain

// maxDepth bounds jump nesting (and breaks loops).
const maxDepth = 16

// outcome of evaluating a chain.
type outcome struct {
	verdict   verdict // vAccept, vDrop or vNone (fell through / returned)
	uncertain bool
	detail    string
}

func (rs ruleset) eval(name string, depth int) outcome {
	c := rs[name]
	if c == nil || depth > maxDepth {
		return outcome{uncertain: true, detail: "cannot follow jump to " + name}
	}
	var res outcome
	note := func(text string) {
		res.uncertain = true
		if res.detail == "" {
			res.detail = "rule not understood: " + text
		}
	}
	for _, r := range c.rules {
		if r.match == no {
			continue
		}
		switch r.verdict {
		case vAccept:
			if r.match == yes {
				return outcome{verdict: vAccept, uncertain: res.uncertain, detail: firstNonEmpty(res.detail, r.text)}
			}
			note(r.text)
		case vDrop:
			if r.match == yes {
				return outcome{verdict: vDrop, uncertain: res.uncertain, detail: firstNonEmpty(res.detail, r.text)}
			}
			// A drop that may not apply is ignored (conservative).
		case vReturn:
			if r.match == yes {
				return res
			}
			note(r.text)
		case vJump, vGoto:
			sub := rs.eval(r.target, depth+1)
			if sub.uncertain {
				res.uncertain = true
				if res.detail == "" {
					res.detail = sub.detail
				}
			}
			if r.match == yes {
				if sub.verdict == vAccept || sub.verdict == vDrop {
					sub.uncertain = sub.uncertain || res.uncertain
					sub.detail = firstNonEmpty(res.detail, sub.detail)
					return sub
				}
				if r.verdict == vGoto {
					return res
				}
				continue
			}
			if sub.verdict == vAccept {
				note(r.text)
			}
		}
	}
	return res
}

// blocks evaluates a base chain and applies its policy. It returns
// blocked=true only for a certain drop; uncertain=true when a rule that may
// accept the packet was seen before the drop.
func (rs ruleset) blocks(base string) (blocked, uncertain bool, detail string) {
	o := rs.eval(base, 0)
	v := o.verdict
	if v == vNone {
		if c := rs[base]; c != nil && c.policy == vDrop {
			v = vDrop
			if o.detail == "" {
				o.detail = "policy drop"
			}
		} else {
			v = vAccept
		}
	}
	if v == vDrop {
		if o.uncertain {
			return false, true, o.detail
		}
		return true, false, o.detail
	}
	return false, false, ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

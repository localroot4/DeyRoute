package config

import (
	"sort"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// BuiltinLadders returns copies of the ladder profiles every hub knows even
// when ladders: does not define them: "default" (section 8) and
// "udp-default" (section 8, UDP-only tunnels). A profile of the same name
// under ladders: overrides the builtin.
func BuiltinLadders() map[string][]string {
	return map[string][]string{
		DefaultLadderName:    cloneStrings(DefaultLadder),
		DefaultUDPLadderName: cloneStrings(DefaultUDPLadder),
	}
}

// IsBuiltinLadder reports whether name is a builtin profile name.
func IsBuiltinLadder(name string) bool {
	return name == DefaultLadderName || name == DefaultUDPLadderName
}

// LadderProfile returns a copy of the rungs of the named profile: the
// ladders: entry when present, else the builtin. ok is false for unknown
// names.
func (c *Config) LadderProfile(name string) (rungs []string, ok bool) {
	if c != nil {
		if r, found := c.Ladders[name]; found {
			return cloneStrings(r), true
		}
	}
	switch name {
	case DefaultLadderName:
		return cloneStrings(DefaultLadder), true
	case DefaultUDPLadderName:
		return cloneStrings(DefaultUDPLadder), true
	}
	return nil, false
}

// LadderNames returns every profile name (ladders: plus builtins), sorted.
func (c *Config) LadderNames() []string {
	set := map[string]bool{DefaultLadderName: true, DefaultUDPLadderName: true}
	if c != nil {
		for n := range c.Ladders {
			set[n] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// LadderUsers returns the ids of tunnels that reference profile name, in
// config order. A UDP-only tunnel that references "default" is reported as a
// user of "udp-default", because that is the profile it resolves to.
func (c *Config) LadderUsers(name string) []string {
	var out []string
	if c == nil {
		return nil
	}
	for i := range c.Tunnels {
		t := &c.Tunnels[i]
		if t.Ladder.Inline != nil {
			continue
		}
		if effectiveProfile(t) == name {
			out = append(out, t.ID)
		}
	}
	return out
}

// effectiveProfile is the profile a named ladder reference resolves to:
// UDP-only tunnels that use "default" (or no ladder) get "udp-default".
func effectiveProfile(t *Tunnel) string {
	name := t.Ladder.Name
	if name == "" {
		name = DefaultLadderName
	}
	if name == DefaultLadderName && t.UDPOnly() {
		return DefaultUDPLadderName
	}
	return name
}

// ResolveLadder returns the ordered transport ids tunnel t uses.
//
// An inline ladder is used as written. A profile name is looked up under
// ladders: and then among the builtins; a UDP-only tunnel that references
// "default" uses "udp-default" instead (section 8: UDP-only tunnels have
// their own default ladder). Then every rung that does not support all of the
// tunnel's protocols is dropped (section 8: the renderer removes rungs that
// do not support the tunnel protocol); supports may be nil to keep all.
// Rungs that need UDP reachability are not dropped here — that is a runtime
// skip with a warning, never a config error.
//
// Errors: DEY-C012 for an unknown profile, DEY-C009 for an explicit empty
// inline list ("ladder: []"), an empty profile, a nil tunnel, or when the
// protocol filter leaves nothing. The returned slice is a fresh copy.
func (c *Config) ResolveLadder(t *Tunnel, supports func(transportID, proto string) bool) ([]string, error) {
	if t == nil {
		return nil, deyerr.New(deyerr.C009, deyerr.Params{"tunnel": ""})
	}
	var rungs []string
	if t.Ladder.Inline != nil {
		rungs = cloneStrings(t.Ladder.Inline)
	} else {
		name := effectiveProfile(t)
		r, ok := c.LadderProfile(name)
		if !ok {
			return nil, deyerr.New(deyerr.C012, deyerr.Params{"ladder": t.Ladder.Name, "tunnel": t.ID})
		}
		rungs = r
	}
	if supports != nil {
		protos := t.Protos()
		kept := rungs[:0]
		for _, r := range rungs {
			all := true
			for _, p := range protos {
				if !supports(r, p) {
					all = false
					break
				}
			}
			if all {
				kept = append(kept, r)
			}
		}
		rungs = kept
	}
	if len(rungs) == 0 {
		return nil, deyerr.New(deyerr.C009, deyerr.Params{"tunnel": t.ID})
	}
	return rungs, nil
}

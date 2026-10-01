package config

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// Id rules (section 4).
const (
	MinIDLen = 2
	MaxIDLen = 32
	// FallbackID is what Slugify returns when a name yields no usable id.
	FallbackID = "tunnel"
)

var idRe = regexp.MustCompile(`^[a-z0-9-]{2,32}$`)

// ValidID reports whether s is a valid node/tunnel/ladder id:
// [a-z0-9-]{2,32} (section 4).
func ValidID(s string) bool { return idRe.MatchString(s) }

// ReservedNodeIDs cannot be used as node ids: "canary" names the canary
// unit directory and control-port keys of every tunnel (spec section 9).
var ReservedNodeIDs = []string{"canary"}

// ValidNodeID is ValidID minus the reserved node ids.
func ValidNodeID(s string) bool {
	for _, r := range ReservedNodeIDs {
		if s == r {
			return false
		}
	}
	return ValidID(s)
}

// Slugify derives an id from a display name: lower case, every run of
// characters outside a-z/0-9 becomes one '-', leading/trailing '-' are
// trimmed and the result is cut to 32 characters. When fewer than 2
// characters remain the result is "tunnel".
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > MaxIDLen {
		s = strings.TrimRight(s[:MaxIDLen], "-")
	}
	if len(s) < MinIDLen {
		return FallbackID
	}
	return s
}

// UniqueID returns base, or base-2, base-3, … (shortened to stay within 32
// characters) — the first candidate taken reports as free. An invalid base
// is slugified first. taken may be nil (then base is returned). After
// maxUniqueTries candidates it gives up and returns "" (which Validate
// rejects as DEY-C007), so a broken taken func cannot hang the caller.
func UniqueID(base string, taken func(string) bool) string {
	if !ValidID(base) {
		base = Slugify(base)
	}
	if taken == nil || !taken(base) {
		return base
	}
	for n := 2; n <= maxUniqueTries; n++ {
		suffix := "-" + strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > MaxIDLen {
			stem = strings.TrimRight(stem[:MaxIDLen-len(suffix)], "-")
		}
		if cand := stem + suffix; !taken(cand) {
			return cand
		}
	}
	return ""
}

const maxUniqueTries = 10000

// Tunnel returns a pointer to the tunnel with id (it aliases c.Tunnels, so
// changes are visible in c; do not keep it across appends/removals).
func (c *Config) Tunnel(id string) (*Tunnel, bool) {
	if c == nil {
		return nil, false
	}
	for i := range c.Tunnels {
		if c.Tunnels[i].ID == id {
			return &c.Tunnels[i], true
		}
	}
	return nil, false
}

// NodeByID returns a pointer to the hub's nodes: entry with id (aliases
// c.Nodes like Tunnel).
func (c *Config) NodeByID(id string) (*Node, bool) {
	if c == nil {
		return nil, false
	}
	for i := range c.Nodes {
		if c.Nodes[i].ID == id {
			return &c.Nodes[i], true
		}
	}
	return nil, false
}

// TunnelsUsingNode returns the ids of tunnels whose nodes list contains id.
func (c *Config) TunnelsUsingNode(id string) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, t := range c.Tunnels {
		for _, n := range t.Nodes {
			if n == id {
				out = append(out, t.ID)
				break
			}
		}
	}
	return out
}

// HasProto reports whether any port map of t uses proto ("tcp"/"udp").
func (t *Tunnel) HasProto(p string) bool {
	if t == nil {
		return false
	}
	for _, pm := range t.Ports {
		if protoOf(pm) == p {
			return true
		}
	}
	return false
}

// Protos returns the distinct protocols of t in fixed order (tcp, udp).
func (t *Tunnel) Protos() []string {
	var out []string
	for _, p := range []string{ProtoTCP, ProtoUDP} {
		if t.HasProto(p) {
			out = append(out, p)
		}
	}
	return out
}

// UDPOnly reports whether t has port maps and all of them are UDP.
func (t *Tunnel) UDPOnly() bool { return t.HasProto(ProtoUDP) && !t.HasProto(ProtoTCP) }

// ProbeTarget returns the port map the path probe uses (section 9): the TCP
// port map whose listen equals probe_port when set, otherwise the first TCP
// port map. A UDP-only tunnel has no TCP target: ok is false and the first
// port map is returned for reference (zero value when there are none).
func (t *Tunnel) ProbeTarget() (PortMap, bool) {
	if t == nil {
		return PortMap{}, false
	}
	if t.ProbePort != 0 {
		for _, pm := range t.Ports {
			if pm.Listen == t.ProbePort && protoOf(pm) == ProtoTCP {
				return pm, true
			}
		}
	}
	for _, pm := range t.Ports {
		if protoOf(pm) == ProtoTCP {
			return pm, true
		}
	}
	if len(t.Ports) > 0 {
		return t.Ports[0], false
	}
	return PortMap{}, false
}

// Clone returns a deep copy of t (the zero Tunnel for nil).
func (t *Tunnel) Clone() Tunnel {
	if t == nil {
		return Tunnel{}
	}
	out := *t
	out.Nodes = cloneStrings(t.Nodes)
	out.Ports = clonePorts(t.Ports)
	out.Ladder.Inline = cloneStrings(t.Ladder.Inline)
	if t.Advanced != nil {
		a := *t.Advanced
		out.Advanced = &a
	}
	return out
}

// ConnectionPool returns advanced.connection_pool, or DefaultConnectionPool
// (8) when it is not set (backhaul/frp pool size).
func (t *Tunnel) ConnectionPool() int {
	if t != nil && t.Advanced != nil && t.Advanced.ConnectionPool > 0 {
		return t.Advanced.ConnectionPool
	}
	return DefaultConnectionPool
}

// HysteriaMbps returns the Hysteria2 bandwidth (up, down) in Mbit/s from
// advanced:, each DefaultHysteriaMbps (100) when not set.
func (t *Tunnel) HysteriaMbps() (up, down int) {
	up, down = DefaultHysteriaMbps, DefaultHysteriaMbps
	if t == nil || t.Advanced == nil {
		return up, down
	}
	if t.Advanced.HysteriaUpMbps > 0 {
		up = t.Advanced.HysteriaUpMbps
	}
	if t.Advanced.HysteriaDownMbps > 0 {
		down = t.Advanced.HysteriaDownMbps
	}
	return up, down
}

// protoOf treats an empty proto as tcp (the default) so helpers work on
// configs that have not been through ApplyDefaults yet.
func protoOf(pm PortMap) string {
	if pm.Proto == "" {
		return DefaultProto
	}
	return pm.Proto
}

// ListenKey identifies one listen port on the hub.
type ListenKey struct {
	Port  int
	Proto string
}

// String returns "443/tcp".
func (k ListenKey) String() string { return fmt.Sprintf("%d/%s", k.Port, k.Proto) }

// UsedListenPorts maps every listen/proto of every tunnel to the id of the
// tunnel that uses it (the first one when the config is invalid).
func (c *Config) UsedListenPorts() map[ListenKey]string {
	out := map[ListenKey]string{}
	if c == nil {
		return out
	}
	for _, t := range c.Tunnels {
		for _, pm := range t.Ports {
			k := ListenKey{Port: pm.Listen, Proto: protoOf(pm)}
			if _, dup := out[k]; !dup {
				out[k] = t.ID
			}
		}
	}
	return out
}

// AddTunnel appends a deep copy of t (with defaults applied) after checking
// that the result is still valid: id format and uniqueness, nodes that
// exist, free listen ports, ports and enums (transport ids are not checked;
// the caller saves with its ValidateOptions). c is unchanged on error.
// Build t with NewTunnel: zero booleans (enabled, failback) are kept as
// false, see ApplyDefaults.
func (c *Config) AddTunnel(t Tunnel) error {
	if c == nil {
		return deyerr.New(deyerr.C016, deyerr.Params{"role": ""})
	}
	nt := t.Clone()
	nt.applyDefaults()
	next := Clone(c)
	next.Tunnels = append(next.Tunnels, nt)
	if err := next.Validate(ValidateOptions{}); err != nil {
		return err
	}
	c.Tunnels = append(c.Tunnels, nt)
	return nil
}

// RemoveTunnel deletes the tunnel with id and reports whether it existed.
func (c *Config) RemoveTunnel(id string) bool {
	if c == nil {
		return false
	}
	for i := range c.Tunnels {
		if c.Tunnels[i].ID == id {
			c.Tunnels = slices.Delete(c.Tunnels, i, i+1)
			return true
		}
	}
	return false
}

// AddNode appends a copy of n after checking that the result is still valid
// (id format and uniqueness, public IP). c is unchanged on error.
func (c *Config) AddNode(n Node) error {
	if c == nil {
		return deyerr.New(deyerr.C016, deyerr.Params{"role": ""})
	}
	nn := n
	nn.Tags = cloneStrings(n.Tags)
	next := Clone(c)
	next.Nodes = append(next.Nodes, nn)
	if err := next.Validate(ValidateOptions{}); err != nil {
		return err
	}
	c.Nodes = append(c.Nodes, nn)
	return nil
}

// RemoveNode deletes the node with id and removes it from every tunnel's
// nodes list. It returns the ids of the tunnels that referenced it and
// whether the node existed (an unknown id changes nothing). A tunnel whose
// only node is id would be left without a node, so RemoveNode refuses with
// DEY-C008 (delete or re-home that tunnel first) and changes nothing.
func (c *Config) RemoveNode(id string) (affected []string, removed bool, err error) {
	if c == nil {
		return nil, false, nil
	}
	idx := -1
	for i := range c.Nodes {
		if c.Nodes[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, false, nil
	}
	affected = c.TunnelsUsingNode(id)
	for _, tid := range affected {
		t, _ := c.Tunnel(tid)
		if len(t.Nodes) == 1 {
			return nil, false, deyerr.New(deyerr.C008, deyerr.Params{"tunnel": tid})
		}
	}
	for _, tid := range affected {
		t, _ := c.Tunnel(tid)
		kept := make([]string, 0, len(t.Nodes)-1)
		for _, n := range t.Nodes {
			if n != id {
				kept = append(kept, n)
			}
		}
		t.Nodes = kept
	}
	c.Nodes = slices.Delete(c.Nodes, idx, idx+1)
	return affected, true, nil
}

// Clone returns a deep copy of c (nil for nil). Nil and empty slices/maps
// keep their nil-ness so a clone compares equal with reflect.DeepEqual.
func Clone(c *Config) *Config {
	if c == nil {
		return nil
	}
	out := *c
	if c.Hub != nil {
		h := *c.Hub
		h.Notify.Telegram.Events = cloneStrings(c.Hub.Notify.Telegram.Events)
		h.DecoySNIs = cloneStrings(c.Hub.DecoySNIs)
		if c.Hub.ACME != nil {
			a := *c.Hub.ACME
			h.ACME = &a
		}
		out.Hub = &h
	}
	if c.Nodes != nil {
		out.Nodes = make([]Node, len(c.Nodes))
		for i, n := range c.Nodes {
			n.Tags = cloneStrings(n.Tags)
			out.Nodes[i] = n
		}
	}
	if c.Tunnels != nil {
		out.Tunnels = make([]Tunnel, len(c.Tunnels))
		for i := range c.Tunnels {
			out.Tunnels[i] = c.Tunnels[i].Clone()
		}
	}
	if c.Ladders != nil {
		out.Ladders = make(map[string][]string, len(c.Ladders))
		for k, v := range c.Ladders {
			out.Ladders[k] = cloneStrings(v)
		}
	}
	if c.Tuning != nil {
		t := *c.Tuning
		out.Tuning = &t
	}
	if c.Security != nil {
		s := *c.Security
		out.Security = &s
	}
	if c.Node != nil {
		n := *c.Node
		out.Node = &n
	}
	return &out
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append(make([]string, 0, len(s)), s...)
}

func clonePorts(p []PortMap) []PortMap {
	if p == nil {
		return nil
	}
	return append(make([]PortMap, 0, len(p)), p...)
}

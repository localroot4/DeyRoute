package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	deyerr "github.com/localroot4/deyroute/internal/errors"
	"gopkg.in/yaml.v3"
)

// inputName is the {path} shown in DEY-C014 when the config comes from memory
// (Parse) instead of a file (Load).
const inputName = "config.yaml"

// currentSchema is the schema version this build reads and writes. It is a
// variable only so tests can exercise the migration path with a fake future
// version; production code never changes it.
var currentSchema = SchemaVersion

// Load reads, migrates, defaults and validates the config file at path.
// Transport ids are not checked (see LoadWith).
func Load(path string) (*Config, error) { return LoadWith(path, ValidateOptions{}) }

// LoadWith is Load with explicit validation options (the daemon passes
// backend.KnownTransport so unknown ladder rungs are DEY-C005).
//
// Errors: DEY-C014 (unreadable, empty or invalid YAML), DEY-C001 (unknown key,
// with its line), DEY-C013 (wrong value type), DEY-C019 (schema_version),
// DEY-C006 (migration), and every validation code joined with errors.Join.
// A missing file is DEY-C014 wrapping fs.ErrNotExist. The config is nil
// whenever an error is returned. Line numbers of a migrated (older) file
// refer to the converted document.
func LoadWith(path string, opts ValidateOptions) (*Config, error) {
	c, _, err := loadFile(path, opts, false)
	return c, err
}

// LoadDropObsolete is LoadWith for the config.yaml the hub runs from (its
// start and the DEY-C026 check before a change): a key an earlier build
// accepted and this one refuses is cleared before validation instead of
// failing the load, and the dotted paths of the cleared keys are returned
// so the daemon can warn about them. An update or a restart must not leave
// the hub unable to start; the next change writes config.yaml without the
// key. Owner edits (config validate, edit and apply, restore) keep refusing
// it through LoadWith and ParseWith.
func LoadDropObsolete(path string, opts ValidateOptions) (*Config, []string, error) {
	return loadFile(path, opts, true)
}

func loadFile(path string, opts ValidateOptions, dropObsolete bool) (*Config, []string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- the config path is chosen by the owner/daemon
	if err != nil {
		return nil, nil, c014(path, err, 0)
	}
	return parse(data, path, opts, dropObsolete)
}

// Parse is Load for in-memory data (strict decode, migration, defaults and
// validation without transport checks).
func Parse(data []byte) (*Config, error) { return ParseWith(data, ValidateOptions{}) }

// ParseWith is Parse with explicit validation options.
func ParseWith(data []byte, opts ValidateOptions) (*Config, error) {
	c, _, err := parse(data, inputName, opts, false)
	return c, err
}

// Decode strictly decodes data and migrates it to the current schema without
// applying defaults or validating. It is the building block of Parse and is
// useful for tools that must see the file exactly as written.
func Decode(data []byte) (*Config, error) {
	c, _, err := decode(data, inputName)
	return c, err
}

// SchemaVersionOf returns the schema_version stored in data without decoding
// the rest of the document. The daemon uses it to take a backup before a
// migrated config is written back.
func SchemaVersionOf(data []byte) (int, error) {
	root, err := parseRoot(data, inputName)
	if err != nil {
		return 0, err
	}
	return schemaVersion(root)
}

func parse(data []byte, path string, opts ValidateOptions, dropObsolete bool) (*Config, []string, error) {
	c, root, err := decode(data, path)
	if err != nil {
		return nil, nil, err
	}
	var dropped []string
	if dropObsolete {
		dropped = clearObsolete(c)
	}
	applyPresenceDefaults(root, c)
	c.ApplyDefaults()
	if err := c.Validate(opts); err != nil {
		return nil, nil, err
	}
	return c, dropped, nil
}

// clearObsolete clears the keys an earlier build accepted and validation
// now refuses, and returns their dotted paths. advanced.backhaul_web_port:
// the pinned Backhaul cannot keep its stats page on 127.0.0.1 (see
// validator.advanced); an advanced section left empty is removed.
func clearObsolete(c *Config) []string {
	var dropped []string
	for i := range c.Tunnels {
		t := &c.Tunnels[i]
		if t.Advanced == nil || t.Advanced.BackhaulWebPort == 0 {
			continue
		}
		t.Advanced.BackhaulWebPort = 0
		if *t.Advanced == (Advanced{}) {
			t.Advanced = nil
		}
		dropped = append(dropped, itemPath("tunnels", t.ID, i)+".advanced.backhaul_web_port")
	}
	return dropped
}

// decode parses data into a Config: syntax check, schema_version check,
// migration (when older), then a strict KnownFields decode. It returns the
// root mapping node of the decoded document for presence-aware defaults.
func decode(data []byte, path string) (*Config, *yaml.Node, error) {
	root, err := parseRoot(data, path)
	if err != nil {
		return nil, nil, err
	}
	ver, err := schemaVersion(root)
	if err != nil {
		return nil, nil, err
	}
	if ver < currentSchema {
		data, root, err = migrateDocument(root, ver, path)
		if err != nil {
			return nil, nil, err
		}
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, nil, mapDecodeError(err, root, path)
	}
	return &c, root, nil
}

// parseRoot checks the YAML syntax and returns the root mapping node.
func parseRoot(data []byte, path string) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, c014(path, deyerr.Plain("the file is empty"), 0)
		}
		return nil, c014(path, err, lineOf(err.Error()))
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, c014(path, err, lineOf(err.Error()))
		}
		return nil, c014(path, deyerr.Plain("the file contains more than one YAML document"), extra.Line)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, c014(path, deyerr.Plain("the file is empty"), 0)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, c014(path, deyerr.Plain("the top level must be a mapping (key: value)"), root.Line)
	}
	return root, nil
}

// schemaVersion reads schema_version from the root mapping: missing, not an
// integer, < 1 or newer than this build → DEY-C019. Any YAML integer form
// (1, +1, 0o1, 0x1) is accepted, exactly like the struct decoder does.
func schemaVersion(root *yaml.Node) (int, error) {
	n, ok := lookup(root, "schema_version")
	switch {
	case !ok || n.ShortTag() == "!!null":
		return 0, deyerr.New(deyerr.C019, deyerr.Params{"version": "(missing)"})
	case n.Kind != yaml.ScalarNode:
		return 0, deyerr.New(deyerr.C019, deyerr.Params{"version": "(not a number)"})
	case n.ShortTag() == "!!str":
		// Quoted so "1" (text) is not mistaken for the supported number 1.
		return 0, deyerr.New(deyerr.C019, deyerr.Params{"version": strconv.Quote(n.Value)})
	case n.ShortTag() != "!!int":
		return 0, deyerr.New(deyerr.C019, deyerr.Params{"version": n.Value})
	}
	var v int
	if err := n.Decode(&v); err != nil {
		return 0, deyerr.New(deyerr.C019, deyerr.Params{"version": n.Value})
	}
	if v < 1 || v > currentSchema {
		return 0, deyerr.New(deyerr.C019, deyerr.Params{"version": v})
	}
	return v, nil
}

// migrateDocument converts an older document to the current schema and
// returns the re-encoded bytes (line numbers in later errors refer to them).
func migrateDocument(root *yaml.Node, from int, path string) ([]byte, *yaml.Node, error) {
	var raw map[string]any
	if err := root.Decode(&raw); err != nil {
		return nil, nil, wrapDetail(deyerr.C006, err, deyerr.Params{"from": from, "to": currentSchema})
	}
	raw, err := Migrate(raw, from)
	if err != nil {
		return nil, nil, err
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		return nil, nil, wrapDetail(deyerr.C006, err, deyerr.Params{"from": from, "to": currentSchema})
	}
	newRoot, err := parseRoot(out, path)
	if err != nil {
		return nil, nil, wrapDetail(deyerr.C006, err, deyerr.Params{"from": from, "to": currentSchema})
	}
	return out, newRoot, nil
}

// wrapDetail wraps cause into a DEY error and also shows the cause text as
// the Detail block, because the owner-facing three-line format (Format)
// does not print the Cause: a YAML parser message with its line number or an
// I/O error would otherwise never reach the owner.
func wrapDetail(code deyerr.Code, cause error, params deyerr.Params) *deyerr.Error {
	return deyerr.Wrap(code, cause, params).WithDetail(cause.Error())
}

func c014(path string, cause error, line int) *deyerr.Error {
	p := deyerr.Params{"path": path}
	if line > 0 {
		p["line"] = line
	}
	return wrapDetail(deyerr.C014, cause, p)
}

var (
	lineRe = regexp.MustCompile(`line (\d+)`)
	// (?s): a key or value may contain a newline (quoted YAML strings).
	unknownKeyRe = regexp.MustCompile(`(?s)^line (\d+): field (.+) not found in type (\S+)$`)
	badTypeRe    = regexp.MustCompile("(?s)^line (\\d+): cannot unmarshal (!!\\w+)(?: `.*`)? into (.+)$")
	ladderErrRe  = regexp.MustCompile(`^line (\d+): ladder must be`)
)

// lineOf extracts the first "line N" of a yaml error message (0 if none).
func lineOf(msg string) int {
	m := lineRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// mapDecodeError converts yaml.v3 decode errors into DEY errors: unknown
// keys → C001 (key path + line), wrong types → C013, anything else → C014.
// Every problem of a *yaml.TypeError is reported (errors.Join).
func mapDecodeError(err error, root *yaml.Node, path string) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		msg := err.Error()
		if m := ladderErrRe.FindStringSubmatch(msg); m != nil {
			line, _ := strconv.Atoi(m[1])
			key, _ := pathAtLine(root, line, func(h lineHit) bool { return h.key != nil && h.key.Value == "ladder" })
			if key == "" {
				key = "ladder"
			}
			return deyerr.Wrap(deyerr.C013, err, deyerr.Params{
				"field": key, "value": "(mapping)", "allowed": "a ladder name or a list of transports", "line": line,
			}).WithDetail(fmt.Sprintf("line %d", line))
		}
		return c014(path, err, lineOf(msg))
	}
	errs := make([]error, 0, len(te.Errors))
	for _, msg := range te.Errors {
		errs = append(errs, mapTypeErrorLine(msg, root, path))
	}
	return errors.Join(errs...)
}

func mapTypeErrorLine(msg string, root *yaml.Node, path string) error {
	if m := unknownKeyRe.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		key := keyPath(root, line, m[2])
		return printableParams(deyerr.New(deyerr.C001, deyerr.Params{"key": key, "line": line}))
	}
	if m := badTypeRe.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		tag := m[2]
		field, value := pathAtLine(root, line, func(h lineHit) bool { return h.node.ShortTag() == tag })
		if field == "" {
			field = fmt.Sprintf("line %d", line)
		}
		if value == "" {
			value = describeYAMLTag(tag)
		}
		return printableParams(deyerr.New(deyerr.C013, deyerr.Params{
			"field": field, "value": value, "allowed": describeGoType(m[3]), "line": line,
		})).WithDetail(fmt.Sprintf("line %d", line))
	}
	return c014(path, deyerr.Plain(msg), lineOf(msg))
}

// describeYAMLTag names a non-scalar (or empty) YAML value for the {value}
// of DEY-C013: "(list)", "(mapping)", or the bare tag.
func describeYAMLTag(tag string) string {
	switch tag {
	case "!!seq":
		return "(list)"
	case "!!map":
		return "(mapping)"
	case "!!null":
		return "(empty)"
	}
	return strings.TrimPrefix(tag, "!!")
}

// describeGoType turns the Go type of a yaml error into owner words.
func describeGoType(t string) string {
	switch {
	case strings.HasPrefix(t, "[]"):
		return "a list"
	case strings.HasPrefix(t, "map[") || strings.HasPrefix(t, "config.") || strings.HasPrefix(t, "*config."):
		return "a section of key: value lines"
	case strings.HasPrefix(t, "int") || strings.HasPrefix(t, "uint"):
		return "a whole number"
	case t == "bool":
		return "true or false"
	case t == "string":
		return "a text value"
	}
	return t
}

// keyPath returns the dotted path ("hub.notify.foo", "tunnels[0].bar") of the
// mapping key named key on line; key alone when it cannot be located.
func keyPath(root *yaml.Node, line int, key string) string {
	var found string
	walk(root, "", func(n *yaml.Node, p string, k *yaml.Node) bool {
		if k != nil && k.Line == line && k.Value == key {
			found = p
			return false
		}
		return true
	})
	if found == "" {
		return key
	}
	return found
}

// lineHit is a node found on a given line during pathAtLine.
type lineHit struct {
	node *yaml.Node
	path string
	key  *yaml.Node
}

// pathAtLine returns the dotted path and scalar value of the node on line
// that best matches: first a mapping value accepted by match, then any node
// accepted by match, then any mapping value on that line.
func pathAtLine(root *yaml.Node, line int, match func(h lineHit) bool) (string, string) {
	var hits []lineHit
	walk(root, "", func(n *yaml.Node, p string, k *yaml.Node) bool {
		if n.Line == line {
			hits = append(hits, lineHit{node: n, path: p, key: k})
		}
		return true
	})
	pick := func(ok func(h lineHit) bool) (string, string, bool) {
		for _, h := range hits {
			if ok(h) {
				v := ""
				if h.node.Kind == yaml.ScalarNode {
					v = h.node.Value
				}
				return h.path, v, true
			}
		}
		return "", "", false
	}
	for _, ok := range []func(h lineHit) bool{
		func(h lineHit) bool { return h.key != nil && match(h) },
		match,
		func(h lineHit) bool { return h.key != nil },
	} {
		if p, v, found := pick(ok); found {
			return p, v
		}
	}
	return "", ""
}

// walk visits every node below n in document order with its dotted path; key
// is the mapping key node of n (nil for sequence items and the root). visit
// returns false to stop the walk.
func walk(n *yaml.Node, path string, visit func(n *yaml.Node, path string, key *yaml.Node) bool) bool {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			p := k.Value
			if path != "" {
				p = path + "." + k.Value
			}
			if !visit(v, p, k) || !walk(v, p, visit) {
				return false
			}
		}
	case yaml.SequenceNode:
		for i, v := range n.Content {
			p := fmt.Sprintf("%s[%d]", path, i)
			if !visit(v, p, nil) || !walk(v, p, visit) {
				return false
			}
		}
	}
	return true
}

// maxMergeDepth bounds alias/merge-key resolution in lookup. yaml.v3 already
// rejects self-referencing anchors; this is a second line of defence.
const maxMergeDepth = 32

// deref follows YAML alias nodes (*anchor) to the node they refer to.
func deref(n *yaml.Node) *yaml.Node {
	for i := 0; n != nil && n.Kind == yaml.AliasNode && i < maxMergeDepth; i++ {
		n = n.Alias
	}
	return n
}

// isMergeKey reports whether k is the YAML merge key "<<" (same rule as
// yaml.v3).
func isMergeKey(k *yaml.Node) bool {
	return k.Kind == yaml.ScalarNode && k.Value == "<<" && (k.Tag == "" || k.Tag == "!" || k.ShortTag() == "!!merge")
}

// lookup returns the value node of key in mapping n with the precedence the
// yaml.v3 decoder uses: aliases are followed, the mapping's own keys win,
// then mappings merged with "<<: *anchor" (or "<<: [*a, *b]", first wins).
// The returned node is dereferenced.
func lookup(n *yaml.Node, key string) (*yaml.Node, bool) {
	return lookupDepth(n, key, 0)
}

func lookupDepth(n *yaml.Node, key string, depth int) (*yaml.Node, bool) {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode || depth > maxMergeDepth {
		return nil, false
	}
	var merge *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := deref(n.Content[i])
		if k == nil {
			continue
		}
		if isMergeKey(k) {
			merge = n.Content[i+1] // yaml.v3 uses the last merge key
			continue
		}
		if k.Value == key {
			return deref(n.Content[i+1]), true
		}
	}
	if merge = deref(merge); merge == nil {
		return nil, false
	}
	sources := []*yaml.Node{merge}
	if merge.Kind == yaml.SequenceNode {
		sources = merge.Content
	}
	for _, src := range sources {
		if v, ok := lookupDepth(src, key, depth+1); ok {
			return v, true
		}
	}
	return nil, false
}

// hasKey reports whether mapping n (aliases and merge keys included)
// contains key with a non-null value.
func hasKey(n *yaml.Node, key string) bool {
	v, ok := lookup(n, key)
	return ok && v != nil && v.ShortTag() != "!!null"
}

// applyPresenceDefaults sets boolean defaults whose key is absent from the
// document. Plain bools cannot tell "false" from "missing" after decoding, so
// this looks at the YAML tree: a tunnel without enabled: is enabled, a
// failover section without failback: fails back, and a tuning/security
// section that omits one of its switches gets the spec default (true).
// Explicit false values are never touched, including values that reach a
// section through a YAML anchor (*alias) or merge key (<<).
func applyPresenceDefaults(root *yaml.Node, c *Config) {
	if tunnels, ok := lookup(root, "tunnels"); ok && tunnels.Kind == yaml.SequenceNode {
		for i, tn := range tunnels.Content {
			if i >= len(c.Tunnels) {
				break
			}
			if !hasKey(tn, "enabled") {
				c.Tunnels[i].Enabled = true
			}
			fo, _ := lookup(tn, "failover")
			if !hasKey(fo, "failback") {
				c.Tunnels[i].Failover.Failback = true
			}
		}
	}
	if tn, ok := lookup(root, "tuning"); ok && c.Tuning != nil && !hasKey(tn, "bbr") {
		c.Tuning.BBR = true
	}
	if sn, ok := lookup(root, "security"); ok && c.Security != nil {
		if !hasKey(sn, "firewall_managed") {
			c.Security.FirewallManaged = true
		}
		if !hasKey(sn, "restrict_control_to_nodes") {
			c.Security.RestrictControlToNodes = true
		}
	}
}

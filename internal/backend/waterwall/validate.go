package waterwall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

// ValidateJSON checks the rendered Waterwall files before the unit starts
// (spec section 7.4: Waterwall explains broken configs badly, so they are
// caught here). files are the Rendered.Files of one side, or the files read
// back from the config directory.
//
//   - DEY-B041 when core.json is missing;
//   - DEY-B040 (file = the offending name, Detail = the reason) when a file is
//     not valid JSON, core.json lists a config that is not present, or a node
//     graph is inconsistent (duplicate or missing names, a "next", Bridge
//     "pair" or RealityServer "destination" pointing at an unknown node).
func ValidateJSON(files map[string][]byte) error {
	core, ok := files[CoreFile]
	if !ok {
		return deyerr.New(deyerr.B041, deyerr.Params{"dir": "the rendered config directory"})
	}
	invalid := func(file, reason string) error {
		return deyerr.New(deyerr.B040, deyerr.Params{"file": file}).WithDetail(reason)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !json.Valid(files[name]) {
			return invalid(name, syntaxDetail(files[name]))
		}
	}

	var c struct {
		Configs []string `json:"configs"`
	}
	if err := json.Unmarshal(core, &c); err != nil {
		return invalid(CoreFile, "\"configs\" must be a list of file names: "+err.Error())
	}
	if len(c.Configs) == 0 {
		return invalid(CoreFile, "\"configs\" lists no node config file")
	}
	for _, cf := range c.Configs {
		data, ok := files[cf]
		if !ok {
			return invalid(cf, "listed in core.json but not present")
		}
		if reason := checkGraph(data); reason != "" {
			return invalid(cf, reason)
		}
	}
	return nil
}

// syntaxDetail describes a JSON syntax error with its byte offset.
func syntaxDetail(data []byte) string {
	var v any
	err := json.NewDecoder(bytes.NewReader(data)).Decode(&v)
	var se *json.SyntaxError
	switch {
	case errors.As(err, &se):
		return fmt.Sprintf("JSON syntax error at byte %d: %v", se.Offset, err)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "JSON is truncated (unexpected end of file)"
	case err != nil:
		return "JSON error: " + err.Error()
	}
	return "trailing data after the JSON document"
}

// checkGraph validates one node config and returns "" when it is sound.
func checkGraph(data []byte) string {
	var g struct {
		Name  string `json:"name"`
		Nodes []struct {
			Name     string          `json:"name"`
			Type     string          `json:"type"`
			Next     string          `json:"next"`
			Settings json.RawMessage `json:"settings"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &g); err != nil {
		return "unexpected structure: " + err.Error()
	}
	if len(g.Nodes) == 0 {
		return "\"nodes\" is empty"
	}
	byName := map[string]string{}
	for i, n := range g.Nodes {
		if n.Name == "" || n.Type == "" {
			return fmt.Sprintf("node #%d has no name or type", i+1)
		}
		if _, dup := byName[n.Name]; dup {
			return "node name \"" + n.Name + "\" is used twice"
		}
		byName[n.Name] = n.Type
	}
	chained := map[string]string{}
	for _, n := range g.Nodes {
		if n.Next != "" {
			if _, ok := byName[n.Next]; !ok {
				return "node \"" + n.Name + "\" -> next \"" + n.Next + "\" does not exist"
			}
			if prev, ok := chained[n.Next]; ok {
				return "nodes \"" + prev + "\" and \"" + n.Name + "\" both chain to \"" + n.Next + "\""
			}
			chained[n.Next] = n.Name
		}
		var s map[string]any
		if len(n.Settings) > 0 && string(n.Settings) != "null" {
			if err := json.Unmarshal(n.Settings, &s); err != nil {
				return "node \"" + n.Name + "\" settings must be an object"
			}
		}
		ref := func(key string) string {
			v, _ := s[key].(string)
			if v == "" {
				return "node \"" + n.Name + "\" (" + n.Type + ") needs settings." + key
			}
			if _, ok := byName[v]; !ok {
				return "node \"" + n.Name + "\" settings." + key + " \"" + v + "\" does not exist"
			}
			return ""
		}
		switch n.Type {
		case "Bridge":
			if r := ref("pair"); r != "" {
				return r
			}
		case "RealityServer":
			if r := ref("destination"); r != "" {
				return r
			}
		}
	}
	return ""
}

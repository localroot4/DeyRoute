package front

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/localroot4/deyroute/internal/config"
	"github.com/localroot4/deyroute/internal/exec"
)

// ShimFileName is the file in a front node's backend config directory that
// configures the shim of that instance (`deyroute front-shim --config`).
const ShimFileName = "front-shim.json"

// ShimCommand is the deyroute subcommand that runs the shim.
const ShimCommand = "front-shim"

// maxShimFile bounds the shim file that is read.
const maxShimFile = 64 << 10

// ShimFile is the content of ShimFileName. The hub renders Port, Tunnel,
// Node, Token and CA; the node adds how it reaches the front (Hub, Scheme,
// EdgeIP, Secret) from its own config before it writes the file, so the
// hub never sends the node's front settings and the unit, which runs as the
// deyroute user, never needs the node's secrets directory.
type ShimFile struct {
	Port   int    `json:"port"`
	Tunnel string `json:"tunnel"`
	Node   string `json:"node"`
	Token  string `json:"token"`
	CA     string `json:"ca"`

	Hub    string `json:"hub,omitempty"` // front host:port (node.hub_addr)
	Scheme string `json:"scheme,omitempty"`
	EdgeIP string `json:"edge_ip,omitempty"`
	Secret string `json:"secret,omitempty"`
}

// ParseShimFile decodes and checks the hub part of a shim file.
func ParseShimFile(b []byte) (ShimFile, error) {
	var f ShimFile
	if len(b) > maxShimFile {
		return f, errors.New("front: shim file too large")
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("front: shim file: %w", err)
	}
	switch {
	case f.Port < config.CtlRangeLow || f.Port > config.CtlRangeHigh:
		return f, fmt.Errorf("front: shim port %d is outside %d-%d", f.Port, config.CtlRangeLow, config.CtlRangeHigh)
	case !config.ValidID(f.Tunnel):
		return f, errors.New("front: shim file has no valid tunnel id")
	case f.Node == "" || len(f.Node) > maxNodeIDLen:
		return f, errors.New("front: shim file has no valid node id")
	case f.Token == "":
		return f, errors.New("front: shim file has no token")
	case f.CA == "":
		return f, errors.New("front: shim file has no CA")
	}
	return f, nil
}

// Complete reports whether the node part is filled in.
func (f ShimFile) Complete() bool { return f.Hub != "" && f.Secret != "" }

// Target returns the front the shim dials.
func (f ShimFile) Target() (Target, error) {
	if !f.Complete() {
		return Target{}, errors.New("front: the shim file has no front address or secret (the node is not in front mode)")
	}
	return NewTarget(f.Hub, f.Scheme, f.EdgeIP, f.Secret)
}

// Marshal encodes f.
func (f ShimFile) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ReadShimFile reads and checks a complete shim file.
func ReadShimFile(path string) (ShimFile, error) {
	b, err := os.ReadFile(filepath.Clean(path)) // #nosec G304 -- the unit's own config file
	if err != nil {
		return ShimFile{}, err
	}
	f, err := ParseShimFile(b)
	if err != nil {
		return f, err
	}
	if !f.Complete() {
		return f, errors.New("front: the shim file has no front address or secret (the node is not in front mode)")
	}
	return f, nil
}

// ShimArgv returns the ExecStart that runs the shim next to argv, the
// backend client: `<self> pair <self> front-shim --config <dir>/front-shim.json -- argv...`.
func ShimArgv(self, configDir string, argv []string) []string {
	out := []string{self, exec.PairCommand, self, ShimCommand, "--config", filepath.Join(configDir, ShimFileName), exec.PairSeparator}
	return append(out, argv...)
}

// IsShimArgv reports whether argv is exactly the shim member of a pair for
// configDir (what a node accepts in a unit).
func IsShimArgv(self, configDir string, argv []string) bool {
	return slices.Equal(argv, []string{self, ShimCommand, "--config", filepath.Join(configDir, ShimFileName)})
}

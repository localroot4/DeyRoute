package backend

import (
	"bytes"
	_ "embed"
	"os"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	deyerr "github.com/localroot4/deyroute/internal/errors"
)

//go:embed backends.yaml
var embeddedManifest []byte

// ManifestFile is the on-disk form of backends.yaml.
type ManifestFile struct {
	ManifestVersion int                      `yaml:"manifest_version"`
	Backends        map[string]ManifestEntry `yaml:"backends"`
}

var (
	manMu      sync.RWMutex
	manCurrent *ManifestFile
)

// EmbeddedManifest returns the raw embedded backends.yaml.
func EmbeddedManifest() []byte { return embeddedManifest }

// ParseManifest decodes backends.yaml strictly.
func ParseManifest(data []byte) (*ManifestFile, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m ManifestFile
	if err := dec.Decode(&m); err != nil {
		return nil, deyerr.Wrap(deyerr.S005, err, deyerr.Params{"file": "backends.yaml"})
	}
	for name, e := range m.Backends {
		e.Name = name
		m.Backends[name] = e
	}
	return &m, nil
}

// SetManifest replaces the active manifest (after verifying an override).
func SetManifest(m *ManifestFile) {
	manMu.Lock()
	defer manMu.Unlock()
	manCurrent = m
}

// LoadManifestOverride activates the manifest at path when it exists.
// Signature verification of downloaded manifests happens in internal/install
// before the file is written there.
func LoadManifestOverride(path string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed root-owned path
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	m, err := ParseManifest(data)
	if err != nil {
		return err
	}
	SetManifest(m)
	return nil
}

func active() *ManifestFile {
	manMu.RLock()
	m := manCurrent
	manMu.RUnlock()
	if m != nil {
		return m
	}
	m, err := ParseManifest(embeddedManifest)
	if err != nil {
		// The embedded manifest is covered by tests; an empty manifest makes
		// every lookup fail with DEY-B008 instead of crashing.
		m = &ManifestFile{Backends: map[string]ManifestEntry{}}
	}
	manMu.Lock()
	if manCurrent == nil {
		manCurrent = m
	}
	manMu.Unlock()
	return m
}

// ManifestFor returns the active manifest entry of backend name. Unknown
// names return an entry with only Name set; installers treat a missing
// Version as DEY-B008.
func ManifestFor(name string) ManifestEntry {
	e, ok := active().Backends[name]
	if !ok {
		return ManifestEntry{Name: name}
	}
	return e
}

// ResolveURL expands "{mirror}" in a manifest URL with the release base.
func ResolveURL(url, mirror string) string {
	return strings.ReplaceAll(url, "{mirror}", strings.TrimRight(mirror, "/"))
}

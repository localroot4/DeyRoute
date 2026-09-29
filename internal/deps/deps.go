//go:build deps

// Package deps pins the approved third-party modules (section 15) in go.mod
// before every package that uses them exists. It is never compiled into the
// binary (build tag "deps") and will be removed once all imports are real.
package deps

import (
	_ "aead.dev/minisign"
	_ "filippo.io/age"
	_ "github.com/charmbracelet/bubbles/textinput"
	_ "github.com/charmbracelet/bubbletea"
	_ "github.com/charmbracelet/lipgloss"
	_ "github.com/go-acme/lego/v4/lego"
	_ "github.com/go-acme/lego/v4/providers/dns/cloudflare"
	_ "github.com/spf13/cobra"
	_ "github.com/stretchr/testify/require"
	_ "go.etcd.io/bbolt"
	_ "go.uber.org/goleak"
	_ "golang.org/x/crypto/curve25519"
	_ "golang.org/x/net/http2"
	_ "golang.org/x/sys/unix"
	_ "gopkg.in/yaml.v3"
)

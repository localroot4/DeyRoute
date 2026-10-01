// Command deyroute is the DEYROUTE Tunnel Manager: one static binary with two
// roles (hub and node). Without arguments it opens the interactive menu;
// /usr/local/bin/dey is a symlink to it and behaves the same.
package main

import (
	"os"

	// Every tunnel backend registers itself (config validation, rendering).
	_ "github.com/localroot4/deyroute/internal/backend/all"
	"github.com/localroot4/deyroute/internal/cli"
	buildinfo "github.com/localroot4/deyroute/internal/version"
)

// Set by the linker: -X main.version=… -X main.commit=… -X main.date=…
var (
	version = ""
	commit  = ""
	date    = ""
)

func main() {
	buildinfo.Set(version, commit, date)
	os.Exit(cli.Execute(os.Args[1:]))
}

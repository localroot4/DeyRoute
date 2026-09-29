// Package version carries build metadata injected by the linker into main
// (-X main.version=… -X main.commit=… -X main.date=…) and copied here by Set.
package version

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

var (
	// Version is the SemVer release, e.g. "1.0.0" (no leading v). "dev" for local builds.
	Version = "dev"
	// Commit is the short git commit.
	Commit = "none"
	// Date is the UTC build date (RFC3339).
	Date = "unknown"
)

// Set installs build metadata; empty values are ignored.
func Set(v, c, d string) {
	if v != "" {
		Version = strings.TrimPrefix(v, "v")
	}
	if c != "" {
		Commit = c
	}
	if d != "" {
		Date = d
	}
}

// GoVersion returns the Go toolchain version the binary was built with.
func GoVersion() string { return runtime.Version() }

// Display returns "v1.0.0" style text for banners ("dev" stays "dev").
func Display() string {
	if Version == "dev" {
		return "dev"
	}
	return "v" + Version
}

// MajorMinor parses "1.2.3" into (1, 2). Unparseable versions (e.g. "dev") return ok=false.
func MajorMinor(v string) (major, minor int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	ma, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(strings.SplitN(parts[1], "-", 2)[0])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return ma, mi, true
}

// Compatible reports whether hub and node versions share major.minor.
// "dev" builds are compatible only with other "dev" builds.
func Compatible(hub, node string) bool {
	if hub == node {
		return true
	}
	hma, hmi, ok1 := MajorMinor(hub)
	nma, nmi, ok2 := MajorMinor(node)
	if !ok1 || !ok2 {
		return false
	}
	return hma == nma && hmi == nmi
}

// String is the full `deyroute version` line.
func String() string {
	return fmt.Sprintf("deyroute %s (commit %s, built %s, %s %s/%s)", Display(), Commit, Date, GoVersion(), runtime.GOOS, runtime.GOARCH)
}

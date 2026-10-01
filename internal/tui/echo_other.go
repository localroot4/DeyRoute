//go:build !linux

package tui

import "io"

// echoControl is only implemented on Linux, the platform deyroute runs on.
func echoControl(io.Reader) func(on bool) { return nil }

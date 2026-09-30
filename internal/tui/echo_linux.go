package tui

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// echoControl returns a function that switches the terminal echo of in on
// or off, or nil when in is not a terminal. Line mode turns echo off while a
// passphrase is typed so that it never appears on screen.
func echoControl(in io.Reader) func(on bool) {
	f, ok := in.(*os.File)
	if !ok {
		return nil
	}
	fd := int(f.Fd()) // #nosec G115 -- file descriptors fit in int
	if !term.IsTerminal(fd) {
		return nil
	}
	return func(on bool) {
		t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
		if err != nil {
			return
		}
		if on {
			t.Lflag |= unix.ECHO
		} else {
			t.Lflag &^= unix.ECHO
		}
		_ = unix.IoctlSetTermios(fd, unix.TCSETS, t)
	}
}

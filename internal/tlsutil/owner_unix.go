//go:build unix

package tlsutil

import (
	"io/fs"
	"syscall"
)

// fileUID returns the owner uid of info when the platform exposes it.
func fileUID(info fs.FileInfo) (uint32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

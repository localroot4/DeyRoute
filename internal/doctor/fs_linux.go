//go:build linux

package doctor

import (
	"os"
	"syscall"
)

// logOpenFlags is added to the read-only open of a log file: O_NONBLOCK so
// a FIFO swapped in after the directory walk cannot block the collector
// (the opened file is rejected by its fstat right after).
const logOpenFlags = syscall.O_NONBLOCK

// singleLink reports whether fi has exactly one hard link. A log file with
// several links may be a link to a file outside the log directory created
// by the unprivileged backend user, so it is not read.
func singleLink(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return !ok || st.Nlink == 1
}

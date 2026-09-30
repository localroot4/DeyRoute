//go:build !linux

package doctor

import "os"

// logOpenFlags adds nothing outside Linux (servers run Linux only).
const logOpenFlags = 0

// singleLink cannot be checked portably; Linux is the only server OS.
func singleLink(os.FileInfo) bool { return true }

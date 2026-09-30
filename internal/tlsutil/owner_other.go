//go:build !unix

package tlsutil

import "io/fs"

// fileUID is unavailable on non-unix platforms; the owner check is skipped.
func fileUID(fs.FileInfo) (uint32, bool) { return 0, false }

//go:build !linux

package doctor

import "errors"

// statfs is only implemented on Linux, the only supported server OS.
func statfs(string) (total, avail uint64, err error) {
	return 0, 0, errors.New("statfs is only supported on linux")
}

//go:build linux

package doctor

import "golang.org/x/sys/unix"

// statfs returns the total and available (to unprivileged users, as df
// shows it) bytes of the filesystem holding path.
func statfs(path string) (total, avail uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize) // #nosec G115 -- block size is a small positive number
	return st.Blocks * bsize, st.Bavail * bsize, nil
}

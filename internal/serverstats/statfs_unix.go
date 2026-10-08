//go:build unix

package serverstats

import (
	"errors"

	"golang.org/x/sys/unix"
)

var errUnsupported = errors.New("not supported on this system")

// statfs returns the size of the filesystem holding path and the space an
// unprivileged process can still use on it.
func statfs(path string) (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize) //nolint:gosec // G115: a block size is positive
	return st.Blocks * bsize, st.Bavail * bsize, nil
}

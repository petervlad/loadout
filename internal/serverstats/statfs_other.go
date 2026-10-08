//go:build !unix

package serverstats

import "errors"

var errUnsupported = errors.New("not supported on this system")

// statfs is only implemented where the registry is meant to run.
func statfs(string) (total, free uint64, err error) { return 0, 0, errUnsupported }

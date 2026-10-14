//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import "golang.org/x/sys/unix"

func graphConversionFreeBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), nil //nolint:unconvert // Bavail signedness differs across supported Unix targets.
}

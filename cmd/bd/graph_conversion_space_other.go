//go:build !(darwin || linux || freebsd || openbsd || netbsd || dragonfly || windows)

package main

import "errors"

func graphConversionFreeBytes(string) (uint64, error) {
	return 0, errors.New("conversion disk-space qualification is unavailable on this platform")
}

//go:build !cgo

package main

import (
	"context"
	"errors"
)

func graphConversionPruneCandidate(_ context.Context, _ *graphConversionReceipt) error {
	return errors.New("graph conversion candidate pruning requires CGO (build with CGO_ENABLED=1)")
}

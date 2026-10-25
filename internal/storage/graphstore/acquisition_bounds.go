package graphstore

import (
	"context"
	"database/sql"
	"fmt"

	txmemory "github.com/steveyegge/beads/internal/storage/memoryops"
)

// Continuity projections share the database, but have a separate acquisition
// budget from canonical graph records. These limits include keys and row costs;
// they do not measure Go heap, engine caches or retained Dolt history.
const (
	PreviewContinuityReadByteLimit = 64 << 20
	PreviewContinuityReadRowLimit  = 65536
)

type acquisitionUsage struct {
	resources, currentBytes, continuityRows, continuityBytes uint64
}

func continuityReadUsage(ctx context.Context, tx *sql.Tx) (rows, bytes uint64, err error) {
	prefix := txmemory.StorageKey("")
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*), CAST(LEAST(COALESCE(SUM(256+OCTET_LENGTH(`key`)+COALESCE(OCTET_LENGTH(value),0)),0),?) AS UNSIGNED) FROM config WHERE BINARY LEFT(`key`, CHAR_LENGTH(?)) = BINARY ?", uint64(1)<<60, prefix, prefix).Scan(&rows, &bytes)
	return rows, bytes, err
}

func checkContinuityReadBounds(ctx context.Context, tx *sql.Tx) error {
	rows, bytes, err := continuityReadUsage(ctx, tx)
	if err != nil {
		return err
	}
	if rows > PreviewContinuityReadRowLimit || bytes > PreviewContinuityReadByteLimit {
		return fmt.Errorf("%w: continuity exceeds %d rows or %d acquisition bytes before payload decoding", ErrLimitExceeded, PreviewContinuityReadRowLimit, PreviewContinuityReadByteLimit)
	}
	return nil
}

func readAcquisitionUsage(ctx context.Context, tx *sql.Tx) (acquisitionUsage, error) {
	var usage acquisitionUsage
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_preview_catalog WHERE allocation_state='live'").Scan(&usage.resources); err != nil {
		return usage, err
	}
	var err error
	usage.currentBytes, err = currentReadBytes(ctx, tx)
	if err != nil {
		return usage, err
	}
	usage.continuityRows, usage.continuityBytes, err = continuityReadUsage(ctx, tx)
	return usage, err
}

// The selected policy bounds atomic growth. Old or out-of-band oversized state
// may be reduced or left unchanged. A dimension already over budget may not
// grow; every other dimension stays under its own cap, including terminal
// bookkeeping required to remove a Resource. Checks stay in the transaction.
func checkAcquisitionGrowth(before, after acquisitionUsage) error {
	return checkProfileGrowth("", before, after)
}

func checkGrowthDimensions(before, after acquisitionUsage, limits CapacityLimits) error {
	dimensions := [...]struct{ limit, before, after uint64 }{
		{limits.Resources, before.resources, after.resources},
		{limits.CurrentBytes, before.currentBytes, after.currentBytes},
		{limits.RetainedRows, before.continuityRows, after.continuityRows},
		{limits.RetainedBytes, before.continuityBytes, after.continuityBytes},
	}
	for _, dimension := range dimensions {
		if dimension.after > dimension.limit && dimension.after > dimension.before {
			return fmt.Errorf("%w: graph write would exceed or grow an existing acquisition limit", ErrLimitExceeded)
		}
	}
	return nil
}

package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func checkCurrentIssueAuthority(ctx context.Context, tx *sql.Tx) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wisps w JOIN graph_preview_catalog c ON c.backing='issue' AND c.backing_key=w.id WHERE c.allocation_state='live'`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("%w: live graph Issue also names a wisp", ErrInvalidStore)
	}
	return nil
}

// currentReadBytes measures sizes in SQL without hydrating retained bodies.
func currentReadBytes(ctx context.Context, tx *sql.Tx) (uint64, error) {
	var used uint64
	for _, part := range currentReadSizeQueries() {
		fields := []string{"256"}
		for _, column := range strings.Split(part.columns, ",") {
			fields = append(fields, "COALESCE(OCTET_LENGTH("+strings.TrimSpace(column)+"),0)")
		}
		var size uint64
		query := "SELECT CAST(LEAST(COALESCE(SUM((" + strings.Join(fields, "+") + ")*?),0),?) AS UNSIGNED) FROM " + part.from
		if err := tx.QueryRowContext(ctx, query, part.copies, uint64(1)<<60).Scan(&size); err != nil {
			return 0, err
		}
		used += size
	}
	return used, nil
}

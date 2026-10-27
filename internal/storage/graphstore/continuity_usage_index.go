package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ContinuityUsageIndexDDL is an explicit offline optimization, never run by an
// ordinary open. Dolt maintains the stored expression and covering index for all
// writers, including older clients and raw SQL; original key/value rows and all
// prior commits remain intact. Install only under the existing exclusive gates.
var ContinuityUsageIndexDDL = []string{
	"ALTER TABLE config ADD COLUMN __beads_continuity_bytes BIGINT UNSIGNED GENERATED ALWAYS AS (256+OCTET_LENGTH(`key`)+COALESCE(OCTET_LENGTH(value),0)) STORED",
	"CREATE INDEX beads_continuity_usage ON config (`key`, __beads_continuity_bytes)",
}

// Validate the generated expression inside the caller's snapshot. A plain or
// altered column must never be trusted as a capacity measurement. An absent
// optimization keeps the exact aggregate fallback, without implicit DDL.
func hasContinuityUsageIndex(ctx context.Context, tx *sql.Tx) (bool, error) {
	var kind, extra, expression string
	err := tx.QueryRowContext(ctx, "SELECT COLUMN_TYPE, EXTRA, GENERATION_EXPRESSION FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='config' AND COLUMN_NAME='__beads_continuity_bytes'").Scan(&kind, &extra, &expression)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	compact := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }
	if kind != "bigint unsigned" || extra != "STORED GENERATED" || compact(expression) != "((256+length(`key`))+coalesce(length(`value`),0))" {
		return false, fmt.Errorf("%w: unrecognized continuity usage expression", ErrInvalidStore)
	}
	rows, err := tx.QueryContext(ctx, "SELECT COLUMN_NAME, NON_UNIQUE, SUB_PART FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='config' AND INDEX_NAME='beads_continuity_usage' ORDER BY SEQ_IN_INDEX")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		var nonunique int
		var part sql.NullInt64
		if err = rows.Scan(&name, &nonunique, &part); err != nil {
			return false, err
		}
		if nonunique != 1 || part.Valid {
			return false, fmt.Errorf("%w: unrecognized continuity usage index", ErrInvalidStore)
		}
		columns = append(columns, name)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return false, err
	}
	if len(columns) == 0 {
		return false, nil
	}
	if len(columns) != 2 || columns[0] != "key" || columns[1] != "__beads_continuity_bytes" {
		return false, fmt.Errorf("%w: unrecognized continuity usage index", ErrInvalidStore)
	}
	return true, nil
}

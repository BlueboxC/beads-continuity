package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/steveyegge/beads/internal/activity"
	txmemory "github.com/steveyegge/beads/internal/storage/memoryops"
)

// ReadObservedActivity retains only the requested recent observations. Counts
// and invalid-envelope diagnostics still cover every selected stored event.
func (s *Store) ReadObservedActivity(ctx context.Context, session, turn string, limit int, summariesOnly bool) (activity.View, error) {
	if limit <= 0 || limit > 1000 {
		return activity.View{}, ErrLimitExceeded
	}
	var result activity.View
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		result, err = readObservedActivityInTx(ctx, tx, session, turn, limit, summariesOnly)
		return err
	})
	if err != nil {
		return activity.View{}, err
	}
	return result, nil
}

func readObservedActivityInTx(ctx context.Context, tx *sql.Tx, session, turn string, limit int, summariesOnly bool) (activity.View, error) {
	var settings []byte
	err := tx.QueryRowContext(ctx, "SELECT CASE WHEN OCTET_LENGTH(value)<=? THEN value ELSE NULL END FROM config WHERE `key`=?", activity.MaxRecord, txmemory.StorageKey(activity.SettingsKey)).Scan(&settings)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return activity.View{}, err
	}
	prefix := txmemory.StorageKey(activity.Prefix + "event/")
	rows, err := tx.QueryContext(ctx, "SELECT `key`,CASE WHEN OCTET_LENGTH(value)<=? THEN value ELSE NULL END FROM config WHERE BINARY LEFT(`key`,CHAR_LENGTH(?))=BINARY ?", activity.MaxRecord, prefix, prefix)
	if err != nil {
		return activity.View{}, err
	}
	defer rows.Close()
	var readErr error
	iterator := func(yield func(string, string) bool) {
		for rows.Next() {
			var key string
			var value []byte
			if readErr = rows.Scan(&key, &value); readErr != nil {
				return
			}
			if !yield(strings.TrimPrefix(key, txmemory.StorageKey("")), string(value)) {
				return
			}
		}
		readErr = rows.Err()
	}
	result := activity.ReadRows(string(settings), iterator, session, turn, limit, summariesOnly)
	if err := errors.Join(readErr, rows.Close()); err != nil {
		return activity.View{}, err
	}
	return result, nil
}

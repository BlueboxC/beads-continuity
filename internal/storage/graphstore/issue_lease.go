package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// HeartbeatIssue refreshes the native node-local lease without changing
// retained History. Re-arming a missing lease is a durable native mutation
// and advances both the Issue version and its graph revision.
func (s *Store) HeartbeatIssue(ctx context.Context, path, actor string) (IssueRecord, error) {
	return s.HeartbeatIssueSelected(ctx, IssueSelector{Path: path}, actor)
}

// HeartbeatIssueSelected resolves a bare native ID and renews the lease in
// one transaction; explicit graph paths never fall back to native IDs.
func (s *Store) HeartbeatIssueSelected(ctx context.Context, selector IssueSelector, actor string) (IssueRecord, error) {
	if err := validateIssueSelector(selector); err != nil {
		return IssueRecord{}, err
	}
	if actor == "" || !utf8.ValidString(actor) {
		return IssueRecord{}, fmt.Errorf("%w: heartbeat requires a nonempty UTF-8 actor", storage.ErrValidation)
	}
	if err := types.CheckFieldLen("actor", actor); err != nil {
		return IssueRecord{}, err
	}
	var result IssueRecord
	err := s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		path, before, err := s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
		if err != nil {
			return err
		}
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		renewal, err := issueops.RenewLeaseInTx(ctx, tx, before.Properties.ID, actor)
		if err != nil {
			return err
		}
		if renewal.Rearmed {
			if err := s.touchCoordination(ctx, tx); err != nil {
				return err
			}
			if err := s.recordIssueMappingInTx(ctx, tx, path, before.Properties.ID, actor); err != nil {
				return err
			}
		}
		result, err = s.showIssueInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		if (result.Revision != before.Revision) != renewal.Rearmed {
			return fmt.Errorf("%w: heartbeat revision disagrees with lease re-arm", ErrInvalidStore)
		}
		return nil
	})
	return result, err
}

// ReclaimExpiredIssueLeases gives the native reaper only graph-mapped Issue
// IDs, then maps every durable native recovery version into graph History in
// the same transaction. It returns native local Issue IDs, as the ordinary
// reclaim command does. An empty sweep leaves even coordination untouched.
func (s *Store) ReclaimExpiredIssueLeases(ctx context.Context, olderThan time.Duration, filter types.ReclaimFilter, actor string) ([]types.ReclaimedLease, error) {
	return s.ReclaimExpiredIssueLeasesSelected(ctx, olderThan, filter, nil, actor)
}

// ReclaimExpiredIssueLeasesSelected resolves scoped CLI selectors in the
// recovery transaction, so a concurrent catalog change cannot retarget the
// native Issue IDs selected for recovery.
func (s *Store) ReclaimExpiredIssueLeasesSelected(ctx context.Context, olderThan time.Duration, filter types.ReclaimFilter, selectors []IssueSelector, actor string) ([]types.ReclaimedLease, error) {
	if olderThan < 0 || actor == "" || !utf8.ValidString(actor) {
		return nil, fmt.Errorf("%w: invalid reclaim grace or actor", storage.ErrValidation)
	}
	if err := types.CheckFieldLen("actor", actor); err != nil {
		return nil, err
	}
	for _, selector := range selectors {
		if err := validateIssueSelector(selector); err != nil {
			return nil, err
		}
	}
	var reclaimed []types.ReclaimedLease
	err := s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if len(selectors) > 0 {
			filter.IDs = make([]string, 0, len(selectors))
			for _, selector := range selectors {
				_, issue, err := s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
				if err != nil {
					return err
				}
				filter.IDs = append(filter.IDs, issue.Properties.ID)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT backing_key,path FROM graph_preview_catalog WHERE backing='issue' AND allocation_state='live'`)
		if err != nil {
			return err
		}
		paths := make(map[string]string)
		for rows.Next() {
			var id, path string
			if err := rows.Scan(&id, &path); err != nil {
				_ = rows.Close()
				return err
			}
			paths[id] = path
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		eligible := make([]string, 0, len(paths))
		for id := range paths {
			if len(filter.IDs) == 0 || containsReclaimID(filter.IDs, id) {
				eligible = append(eligible, id)
			}
		}
		if len(eligible) == 0 {
			return nil
		}
		filter.IDs = eligible
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		reclaimed, err = issueops.ReclaimExpiredLeasesInTx(ctx, tx, time.Now().UTC().Add(-olderThan), filter, actor)
		if err != nil || len(reclaimed) == 0 {
			return err
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		for i := range reclaimed {
			path, ok := paths[reclaimed[i].ID]
			if !ok {
				return fmt.Errorf("%w: reclaimed an unmapped Issue", ErrInvalidStore)
			}
			if err := s.recordIssueMappingInTx(ctx, tx, path, reclaimed[i].ID, actor); err != nil {
				return err
			}
			if _, err := s.showIssueInTx(ctx, tx, path); err != nil {
				return err
			}
		}
		return nil
	})
	return reclaimed, err
}

func containsReclaimID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

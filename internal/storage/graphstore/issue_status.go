package graphstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// IssueStatistics computes the native durable Issue summary after checking
// that every counted Issue has a live graph mapping.
func (s *Store) IssueStatistics(ctx context.Context) (*types.Statistics, error) {
	var stats *types.Statistics
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if err := s.checkIssueListCatalogInTx(ctx, tx); err != nil {
			return err
		}
		var mapped, native int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_catalog WHERE backing='issue' AND allocation_state='live'`).Scan(&mapped); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues`).Scan(&native); err != nil {
			return err
		}
		if mapped != native {
			return fmt.Errorf("%w: native Issue count differs from graph mappings", ErrInvalidStore)
		}
		var err error
		stats, err = issueops.GetStatisticsInTx(ctx, tx)
		return err
	})
	return stats, err
}

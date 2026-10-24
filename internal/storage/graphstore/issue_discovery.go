package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// SearchIssueRows is the checked, read-only Issue discovery seam. It keeps the
// native Issue filter and count projection, but replaces backing IDs with exact
// public IDs after validating every selected complete graph record in the same
// snapshot. Callers must request durable scope; the preview does not read wisps.
func (s *Store) SearchIssueRows(ctx context.Context, query string, filter types.IssueFilter) ([]*types.IssueWithCounts, error) {
	if !filter.SkipWisps || (filter.Ephemeral != nil && *filter.Ephemeral) {
		return nil, fmt.Errorf("%w: graph Issue discovery requires durable scope", ErrCapabilityUnavailable)
	}
	// The graph catalog has at most PreviewSnapshotLimit live Resources, so no
	// admitted Issue can be lost by this acquisition bound.
	filter.Limit = PreviewSnapshotLimit
	var result []*types.IssueWithCounts
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if err := s.checkCollectionMappingsInTx(ctx, tx); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_preview_catalog WHERE allocation_state='live'").Scan(&count); err != nil {
			return err
		}
		if count > PreviewSnapshotLimit {
			return fmt.Errorf("%w: graph Issue discovery exceeds %d live Resources", ErrLimitExceeded, PreviewSnapshotLimit)
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_preview_catalog WHERE allocation_state NOT IN ('live','deleted')").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("%w: unknown allocation state", ErrInvalidStore)
		}
		if err := s.checkIssueListCatalogInTx(ctx, tx); err != nil {
			return err
		}
		rows, err := issueops.SearchIssuesWithCountsInTx(ctx, tx, query, filter)
		if err != nil {
			var capError *issueops.ErrTooManyRows
			if errors.As(err, &capError) {
				return fmt.Errorf("%w: %w", ErrLimitExceeded, err)
			}
			return err
		}
		for _, row := range rows {
			if row == nil || row.Issue == nil {
				return fmt.Errorf("%w: nil Issue discovery row", ErrInvalidStore)
			}
			var path string
			if err := tx.QueryRowContext(ctx, "SELECT path FROM graph_preview_catalog WHERE backing='issue' AND backing_key=? AND allocation_state='live'", row.ID).Scan(&path); err != nil {
				return fmt.Errorf("%w: unmapped discovered Issue: %v", ErrInvalidStore, err)
			}
			record, err := s.showIssueInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			row.ID = strings.TrimPrefix(record.ID, s.ScopeURL())
			if row.Parent != nil {
				var parentPath string
				if err := tx.QueryRowContext(ctx, "SELECT path FROM graph_preview_catalog WHERE backing='issue' AND backing_key=? AND allocation_state='live'", *row.Parent).Scan(&parentPath); err != nil {
					return fmt.Errorf("%w: unmapped discovered parent Issue: %v", ErrInvalidStore, err)
				}
				if err := validatePath(parentPath); err != nil {
					return fmt.Errorf("%w: malformed discovered parent Issue", ErrInvalidStore)
				}
				parent, err := s.showIssueInTx(ctx, tx, parentPath)
				if err != nil {
					return err
				}
				*row.Parent = strings.TrimPrefix(parent.ID, s.ScopeURL())
			}
		}
		if rows == nil {
			rows = []*types.IssueWithCounts{}
		}
		result = rows
		return nil
	})
	return result, err
}

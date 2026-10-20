package issueops

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// batchHierarchyChecker belongs to one create transaction. Only parent edges
// determine ancestry, and that phase finishes before this closure is called.
func batchHierarchyChecker(ctx context.Context, tx DBTX) func(*types.Dependency) error {
	var parents map[string][]string
	return func(dep *types.Dependency) error {
		if dep.Type != types.DepBlocks && dep.Type != types.DepConditionalBlocks {
			return nil
		}
		if dep.IssueID == dep.DependsOnID {
			return nil
		}
		if parents == nil {
			var unions []string
			for _, table := range cycleDetectionTables() {
				unions = append(unions, fmt.Sprintf("SELECT issue_id, %s AS parent_id FROM %s WHERE type = 'parent-child'", DepTargetExpr, table))
			}
			//nolint:gosec // G201: table names and target expression are fixed constants.
			rows, err := tx.QueryContext(ctx, strings.Join(unions, " UNION "))
			if err != nil {
				return fmt.Errorf("failed to check blocker ancestry: %w", err)
			}
			loaded := make(map[string][]string)
			for rows.Next() {
				var child, parent string
				if err := rows.Scan(&child, &parent); err != nil {
					_ = rows.Close()
					return fmt.Errorf("failed to check blocker ancestry: %w", err)
				}
				loaded[child] = append(loaded[child], parent)
			}
			readErr := rows.Err()
			closeErr := rows.Close()
			if readErr != nil {
				return fmt.Errorf("failed to check blocker ancestry: %w", readErr)
			}
			if closeErr != nil {
				return fmt.Errorf("failed to check blocker ancestry: %w", closeErr)
			}
			parents = loaded
		}
		return checkBlockingHierarchy(dep, func(node, candidate string) (bool, error) {
			seen := make(map[string]bool)
			pending := []string{node}
			for len(pending) > 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
				n := pending[len(pending)-1]
				pending = pending[:len(pending)-1]
				if n == candidate {
					return true, nil
				}
				if !seen[n] {
					seen[n] = true
					pending = append(pending, parents[n]...)
				}
			}
			return false, nil
		})
	}
}

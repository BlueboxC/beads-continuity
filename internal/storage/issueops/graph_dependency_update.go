package issueops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// WriteGraphDependencyInTx composes a checked graph Link edit through native
// integrity, readiness, journal and versioning seams. The caller owns graph
// identity guards and the transaction. Recovery cannot adopt an occupied pair.
func WriteGraphDependencyInTx(ctx context.Context, tx *sql.Tx, dep types.Dependency, actor string, recoverDeleted bool) error {
	if err := ValidateAddDependenciesRequest(publicops.AddDependenciesRequest{Actor: actor, Edges: []publicops.DependencyEdge{{IssueID: dep.IssueID, DependsOnID: dep.DependsOnID, Type: dep.Type}}}); err != nil {
		return err
	}
	var previousType string
	err := tx.QueryRowContext(ctx, `SELECT type FROM dependencies WHERE issue_id=? AND depends_on_issue_id=?`, dep.IssueID, dep.DependsOnID).Scan(&previousType)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if recoverDeleted {
		if err == nil {
			return fmt.Errorf("%w: Dependency pair already has a live assertion", storage.ErrValidation)
		}
		kind := DepTargetIssue
		_, err := addDependencyInTx(ctx, tx, &dep, actor, AddDependencyOpts{SourceTable: "issues", TargetTable: "issues", WriteTable: "dependencies", TargetKind: &kind, EmitEvent: true}, nil)
		if err != nil {
			return err
		}
		if dep.ID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE dependencies SET id=? WHERE issue_id=? AND depends_on_issue_id=?`, dep.ID, dep.IssueID, dep.DependsOnID); err != nil {
				return err
			}
		}
	} else {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: checked Dependency disappeared", storage.ErrValidation)
		}
		// Validate the final native graph inside the same transaction. Updating
		// in place preserves creation facts, including for journal consumers:
		// dep_add is an upsert, not a remove followed by a new assertion.
		if _, err := tx.ExecContext(ctx, `UPDATE dependencies SET type=?,metadata=? WHERE issue_id=? AND depends_on_issue_id=?`, dep.Type, dep.Metadata, dep.IssueID, dep.DependsOnID); err != nil {
			return err
		}
		if err := CheckBlockingHierarchyInTx(ctx, tx, &dep, cycleDetectionTables()); err != nil {
			return err
		}
		if err := CheckDependencyCycleInTx(ctx, tx, &dep, cycleDetectionTables()); err != nil {
			return err
		}
		if previousType != string(dep.Type) && dep.Type == types.DepParentChild {
			if err := TouchDependencyCoordinationTableInTx(ctx, tx, dep.DependsOnID, "dependencies"); err != nil {
				return err
			}
		}
		if previousType != string(dep.Type) {
			if err := RecordEventInTable(ctx, tx, "events", dep.IssueID, types.EventDependencyRemoved, actor, fmt.Sprintf("Removed dependency on %s", dep.DependsOnID)); err != nil {
				return err
			}
			if err := RecordEventInTable(ctx, tx, "events", dep.IssueID, types.EventDependencyAdded, actor, fmt.Sprintf("Added dependency: %s %s %s", dep.IssueID, dep.Type, dep.DependsOnID)); err != nil {
				return err
			}
		}
		if err := RecordDepEventInTx(ctx, tx, EventDepAdd, dep.IssueID, string(dep.Type), dep.DependsOnID, dep.Metadata, actor); err != nil {
			return err
		}
	}
	// Kind changes can affect both populations. Metadata carries waits-for
	// gate/also_blocks semantics too. Recompute final state before the one
	// source version, including when the native same-type add returned early.
	for _, kind := range []types.DependencyType{types.DependencyType(previousType), dep.Type} {
		issues, wisps, err := AffectedByDepChangeInTx(ctx, tx, dep.IssueID, dep.DependsOnID, kind)
		if err != nil {
			return err
		}
		if err := RecomputeIsBlockedInTx(ctx, tx, issues, wisps); err != nil {
			return err
		}
	}
	return RecordVersionInTx(ctx, tx, dep.IssueID, actor)
}

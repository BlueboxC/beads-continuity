package graphstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
	publicops "github.com/steveyegge/beads/issueops"
)

func (s *Store) recoverIssueInTx(ctx context.Context, tx *sql.Tx, path string, baseline IssueRecord, terminal BeadTerminalState, generation sql.NullInt64, attempt publicops.UpdateRequest, sourceRevision string) (IssueMutationResult, error) {
	if !generation.Valid {
		return IssueMutationResult{}, fmt.Errorf("%w: native recovery requires separately retained current participation admission", ErrCapabilityUnavailable)
	}
	current := *baseline.Properties
	current.Metadata = baseline.Metadata
	if err := issueops.AuthorizeTemplateUpdate(&current, attempt); err != nil {
		return IssueMutationResult{}, err
	}
	if err := issueops.AuthorizeAssigneeTransfer(ctx, tx, &current, attempt); err != nil {
		return IssueMutationResult{}, err
	}
	if err := issueops.AuthorizeNotesOverwrite(&current, attempt); err != nil {
		return IssueMutationResult{}, err
	}
	patch := attempt.Patch
	// Only the published admitted setter crosswalk supplies historical content.
	// Every other durable member comes from the independent last-current baseline.
	next := current
	next.Title = patch.Title.Value
	next.Description = patch.Description.Value
	next.Design = patch.Design.Value
	next.AcceptanceCriteria = patch.AcceptanceCriteria.Value
	next.Priority = patch.Priority.Value
	next.EstimatedMinutes = patch.EstimatedMinutes.Value
	next.Assignee = patch.Assignee.Value
	next.ExternalRef = patch.ExternalRef.Value
	next.SpecID = patch.SpecID.Value
	next.DueAt = patch.DueAt.Value
	next.Notes = patch.Notes.Value
	if patch.AppendNotes.Set {
		resolved, err := issueops.ResolveMergeOps(&current, issueops.UpdateFields(publicops.IssuePatch{AppendNotes: patch.AppendNotes}))
		if err != nil {
			return IssueMutationResult{}, err
		}
		value, ok := resolved["notes"].(string)
		if !ok {
			return IssueMutationResult{}, fmt.Errorf("%w: native recovery notes composition lost value", ErrInvalidStore)
		}
		next.Notes = value
	}
	metadata, _, err := issueops.ApplyMetadataPatch(baseline.Metadata, patch.Metadata)
	if err != nil {
		return IssueMutationResult{}, err
	}
	next.Metadata = metadata
	// Native deletion removed these current structures. A retained content source
	// or last-current body cannot resurrect their historical membership/liveness.
	next.Labels = nil
	next.Dependencies = nil
	next.Comments = nil
	next.LeaseExpiresAt = nil
	next.HeartbeatAt = nil
	next.LeaseGrantedNode = ""
	if err := s.touchCoordination(ctx, tx); err != nil {
		return IssueMutationResult{}, err
	}
	unScope := issueops.ScopeVersionedHistoryTransaction(tx, true)
	defer unScope()
	if err := issueops.RecoverRetainedIssueInTx(ctx, tx, &next, generation.Int64, attempt.Actor); err != nil {
		return IssueMutationResult{}, err
	}
	if err := s.afterStage("issue-recovery-native"); err != nil {
		return IssueMutationResult{}, err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET allocation_state='live' WHERE path=? AND backing='issue' AND allocation_state='deleted' AND revision=?`, path, terminal.Revision)
	if err != nil {
		return IssueMutationResult{}, err
	}
	if n, err := changed.RowsAffected(); err != nil {
		return IssueMutationResult{}, err
	} else if n != 1 {
		return IssueMutationResult{}, fmt.Errorf("%w: native recovery lost its allocation", ErrInvalidStore)
	}
	if err := s.afterStage("issue-recovery-allocation"); err != nil {
		return IssueMutationResult{}, err
	}
	context := issueRevisionContext{Inception: "revert", HistoricalUpdate: &HistoricalUpdateContext{FromRevision: sourceRevision, PreviousRevision: terminal.Revision}}
	if err := s.recordIssueMappingContextInTx(ctx, tx, path, next.ID, context, attempt.Actor); err != nil {
		return IssueMutationResult{}, err
	}
	if err := checkCurrentReadBytes(ctx, tx); err != nil {
		return IssueMutationResult{}, err
	}
	if err := s.validateTypeStateInTx(ctx, tx); err != nil {
		return IssueMutationResult{}, err
	}
	result, err := s.showIssueInTx(ctx, tx, path)
	if err != nil {
		return IssueMutationResult{}, err
	}
	return IssueMutationResult{Issue: result, Changed: true}, nil
}

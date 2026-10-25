package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/validation"
)

// EpicStatuses projects the ordinary native completion policy onto live graph
// paths in one read transaction. Each counted child must also be a mapped
// immediate parent-child relationship in this workspace.
func (s *Store) EpicStatuses(ctx context.Context) ([]*types.EpicStatus, error) {
	result := []*types.EpicStatus{}
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if err := s.requireRelationshipGenerationInTx(ctx, tx); err != nil {
			return err
		}
		if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE issue_type='epic' AND status!='closed'`).Scan(&count); err != nil {
			return err
		}
		if count > PreviewSnapshotLimit {
			return fmt.Errorf("%w: open epic Issue limit exceeded", ErrLimitExceeded)
		}
		native, err := issueops.GetEpicsEligibleForClosureInTx(ctx, tx)
		if err != nil {
			return err
		}
		for _, status := range native {
			if status == nil || status.Epic == nil {
				return fmt.Errorf("%w: native epic status omitted its Issue", ErrInvalidStore)
			}
			var path string
			if err := tx.QueryRowContext(ctx, `SELECT path FROM graph_preview_catalog WHERE backing='issue' AND backing_key=? AND allocation_state='live'`, status.Epic.ID).Scan(&path); err != nil {
				return fmt.Errorf("%w: unmapped epic Issue: %v", ErrInvalidStore, err)
			}
			epic, err := s.showIssueInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			if epic.Properties.ID != status.Epic.ID || epic.Properties.IssueType != types.TypeEpic {
				return fmt.Errorf("%w: epic path changed during progress read", ErrInvalidStore)
			}
			children, err := s.listChildrenInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			closed, completing := 0, 0
			for _, child := range children {
				if child.Properties.Status == types.StatusClosed {
					closed++
					if !types.IsNonCompletingClose(child.Properties.CloseReason) {
						completing++
					}
				}
			}
			eligible := len(children) > 0 && completing == len(children)
			if len(children) != status.TotalChildren || closed != status.ClosedChildren || eligible != status.EligibleForClose {
				return fmt.Errorf("%w: epic native and graph child progress disagree", ErrInvalidStore)
			}
			projected := *epic.Properties
			projected.ID = strings.TrimPrefix(epic.ID, s.ScopeURL())
			result = append(result, &types.EpicStatus{
				Epic: &projected, TotalChildren: len(children), ClosedChildren: closed, EligibleForClose: eligible,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type EpicCloseRequest struct {
	Path, ExpectedRevision, Reason, Actor string
}

// CloseEligibleEpic rechecks the ordinary completion rule and closes exactly
// one epic through the native Issue writer in the same graph transaction.
func (s *Store) CloseEligibleEpic(ctx context.Context, request EpicCloseRequest) (IssueMutationResult, error) {
	if err := validatePath(request.Path); err != nil {
		return IssueMutationResult{}, err
	}
	if request.ExpectedRevision == "" || request.Actor == "" || !utf8.ValidString(request.Actor) || !utf8.ValidString(request.Reason) {
		return IssueMutationResult{}, fmt.Errorf("%w: epic close requires revision, UTF-8 actor and reason", storage.ErrValidation)
	}
	var result IssueMutationResult
	err := s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if err := s.requireRelationshipGenerationInTx(ctx, tx); err != nil {
			return err
		}
		before, err := s.requireIssueInTx(ctx, tx, request.Path)
		if err != nil {
			return err
		}
		if before.Properties.IssueType != types.TypeEpic {
			return fmt.Errorf("%w: %s is not an epic Issue", storage.ErrValidation, request.Path)
		}
		if err := checkRevisionGuard(request.ExpectedRevision, false, before.Revision, true, "epic Issue"); err != nil {
			return err
		}
		if before.Properties.Status == types.StatusClosed {
			return fmt.Errorf("%w: epic is already closed", ErrConflict)
		}
		if err := validation.Chain(validation.NotTemplate(), validation.NotPinned(false), validation.AssigneeMatches(request.Actor, false))(before.Properties.ID, before.Properties); err != nil {
			return fmt.Errorf("%w: %v", storage.ErrValidation, err)
		}
		statuses, err := issueops.GetEpicsEligibleForClosureInTx(ctx, tx)
		if err != nil {
			return err
		}
		eligible, nativeChildren := false, 0
		for _, status := range statuses {
			if status.Epic != nil && status.Epic.ID == before.Properties.ID {
				eligible = status.EligibleForClose
				nativeChildren = status.TotalChildren
				break
			}
		}
		if !eligible {
			return fmt.Errorf("%w: epic is not eligible for closure", ErrConflict)
		}
		// A graph edge can never be inferred from a dotted native ID. Fail closed
		// if the native eligibility read includes an unmapped child.
		children, err := s.listChildrenInTx(ctx, tx, request.Path)
		if err != nil {
			return err
		}
		if len(children) == 0 || len(children) != nativeChildren {
			return fmt.Errorf("%w: eligible epic has incomplete checked graph children", ErrInvalidStore)
		}
		for _, child := range children {
			if child.Properties.Status != types.StatusClosed || types.IsNonCompletingClose(child.Properties.CloseReason) {
				return fmt.Errorf("%w: child no longer completes epic", ErrConflict)
			}
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		closed, err := issueops.CloseIssueCheckedInTx(ctx, tx, before.Properties.ID, request.Reason, request.Actor, "", false, nil)
		if err != nil {
			return err
		}
		if closed.AlreadyClosed || closed.IsWisp {
			return fmt.Errorf("%w: epic close unexpectedly changed scope or became a no-op", ErrInvalidStore)
		}
		if err := s.afterStage("epic-close-native"); err != nil {
			return err
		}
		if err := s.recordIssueMappingInTx(ctx, tx, request.Path, before.Properties.ID, request.Actor); err != nil {
			return err
		}
		if err := s.afterStage("epic-close-retained"); err != nil {
			return err
		}
		result.Issue, err = s.showIssueInTx(ctx, tx, request.Path)
		result.Changed = err == nil
		return err
	})
	if err != nil {
		return IssueMutationResult{}, err
	}
	return result, nil
}

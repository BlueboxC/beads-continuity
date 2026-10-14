package uow

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/dberrors"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

// MergeSlotCreate creates the slot and its label in one unit of work.
func MergeSlotCreate(ctx context.Context, p UnitOfWorkProvider, actor string) (*types.Issue, error) {
	return RunTxResult(ctx, p, func(ctx context.Context, uw UnitOfWork) (*types.Issue, string, error) {
		id := storage.MergeSlotID(ctx, uw.ConfigUseCase())
		existing, err := uw.IssueUseCase().GetIssue(ctx, id)
		if err == nil && existing != nil {
			return existing, "", nil
		}
		if err != nil && !dberrors.IsNoRows(err) {
			return nil, "", err
		}
		issue := &types.Issue{ID: id, Title: "Merge Slot", Description: "Exclusive access slot for serialized conflict resolution in the merge queue.", IssueType: types.TypeTask, Status: types.StatusOpen, Priority: 0}
		created, err := uw.IssueUseCase().CreateIssue(ctx, domain.CreateIssueParams{Issue: issue, ExplicitID: id, CreateOnly: true, Labels: []string{"gt:slot"}}, actor)
		if err != nil {
			return nil, "", err
		}
		return created.Issue, "bd: create merge slot " + id, nil
	})
}

// MergeSlotCheck reads the slot without committing a transaction.
func MergeSlotCheck(ctx context.Context, p UnitOfWorkProvider) (*storage.MergeSlotStatus, error) {
	return RunTxRead(ctx, p, func(ctx context.Context, uw UnitOfWork) (*storage.MergeSlotStatus, error) {
		id := storage.MergeSlotID(ctx, uw.ConfigUseCase())
		status := &storage.MergeSlotStatus{SlotID: id}
		issue, err := uw.IssueUseCase().GetIssue(ctx, id)
		if dberrors.IsNoRows(err) || (err == nil && issue == nil) {
			return status, fmt.Errorf("merge slot not found: %s: %w", id, storage.ErrNotFound)
		}
		if err != nil {
			return status, err
		}
		status.Available = issue.Status == types.StatusOpen
		var metadata struct {
			Holder  string   `json:"holder"`
			Waiters []string `json:"waiters"`
		}
		if len(issue.Metadata) > 0 {
			if err := json.Unmarshal(issue.Metadata, &metadata); err != nil {
				return nil, err
			}
		}
		status.Holder, status.Waiters = metadata.Holder, metadata.Waiters
		return status, nil
	})
}

// MergeSlotAcquire retries only through the existing transaction runner.
func MergeSlotAcquire(ctx context.Context, p UnitOfWorkProvider, holder, actor string, wait bool) (*storage.MergeSlotResult, error) {
	return RunTxResult(ctx, p, func(ctx context.Context, uw UnitOfWork) (*storage.MergeSlotResult, string, error) {
		id := storage.MergeSlotID(ctx, uw.ConfigUseCase())
		result, err := storage.MergeSlotAcquireInTx(ctx, uw.IssueUseCase(), id, holder, actor, wait)
		if err != nil {
			return nil, "", err
		}
		message := ""
		if result.Acquired || result.Waiting {
			message = fmt.Sprintf("bd: acquire merge slot %s for %s", id, holder)
		}
		return result, message, nil
	})
}

// MergeSlotRelease returns the same slot ID the committed operation used.
func MergeSlotRelease(ctx context.Context, p UnitOfWorkProvider, holder, actor string) (string, error) {
	return RunTxResult(ctx, p, func(ctx context.Context, uw UnitOfWork) (string, string, error) {
		id := storage.MergeSlotID(ctx, uw.ConfigUseCase())
		if err := storage.MergeSlotReleaseInTx(ctx, uw.IssueUseCase(), id, holder, actor); err != nil {
			return "", "", err
		}
		return id, "bd: release merge slot " + id, nil
	})
}

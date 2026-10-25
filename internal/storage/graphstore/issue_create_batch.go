package graphstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	publicops "github.com/steveyegge/beads/issueops"
)

// CreateIssueBatch admits the dependency-free, durable part of ordinary
// markdown creation. It uses the native batch writer and publishes every graph
// identity in that same transaction; a refused item cannot leave earlier
// headings behind. Dependencies need a canonical Link and source-owned History
// mapping in this transaction, and are refused until that mapping exists.
func (s *Store) CreateIssueBatch(ctx context.Context, request publicops.CreateBatchRequest) ([]IssueRecord, error) {
	request = issueops.CloneCreateBatchRequest(request)
	if err := issueops.ValidateCreateBatchRequest(request); err != nil {
		return nil, err
	}
	for index, item := range request.Items {
		if len(item.Dependencies) != 0 {
			return nil, fmt.Errorf("%w: markdown item %d has Dependencies; graph batch create needs atomic canonical Link mapping", ErrCapabilityUnavailable, index)
		}
		if err := validateIssueCreate(issueops.CreateBatchItemRequest(request, item)); err != nil {
			return nil, issueops.CreateBatchItemError(index, err)
		}
		due, err := normalizeIssueDue(item.Issue.DueAt)
		if err != nil {
			return nil, issueops.CreateBatchItemError(index, err)
		}
		item.Issue.DueAt = due
		request.Items[index] = item
	}
	var records []IssueRecord
	err := s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		_, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
		if err != nil {
			return err
		}
		for index, item := range request.Items {
			if issueops.ResolveInfraTypesInTx(ctx, tx)[string(item.Issue.IssueType.Normalize())] {
				return issueops.CreateBatchItemError(index, fmt.Errorf("%w: infrastructure Issue types are not admitted by the graph preview", storage.ErrValidation))
			}
			// Explicit IDs may remain reserved by another Resource or a deleted
			// allocation after the native Issue row is gone. Check before the
			// native batch writer can reuse retained History for that spelling.
			if item.Issue.ID != "" {
				if exists, err := publicResourceIDExistsInTx(ctx, tx, item.Issue.ID); err != nil {
					return issueops.CreateBatchItemError(index, err)
				} else if exists {
					return issueops.CreateBatchItemError(index, ErrAlreadyExists)
				}
			}
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		created, _, err := issueops.ExecuteCreateBatch(ctx, tx, request)
		if err != nil {
			return err
		}
		if err := s.afterStage("issue"); err != nil {
			return err
		}
		// Reserve every prospective Type before checking any new Resource ID.
		// Item order cannot let an earlier Resource claim a later item's Type.
		declaredTypes := make([]string, len(created.Issues))
		for index, issue := range created.Issues {
			if !sameIssueCreateFields(request.Items[index].Issue, issue) || issueops.IsWisp(issue) {
				return fmt.Errorf("%w: markdown item %d cannot be represented exactly as a durable graph Issue", storage.ErrValidation, index)
			}
			declaredTypes[index], err = NativeIssueTypeURL(s.ScopeURL(), string(issue.IssueType))
			if err != nil {
				return err
			}
			if err := s.ensureNativeTypeInTx(ctx, tx, declaredTypes[index]); err != nil {
				return err
			}
		}
		records = make([]IssueRecord, 0, len(created.Issues))
		hasInitialNotes := false
		for index, issue := range created.Issues {
			// Generated IDs are known only after the native batch writer runs.
			// Roll every item back if any public ID is already allocated.
			if exists, err := publicResourceIDExistsInTx(ctx, tx, issue.ID); err != nil {
				return issueops.CreateBatchItemError(index, err)
			} else if exists {
				return issueops.CreateBatchItemError(index, ErrAlreadyExists)
			}
			path, err := generatedIssuePath(ctx, tx, issue.ID)
			if err != nil {
				return err
			}
			revision, err := freshToken()
			if err != nil {
				return err
			}
			var ordinal int64
			if err := issueops.RecordVersionForCreateInTx(ctx, tx, issue.ID, request.Actor); err != nil {
				return err
			}
			if err := tx.QueryRowContext(ctx, `SELECT current_revision FROM issues WHERE id=?`, issue.ID).Scan(&ordinal); err != nil {
				return err
			}
			issueType := declaredTypes[index]
			if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog
				(path,resource_kind,type_url,revision,allocation_state,backing,backing_key)
				VALUES (?,'bead',?,?,'live','issue',?)`, path, issueType, revision, issue.ID); err != nil {
				return err
			}
			if err := s.afterStage("issue-catalog"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_issue_versions
				(path,version,issue_id,issue_revision,owned) VALUES (?,?,?,?,'[]')`, path, revision, issue.ID, ordinal); err != nil {
				return err
			}
			if err := s.afterStage("retained"); err != nil {
				return err
			}
			record, err := s.showIssueInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			records = append(records, record)
			hasInitialNotes = hasInitialNotes || issue.Notes != ""
		}
		if hasInitialNotes {
			if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

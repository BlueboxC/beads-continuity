package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// IssueChildCreateRequest binds an ordinary child create to one checked graph
// parent. The native create writer allocates the dotted ID and parent-child
// Dependency; the graph writer publishes their Issue and Link identities in
// the same transaction. No explicit child ID or other edge is admitted here.
type IssueChildCreateRequest struct {
	ParentPath             string
	ExpectedParentRevision string
	InheritLabels          bool
	Create                 publicops.CreateRequest
}

type IssueChildCreateResult struct {
	Child IssueRecord `json:"child"`
	Link  LinkRecord  `json:"link"`
}

func (s *Store) CreateChildIssue(ctx context.Context, request IssueChildCreateRequest) (IssueChildCreateResult, error) {
	if err := validatePath(request.ParentPath); err != nil {
		return IssueChildCreateResult{}, err
	}
	if request.ExpectedParentRevision == "" {
		return IssueChildCreateResult{}, fmt.Errorf("%w: child create requires the parent revision used for its read", storage.ErrValidation)
	}
	create := issueops.CloneCreateRequest(request.Create)
	if create.Issue == nil || create.Issue.ID != "" {
		return IssueChildCreateResult{}, fmt.Errorf("%w: child ID must be allocated by the native Issue writer", storage.ErrValidation)
	}
	if err := validateIssueCreate(create); err != nil {
		return IssueChildCreateResult{}, err
	}
	due, err := normalizeIssueDue(create.Issue.DueAt)
	if err != nil {
		return IssueChildCreateResult{}, err
	}
	create.Issue.DueAt = due
	var result IssueChildCreateResult
	err = s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if err := s.requireRelationshipGenerationInTx(ctx, tx); err != nil {
			return err
		}
		issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
		if err != nil {
			return err
		}
		linkType, ok := nativeDependencyTypeURLForIssueType(s.ScopeURL(), issueType, types.DepParentChild)
		if !ok {
			return fmt.Errorf("%w: installed Issue Type has no parent-child Link", ErrCapabilityUnavailable)
		}
		if err := s.selectedLinkTypeInTx(ctx, tx, linkType); err != nil {
			return err
		}
		parent, err := s.requireIssueInTx(ctx, tx, request.ParentPath)
		if err != nil {
			return err
		}
		if !nativeIssueTypeMatches(s.ScopeURL(), parent.Type, issueType) {
			return fmt.Errorf("%w: parent is not an Issue of the installed relationship Type", storage.ErrValidation)
		}
		if parent.Revision != request.ExpectedParentRevision {
			return fmt.Errorf("%w: parent Issue revision changed (current %s)", ErrConflict, parent.Revision)
		}
		if issueops.ResolveInfraTypesInTx(ctx, tx)[string(create.Issue.IssueType.Normalize())] {
			return fmt.Errorf("%w: infrastructure Issue types are not admitted by the graph preview", storage.ErrValidation)
		}
		// The selected graph path can differ from the native backing ID. The
		// native writer must receive that backing ID so its counter and edge
		// refer to the same parent the graph read just checked.
		native := issueops.CloneCreateRequest(create)
		native.ParentID = parent.Properties.ID
		native.InheritLabelsFromParent = request.InheritLabels
		if err := issueops.ValidatePublicCreateRequest(native); err != nil {
			return err
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		created, _, err := issueops.ExecuteCreate(ctx, tx, native)
		if err != nil {
			return err
		}
		// The native counter allocated this ID, but another Resource may have
		// reserved its public spelling. Refusal must roll the counter, Issue,
		// and native parent edge back in this same transaction.
		if exists, err := publicResourceIDExistsInTx(ctx, tx, created.Issue.ID); err != nil {
			return err
		} else if exists {
			return ErrAlreadyExists
		}
		if !sameIssueCreateFields(create.Issue, created.Issue) || issueops.IsWisp(created.Issue) ||
			!strings.HasPrefix(created.Issue.ID, parent.Properties.ID+".") {
			return fmt.Errorf("%w: native child Issue cannot be represented exactly", storage.ErrValidation)
		}
		if err := s.afterStage("child-issue"); err != nil {
			return err
		}
		childPath, err := generatedIssuePath(ctx, tx, created.Issue.ID)
		if err != nil {
			return err
		}
		childRevision, err := freshToken()
		if err != nil {
			return err
		}
		issueType, err = NativeIssueTypeURL(s.ScopeURL(), string(created.Issue.IssueType))
		if err != nil {
			return err
		}
		if err := s.ensureNativeTypeInTx(ctx, tx, issueType); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog
			(path,resource_kind,type_url,revision,allocation_state,backing,backing_key)
			VALUES (?,'bead',?,?,'live','issue',?)`, childPath, issueType, childRevision, created.Issue.ID); err != nil {
			return err
		}
		if err := s.afterStage("child-catalog"); err != nil {
			return err
		}
		var dependencyID, dependencyType string
		if err := tx.QueryRowContext(ctx, `SELECT id,type FROM dependencies
			WHERE issue_id=? AND depends_on_issue_id=?`, created.Issue.ID, parent.Properties.ID).
			Scan(&dependencyID, &dependencyType); err != nil {
			return fmt.Errorf("%w: native child has no parent Dependency: %v", ErrInvalidStore, err)
		}
		if dependencyType != string(types.DepParentChild) {
			return fmt.Errorf("%w: native child has a non-parent Dependency", ErrInvalidStore)
		}
		linkToken, err := freshToken()
		if err != nil {
			return err
		}
		linkPath := "links/" + linkToken
		var allocated int
		allocationErr := tx.QueryRowContext(ctx, `SELECT 1 FROM graph_preview_catalog WHERE path=?`, linkPath).Scan(&allocated)
		if allocationErr == nil {
			return ErrAlreadyExists
		}
		if !errors.Is(allocationErr, sql.ErrNoRows) {
			return allocationErr
		}
		linkRevision, err := freshToken()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog
			(path,resource_kind,type_url,revision,allocation_state,backing,backing_key)
			VALUES (?,'link',?,?,'live','dependency',?)`, linkPath, linkType, linkRevision, dependencyID); err != nil {
			return err
		}
		if err := s.afterStage("child-link-catalog"); err != nil {
			return err
		}
		link, err := s.currentLinkForCreationInTx(ctx, tx, linkPath)
		if err != nil {
			return err
		}
		childURL, err := s.publicResourceURLInTx(ctx, tx, childPath)
		if err != nil {
			return err
		}
		if link.Source != childURL || link.Target != parent.ID || link.Type != linkType {
			return fmt.Errorf("%w: child Link endpoints or Type differ from native Dependency", ErrInvalidStore)
		}
		linkBytes, err := canonicalJSON(link)
		if err != nil {
			return err
		}
		if err := insertPreviewVersionInTx(ctx, tx, linkPath, linkRevision, linkBytes, create.Actor); err != nil {
			return err
		}
		if err := s.afterStage("child-link-retained"); err != nil {
			return err
		}
		// The child has no prior graph mapping whose context could be preserved.
		if err := s.recordIssueMappingContextInTx(ctx, tx, childPath, created.Issue.ID, issueRevisionContext{Inception: "create"}, request.Create.Actor); err != nil {
			return err
		}
		if err := s.afterStage("child-retained"); err != nil {
			return err
		}
		if create.Issue.Notes != "" {
			if err := checkCurrentReadBytes(ctx, tx); err != nil {
				return err
			}
		}
		child, err := s.showIssueInTx(ctx, tx, childPath)
		if err != nil {
			return err
		}
		if len(child.Owned) != 1 || !bytes.Equal(child.Owned[0], linkBytes) {
			return fmt.Errorf("%w: child has no exact owned parent Link", ErrInvalidStore)
		}
		result = IssueChildCreateResult{Child: child, Link: link}
		return nil
	})
	if err != nil {
		return IssueChildCreateResult{}, err
	}
	return result, nil
}

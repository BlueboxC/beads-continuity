package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	publicops "github.com/steveyegge/beads/issueops"
)

// IssueDeleteRequest previews or applies deletion of one durable graph Issue.
// Apply requires an observed graph revision or an explicit unconditional choice.
type IssueDeleteRequest struct {
	Path, Actor, ExpectedRevision string
	NativeID                      string
	Unconditional, Preview        bool
}

// IssueDeleteResult separates the final live Issue from graph deletion evidence.
type IssueDeleteResult struct {
	Issue           IssueRecord        `json:"issue"`
	Terminal        *BeadTerminalState `json:"terminal,omitempty"`
	Preview         bool               `json:"preview"`
	Deleted         bool               `json:"deleted"`
	IncidentLinkIDs []string           `json:"incident_link_ids,omitempty"`
}

func (s *Store) DeleteIssue(ctx context.Context, request IssueDeleteRequest) (IssueDeleteResult, error) {
	return s.deleteIssue(ctx, request, false, false)
}

// DeleteIssueWithIncidentLinks is the explicit composed storage operation.
// The CLI must select its own force/cascade policy before using it.
func (s *Store) DeleteIssueWithIncidentLinks(ctx context.Context, request IssueDeleteRequest) (IssueDeleteResult, error) {
	return s.deleteIssue(ctx, request, true, false)
}

// PreviewDeleteIssueWithIncidentLinks plans an unforced single-Issue deletion
// under one read transaction. Native dependents outside the deletion set
// refuse, including when a canonical Link would otherwise be unlinked.
func (s *Store) PreviewDeleteIssueWithIncidentLinks(ctx context.Context, request IssueDeleteRequest) (IssueDeleteResult, error) {
	request.Preview = true
	return s.deleteIssue(ctx, request, true, true)
}

func (s *Store) deleteIssue(ctx context.Context, request IssueDeleteRequest, removeIncidentLinks, enforceDependents bool) (IssueDeleteResult, error) {
	selector := IssueSelector{Path: request.Path, NativeID: request.NativeID}
	if err := validateIssueSelector(selector); err != nil {
		return IssueDeleteResult{}, fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	if !utf8.ValidString(request.Actor) {
		return IssueDeleteResult{}, fmt.Errorf("%w: actor must be UTF-8", storage.ErrValidation)
	}
	if request.ExpectedRevision != "" {
		if err := validateVersionToken(request.ExpectedRevision); err != nil {
			return IssueDeleteResult{}, err
		}
	}
	var result IssueDeleteResult
	transact := s.withTypeValidationTx
	if request.Preview {
		transact = func(ctx context.Context, fn func(*sql.Tx) error) error {
			return s.withTx(ctx, false, fn)
		}
	}
	err := transact(ctx, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		path, current, err := s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
		if err != nil {
			return err
		}
		if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, current.Revision, !request.Preview, "Issue"); err != nil {
			return err
		}
		// The native writer removes dependencies. Refuse every live graph Link
		// first, in the same transaction, so that native cleanup cannot cascade
		// through a graph endpoint. Informational and blocking Links both count.
		links, err := s.incidentLinksInTx(ctx, tx, LinksRequest{BeadPath: path, Direction: "both"}, "")
		if err != nil {
			return err
		}
		ids := make([]string, len(links))
		for i, link := range links {
			ids[i] = link.ID
		}
		if len(links) != 0 && !removeIncidentLinks {
			return fmt.Errorf("%w: %s has incident Links %s; unlink explicitly before retrying", ErrIncidentLinkConstraint, path, strings.Join(ids, ", "))
		}
		if enforceDependents {
			issueID := current.Properties.ID
			byTarget, err := issueops.ExternalDependentsBySourceInTx(ctx, tx, []string{issueID}, map[string]bool{issueID: true})
			if err != nil {
				return err
			}
			if dependents := byTarget[issueID]; len(dependents) != 0 {
				return &publicops.DependentsOutsideRequestError{IssueID: issueID, Dependents: dependents}
			}
		}
		result = IssueDeleteResult{Issue: current, Preview: request.Preview, IncidentLinkIDs: ids}
		if request.Preview {
			return nil
		}
		for _, link := range links {
			path, err := s.publicResourcePathInTx(ctx, tx, link.ID, graph.KindLink)
			if err != nil {
				return err
			}
			if _, err := s.unlinkInTx(ctx, tx, LinkDeleteRequest{
				Path: path, Actor: request.Actor, ExpectedRevision: link.Revision,
				UnconditionalSource: true,
			}); err != nil {
				return err
			}
		}
		if len(links) != 0 {
			// Unlinking a dependency can version its source Issue. The deletion
			// terminal must refer to that final live head, not the pre-plan head.
			current, err = s.showIssueInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			result.Issue = current
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		deletionRevision, err := freshToken()
		if err != nil {
			return err
		}
		ordinal, err := nextIssueHistoryOrdinalInTx(ctx, tx, path, 0)
		if err != nil {
			return err
		}
		var generation sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT participation_generation FROM issues WHERE id=?`, current.Properties.ID).Scan(&generation); err != nil {
			return err
		}
		attribution := writeAttribution(request.Actor)
		attribution.Inception = current.Attribution.Inception
		terminal := BeadTerminalState{ID: current.ID, Revision: deletionRevision, Version: deletionRevision, State: "deleted", PreviousVersion: current.Revision, Attribution: attribution}
		// The native Issue writer owns its delete journal, lease and other
		// domain cleanup. It does not mint an issue_versions row for deletion.
		unScope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unScope()
		if err := issueops.DeleteIssueInTx(ctx, tx, current.Properties.ID, request.Actor); err != nil {
			return err
		}
		if err := s.afterStage("issue-delete-native"); err != nil {
			return err
		}
		changed, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET allocation_state='deleted',revision=? WHERE path=? AND allocation_state='live' AND revision=? AND backing='issue'`, deletionRevision, path, current.Revision)
		if err != nil {
			return err
		}
		if n, err := changed.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("%w: Issue deletion lost its allocation", ErrInvalidStore)
		}
		if err := s.afterStage("issue-delete-allocation"); err != nil {
			return err
		}
		snapshot, err := canonicalJSON(terminal)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_versions(path,version,snapshot,actor,ordinal,change_at,native_participation_generation) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6),?)`, path, deletionRevision, snapshot, request.Actor, ordinal, generation); err != nil {
			return err
		}
		if err := s.afterStage("issue-delete-retained"); err != nil {
			return err
		}
		if err := s.validateDeletedIssueInTx(ctx, tx, path); err != nil {
			return err
		}
		result.Deleted = true
		result.Terminal = &terminal
		return nil
	})
	if err != nil {
		return IssueDeleteResult{}, err
	}
	return result, nil
}

func (s *Store) validDeletedIssueAllocationInTx(ctx context.Context, tx *sql.Tx, path, kind, typ, head, state, backing string, key sql.NullString) error {
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return err
	}
	if validatePath(path) != nil || kind != "bead" || !nativeIssueTypeMatches(s.ScopeURL(), typ, issueType) ||
		!authorityID.MatchString(head) || state != "deleted" || backing != "issue" || !key.Valid || key.String == "" {
		return fmt.Errorf("%w: invalid deleted Issue allocation", ErrInvalidStore)
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM issues WHERE id=?) +
	 (SELECT COUNT(*) FROM graph_preview_payloads WHERE path=?) +
	 (SELECT COUNT(*) FROM graph_preview_links WHERE source_path=? OR target_path=?) +
	 (SELECT COUNT(*) FROM dependencies WHERE issue_id=? OR depends_on_issue_id=?)`, key.String, path, path, path, key.String, key.String).Scan(&current); err != nil {
		return err
	}
	if current != 0 {
		return fmt.Errorf("%w: deleted Issue still has current backing or incident Links", ErrInvalidStore)
	}
	return nil
}

func (s *Store) validateDeletedIssueInTx(ctx context.Context, tx *sql.Tx, path string) error {
	var kind, typ, head, state, backing string
	var key sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key FROM graph_preview_catalog WHERE path=?`, path).Scan(&kind, &typ, &head, &state, &backing, &key); err != nil {
		return err
	}
	if err := s.validDeletedIssueAllocationInTx(ctx, tx, path, kind, typ, head, state, backing, key); err != nil {
		return err
	}
	terminal, _, err := s.issueTerminalInTx(ctx, tx, path, head)
	if err != nil {
		return err
	}
	if terminal != nil {
		return nil
	}
	// Legacy graph deletion retained only its final native live head.
	baseline, err := s.readIssueVersionInTx(ctx, tx, path, head, head, key.String)
	if err != nil {
		return err
	}
	if len(baseline.Owned) != 0 {
		return fmt.Errorf("%w: deleted Issue final state still owns Links", ErrInvalidStore)
	}
	return nil
}

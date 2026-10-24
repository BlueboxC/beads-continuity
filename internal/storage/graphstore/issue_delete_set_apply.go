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

// IssueDeleteSetRequest is an opt-in storage composition, not the CLI delete
// policy. Every named path is resolved and checked in the deleting transaction.
type IssueDeleteSetRequest struct {
	Paths                    []string
	Selectors                []IssueSelector
	ExpectedRevisions        map[string]string
	ExpectedSelectorRevision string
	Unconditional            bool
	Cascade                  bool
	Force                    bool
	Preview                  bool
	Actor                    string
}

type IssueDeleteSetResult struct {
	Issues          []IssueDeleteResult `json:"issues"`
	IncidentLinkIDs []string            `json:"incident_link_ids,omitempty"`
	Preview         bool                `json:"preview"`
	Deleted         bool                `json:"deleted"`
}

// DeleteIssueSetWithIncidentLinks composes bounded native deletion, canonical
// Link tombstones, Issue terminals and surviving-neighbor History in one graph
// transaction. It refuses dotted native IDs until the upstream citation
// rewrite boundary fix is integrated; a wrong rewrite is worse than refusal.
func (s *Store) DeleteIssueSetWithIncidentLinks(ctx context.Context, req IssueDeleteSetRequest) (IssueDeleteSetResult, error) {
	if !utf8.ValidString(req.Actor) {
		return IssueDeleteSetResult{}, fmt.Errorf("%w: actor must be UTF-8", storage.ErrValidation)
	}
	if len(req.Paths) != 0 && len(req.Selectors) != 0 {
		return IssueDeleteSetResult{}, fmt.Errorf("%w: delete accepts paths or public Issue selectors, not both", storage.ErrValidation)
	}
	if len(req.Selectors) != 0 && len(req.ExpectedRevisions) != 0 {
		return IssueDeleteSetResult{}, fmt.Errorf("%w: public Issue selectors require a selector revision guard", storage.ErrValidation)
	}
	if len(req.Selectors) > PreviewSnapshotLimit {
		return IssueDeleteSetResult{}, fmt.Errorf("%w: delete request exceeds %d Issue selectors", ErrLimitExceeded, PreviewSnapshotLimit)
	}
	for _, selector := range req.Selectors {
		if err := validateIssueSelector(selector); err != nil {
			return IssueDeleteSetResult{}, err
		}
	}
	if req.ExpectedSelectorRevision != "" {
		if len(req.Selectors) != 1 {
			return IssueDeleteSetResult{}, fmt.Errorf("%w: revision guard requires one public Issue selector", storage.ErrValidation)
		}
		if err := validateVersionToken(req.ExpectedSelectorRevision); err != nil {
			return IssueDeleteSetResult{}, err
		}
	}
	namedPaths := make(map[string]bool, len(req.Paths))
	for _, path := range req.Paths {
		namedPaths[path] = true
	}
	for path, revision := range req.ExpectedRevisions {
		if !namedPaths[path] {
			return IssueDeleteSetResult{}, fmt.Errorf("%w: revision guard for unnamed Issue %s", storage.ErrValidation, path)
		}
		if err := validateVersionToken(revision); err != nil {
			return IssueDeleteSetResult{}, err
		}
	}
	var result IssueDeleteSetResult
	transact := s.withTypeValidationTx
	if req.Preview {
		transact = func(ctx context.Context, fn func(*sql.Tx) error) error { return s.withTx(ctx, false, fn) }
	}
	err := transact(ctx, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		paths := req.Paths
		expected := req.ExpectedRevisions
		if len(req.Selectors) != 0 {
			paths = make([]string, 0, len(req.Selectors))
			seen := make(map[string]bool, len(req.Selectors))
			for _, selector := range req.Selectors {
				path, _, err := s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
				if err != nil {
					return err
				}
				if !seen[path] {
					seen[path] = true
					paths = append(paths, path)
				}
			}
			if req.ExpectedSelectorRevision != "" {
				expected = map[string]string{paths[0]: req.ExpectedSelectorRevision}
			}
		}
		plan, err := s.planIssueDeleteSetInTx(ctx, tx, paths, req.Cascade)
		if err != nil {
			return err
		}
		byPath := make(map[string]IssueRecord, len(plan.Issues))
		namedIDs := make([]string, 0, len(paths))
		namedSet := make(map[string]bool, len(paths))
		for _, issue := range plan.Issues {
			path, err := s.publicResourcePathInTx(ctx, tx, issue.ID, graph.KindBead)
			if err != nil {
				return err
			}
			byPath[path] = issue
			if strings.Contains(issue.Properties.ID, ".") {
				return fmt.Errorf("%w: dotted Issue ID citation rewrite is not qualified for composed deletion", ErrCapabilityUnavailable)
			}
		}
		for _, path := range paths {
			if namedSet[path] {
				continue
			}
			namedSet[path] = true
			issue := byPath[path]
			if err := checkRevisionGuard(expected[path], req.Unconditional, issue.Revision, !req.Preview, "Issue"); err != nil {
				return err
			}
			namedIDs = append(namedIDs, issue.Properties.ID)
		}
		if !req.Cascade {
			idSet := make(map[string]bool, len(namedIDs))
			for _, id := range namedIDs {
				idSet[id] = true
			}
			outside, err := issueops.ExternalDependentsBySourceInTx(ctx, tx, namedIDs, idSet)
			if err != nil {
				return err
			}
			if !req.Force {
				for _, id := range namedIDs {
					if len(outside[id]) != 0 {
						return &publicops.DependentsOutsideRequestError{IssueID: id, Dependents: outside[id]}
					}
				}
			}
		}
		result.Preview = req.Preview
		for _, issue := range plan.Issues {
			result.Issues = append(result.Issues, IssueDeleteResult{Issue: issue, Preview: req.Preview})
		}
		for _, link := range plan.Links {
			result.IncidentLinkIDs = append(result.IncidentLinkIDs, link.ID)
		}
		if req.Preview {
			return nil
		}
		neighbors, err := issueops.DeleteNeighborsInTx(ctx, tx, plan.Native.All)
		if err != nil {
			return err
		}
		beforeText := make(map[string][4]string, len(neighbors))
		for _, neighbor := range neighbors {
			beforeText[neighbor.ID] = [4]string{neighbor.Description, neighbor.Notes, neighbor.Design, neighbor.AcceptanceCriteria}
		}
		for _, link := range plan.Links {
			path, err := s.publicResourcePathInTx(ctx, tx, link.ID, graph.KindLink)
			if err != nil {
				return err
			}
			if _, err := s.unlinkInTx(ctx, tx, LinkDeleteRequest{Path: path, Actor: req.Actor, ExpectedRevision: link.Revision, UnconditionalSource: true}); err != nil {
				return err
			}
		}
		// Unlinking can advance a source Issue's live revision. Capture the
		// terminal predecessor and native generation only after all Link writes.
		type finalLive struct {
			issue      IssueRecord
			generation sql.NullInt64
		}
		final := make([]finalLive, 0, len(plan.Issues))
		for _, issue := range plan.Issues {
			path, err := s.publicResourcePathInTx(ctx, tx, issue.ID, graph.KindBead)
			if err != nil {
				return err
			}
			current, err := s.showIssueInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			var generation sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT participation_generation FROM issues WHERE id=?`, current.Properties.ID).Scan(&generation); err != nil {
				return err
			}
			final = append(final, finalLive{current, generation})
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		unScope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unScope()
		deleted, err := issueops.DeleteResolvedSetInTx(ctx, tx, plan.Native, false, req.Actor)
		if err != nil {
			return err
		}
		if deleted.DeletedCount != len(final) {
			return fmt.Errorf("%w: native deletion set changed after graph planning", ErrInvalidStore)
		}
		if err := s.afterStage("issue-delete-native"); err != nil {
			return err
		}
		for i, live := range final {
			terminal, err := s.retainIssueDeletionInTx(ctx, tx, live.issue, live.generation, req.Actor)
			if err != nil {
				return err
			}
			result.Issues[i] = IssueDeleteResult{Issue: live.issue, Terminal: &terminal, Deleted: true}
		}
		if _, err := issueops.RewriteDeletedReferencesInTx(ctx, tx, plan.Native.All, neighbors, req.Actor); err != nil {
			return err
		}
		for _, neighbor := range neighbors {
			text := [4]string{neighbor.Description, neighbor.Notes, neighbor.Design, neighbor.AcceptanceCriteria}
			if text == beforeText[neighbor.ID] {
				continue
			}
			var path string
			err := tx.QueryRowContext(ctx, `SELECT path FROM graph_preview_catalog WHERE backing='issue' AND backing_key=? AND allocation_state='live'`, neighbor.ID).Scan(&path)
			if err == sql.ErrNoRows {
				return fmt.Errorf("%w: rewritten native neighbor %s has no live graph allocation", ErrCapabilityUnavailable, neighbor.ID)
			}
			if err != nil {
				return err
			}
			if err := s.recordIssueMappingInTx(ctx, tx, path, neighbor.ID, req.Actor); err != nil {
				return err
			}
		}
		result.Deleted = true
		return nil
	})
	if err != nil {
		return IssueDeleteSetResult{}, err
	}
	return result, nil
}

func (s *Store) retainIssueDeletionInTx(ctx context.Context, tx *sql.Tx, current IssueRecord, generation sql.NullInt64, actor string) (BeadTerminalState, error) {
	path, err := s.publicResourcePathInTx(ctx, tx, current.ID, graph.KindBead)
	if err != nil {
		return BeadTerminalState{}, err
	}
	revision, err := freshToken()
	if err != nil {
		return BeadTerminalState{}, err
	}
	ordinal, err := nextIssueHistoryOrdinalInTx(ctx, tx, path, 0)
	if err != nil {
		return BeadTerminalState{}, err
	}
	attribution := writeAttribution(actor)
	attribution.Inception = current.Attribution.Inception
	terminal := BeadTerminalState{ID: current.ID, Revision: revision, Version: revision, State: "deleted", PreviousVersion: current.Revision, Attribution: attribution}
	changed, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET allocation_state='deleted',revision=? WHERE path=? AND allocation_state='live' AND revision=? AND backing='issue'`, revision, path, current.Revision)
	if err != nil {
		return BeadTerminalState{}, err
	}
	if n, err := changed.RowsAffected(); err != nil {
		return BeadTerminalState{}, err
	} else if n != 1 {
		return BeadTerminalState{}, fmt.Errorf("%w: Issue deletion lost its allocation", ErrInvalidStore)
	}
	if err := s.afterStage("issue-delete-allocation"); err != nil {
		return BeadTerminalState{}, err
	}
	snapshot, err := canonicalJSON(terminal)
	if err != nil {
		return BeadTerminalState{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_versions(path,version,snapshot,actor,ordinal,change_at,native_participation_generation) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6),?)`, path, revision, snapshot, actor, ordinal, generation); err != nil {
		return BeadTerminalState{}, err
	}
	if err := s.afterStage("issue-delete-retained"); err != nil {
		return BeadTerminalState{}, err
	}
	if err := s.validateDeletedIssueInTx(ctx, tx, path); err != nil {
		return BeadTerminalState{}, err
	}
	return terminal, nil
}

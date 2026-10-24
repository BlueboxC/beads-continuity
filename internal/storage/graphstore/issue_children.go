package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/workapi"
)

// ListChildren reads native immediate parent-child edges and returns complete
// mapped Issue records. Dotted IDs alone are not proof of parentage: the native
// dependency is authoritative. This read does not allocate a Link or change
// the existing preview's admitted dependency writer.
func (s *Store) ListChildren(ctx context.Context, parentPath string) ([]IssueRecord, error) {
	if err := validatePath(parentPath); err != nil {
		return nil, err
	}
	var children []IssueRecord
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		var err error
		children, err = s.listChildrenInTx(ctx, tx, parentPath)
		return err
	})
	if err != nil {
		return nil, err
	}
	return children, nil
}

func (s *Store) listChildrenInTx(ctx context.Context, tx *sql.Tx, parentPath string) ([]IssueRecord, error) {
	if err := checkCurrentReadBytes(ctx, tx); err != nil {
		return nil, err
	}
	if err := checkBinding(ctx, tx, s.options); err != nil {
		return nil, err
	}
	if err := s.requireRelationshipGenerationInTx(ctx, tx); err != nil {
		return nil, err
	}
	parent, err := s.requireIssueInTx(ctx, tx, parentPath)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.path
			FROM dependencies d LEFT JOIN graph_preview_catalog c
			ON c.backing='issue' AND c.backing_key=d.issue_id AND c.allocation_state='live'
			WHERE d.depends_on_issue_id=? AND d.type=? ORDER BY c.path LIMIT ?`,
		parent.Properties.ID, string(types.DepParentChild), PreviewSnapshotLimit+1)
	if err != nil {
		return nil, err
	}
	var paths []string
	for rows.Next() {
		var path sql.NullString
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !path.Valid {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: unmapped child Issue", ErrInvalidStore)
		}
		paths = append(paths, path.String)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(paths) > PreviewSnapshotLimit {
		return nil, fmt.Errorf("%w: child Issue limit exceeded", ErrLimitExceeded)
	}
	children := make([]IssueRecord, 0, len(paths))
	for _, path := range paths {
		child, err := s.showIssueInTx(ctx, tx, path)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, nil
}

// TypeGeneration zero is the direct Store API's unspecified marker, not an
// older installation. Every caller runs checkBinding first, which checks all
// installed descriptor bytes and fingerprints. Its validated set must also be
// the complete fresh generation before a relationship journey is available.
func (s *Store) requireRelationshipGenerationInTx(ctx context.Context, tx *sql.Tx) error {
	if s.options.TypeGeneration == TypeGeneration {
		return nil
	}
	if s.options.TypeGeneration != 0 {
		return fmt.Errorf("%w: parent-child relationship generation differs from the current installation", ErrInvalidStore)
	}
	if s.options.TypeGeneration == 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_types`).Scan(&count); err != nil {
			return err
		}
		if count == len(previewTypeDefinitions()) {
			return nil
		}
	}
	return fmt.Errorf("%w: parent-child relationships require the complete current Type installation", ErrCapabilityUnavailable)
}

// ListChildrenLegacy keeps bd children --json's ordinary counted Issue row
// shape while selecting membership exclusively from checked native edges.
// The edge read and row projection share a transaction so no concurrent
// writer can change the family between the two views.
func (s *Store) ListChildrenLegacy(ctx context.Context, parentPath string) ([]*types.IssueWithCounts, error) {
	return s.ListChildrenLegacySelected(ctx, IssueSelector{Path: parentPath, Exact: true})
}

// ListChildrenLegacySelected resolves public Issue identity in the same checked
// snapshot as the native parent-child edge read. Private callers stay explicit.
func (s *Store) ListChildrenLegacySelected(ctx context.Context, selector IssueSelector) ([]*types.IssueWithCounts, error) {
	if err := validateIssueSelector(selector); err != nil {
		return nil, err
	}
	children := []*types.IssueWithCounts{}
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		parentPath, _, err := s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
		if err != nil {
			return err
		}
		checked, err := s.listChildrenInTx(ctx, tx, parentPath)
		if err != nil {
			return err
		}
		if len(checked) == 0 {
			return nil
		}
		ids := make([]string, len(checked))
		pathByID := make(map[string]string, len(checked))
		for i, child := range checked {
			if child.Properties == nil || !strings.HasPrefix(child.ID, s.ScopeURL()) {
				return fmt.Errorf("%w: child escaped graph scope or has no Issue", ErrInvalidStore)
			}
			ids[i] = child.Properties.ID
			path, err := s.publicResourcePathInTx(ctx, tx, child.ID, graph.KindBead)
			if err != nil {
				return err
			}
			pathByID[child.Properties.ID] = path
		}
		rows, err := issueops.SearchIssuesWithCountsInTx(ctx, tx, "", types.IssueFilter{
			IDs: ids, SkipWisps: true, Limit: len(ids),
		})
		if err != nil {
			return err
		}
		if len(rows) != len(checked) {
			return fmt.Errorf("%w: counted children disagree with checked native edges", ErrInvalidStore)
		}
		seen := make(map[string]bool, len(rows))
		children = make([]*types.IssueWithCounts, 0, len(rows))
		for _, row := range rows {
			if row == nil || row.Issue == nil || seen[row.ID] || pathByID[row.ID] == "" {
				return fmt.Errorf("%w: invalid counted child row", ErrInvalidStore)
			}
			seen[row.ID] = true
			if row.Parent != nil {
				if err := s.validateCanonicalIssueAllocationInTx(ctx, tx, *row.Parent); err != nil {
					return err
				}
			}
			children = append(children, row)
		}
		return workapi.HydrateListComments(ctx, func() workapi.CommentStreamer { return graphIssueDetailSource{tx: tx} }, workapi.SourceRoutesCommentPlanes, children, false, true)
	})
	return children, err
}

// ListIssueDependencies reads only the installed blocking Dependency Type,
// checking the anchor as a native Issue in the same transaction as its Links.
func (s *Store) ListIssueDependencies(ctx context.Context, issuePath, direction string) ([]LinkRecord, error) {
	if err := validatePath(issuePath); err != nil {
		return nil, err
	}
	if direction != "in" && direction != "out" {
		return nil, fmt.Errorf("%w: dependency direction must be in or out", ErrCapabilityUnavailable)
	}
	var links []LinkRecord
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		issue, err := s.requireIssueInTx(ctx, tx, issuePath)
		if err != nil {
			return err
		}
		blockingType, ok := nativeDependencyTypeURLForIssueType(s.ScopeURL(), issue.Type, types.DepBlocks)
		if !ok {
			return fmt.Errorf("%w: Issue has no installed blocking Type", ErrInvalidStore)
		}
		incident, err := s.incidentLinksInTx(ctx, tx, LinksRequest{
			BeadPath: issuePath, Direction: direction, TypeURL: blockingType,
		}, "")
		if err != nil {
			return err
		}
		// One nominal Type now carries every dependency kind. Preserve the
		// familiar dep list view's blocks-only contract.
		for _, link := range incident {
			if link.Properties["dep_type"] == string(types.DepBlocks) {
				links = append(links, link)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return links, nil
}

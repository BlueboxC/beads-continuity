package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/workapi"
	publicops "github.com/steveyegge/beads/issueops"
)

// LegacyIssueListPage preserves ordinary bd list --json's Issue row shape.
// It is separate from the complete graph-record projection used by
// --format records-json.
type LegacyIssueListPage struct {
	Items   []*types.IssueWithCounts
	HasMore bool
}

// ListIssuesLegacy reads counted native Issue rows and validates their graph
// allocations in one checked snapshot. The existing graph Issue list first validates
// every selected record, including the over-fetched pagination probe.
func (s *Store) ListIssuesLegacy(ctx context.Context, request publicops.ListRequest) (LegacyIssueListPage, error) {
	includeComments := request.IncludeComments
	request.IncludeComments = false
	request, err := prepareIssueListRequest(request)
	if err != nil {
		return LegacyIssueListPage{}, err
	}
	request.IncludeComments = includeComments
	var result LegacyIssueListPage
	err = s.withTx(ctx, false, func(tx *sql.Tx) error {
		page, err := s.listIssuesInTx(ctx, tx, request)
		if err != nil {
			return err
		}
		cfg, err := issueListConfigInTx(ctx, tx)
		if err != nil {
			return err
		}
		filter, err := workapi.BuildListFilter(request, cfg)
		if err != nil {
			return err
		}
		filter.SkipCounts = false
		if filter.Limit > PreviewSnapshotLimit {
			filter.Limit = PreviewSnapshotLimit
		}
		filter = workapi.WithFetchOneExtra(workapi.WithRowsBeforeThePage(filter, request.Offset))
		rows, err := issueops.SearchIssuesWithCountsInTx(ctx, tx, "", filter)
		if err != nil {
			return err
		}
		rows, more := workapi.FinishPageAt(rows, request.SortBy, request.Reverse, request.Offset, workapi.PageLimit(request), false)
		if rows == nil {
			rows = []*types.IssueWithCounts{}
		}
		if more != page.HasMore || len(rows) != len(page.Items) {
			return fmt.Errorf("%w: counted Issue list disagrees with checked graph page", ErrInvalidStore)
		}
		for i, row := range rows {
			if row == nil || row.Issue == nil || page.Items[i].Properties == nil || row.ID != page.Items[i].Properties.ID {
				return fmt.Errorf("%w: counted Issue list disagrees with checked graph row", ErrInvalidStore)
			}
		}
		if err := workapi.HydrateListComments(ctx, func() workapi.CommentStreamer { return graphIssueDetailSource{tx: tx} }, workapi.SourceRoutesCommentPlanes, rows, request.IncludeComments, true); err != nil {
			return err
		}
		for i, row := range rows {
			canonical := page.Items[i].ID
			if !strings.HasPrefix(canonical, s.ScopeURL()) {
				return fmt.Errorf("%w: listed Issue escaped graph scope", ErrInvalidStore)
			}
			path, err := s.publicResourcePathInTx(ctx, tx, canonical, graph.KindBead)
			if err != nil {
				return err
			}
			if err := validatePath(path); err != nil {
				return fmt.Errorf("%w: invalid listed Issue path: %v", ErrInvalidStore, err)
			}
			if row.Parent != nil {
				if err := s.validateCanonicalIssueAllocationInTx(ctx, tx, *row.Parent); err != nil {
					return err
				}
			}
		}
		result = LegacyIssueListPage{Items: rows, HasMore: more}
		return nil
	})
	return result, err
}

func (s *Store) validateCanonicalIssueAllocationInTx(ctx context.Context, tx *sql.Tx, nativeID string) error {
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return err
	}
	var path, kind, typ, state string
	if err := tx.QueryRowContext(ctx, `SELECT path,resource_kind,type_url,allocation_state FROM graph_preview_catalog WHERE backing='issue' AND backing_key=?`, nativeID).Scan(&path, &kind, &typ, &state); err != nil {
		return fmt.Errorf("%w: unmapped Issue list parent: %v", ErrInvalidStore, err)
	}
	if kind != "bead" || !nativeIssueTypeMatches(s.ScopeURL(), typ, issueType) || state != "live" || validatePath(path) != nil {
		return fmt.Errorf("%w: invalid Issue list parent allocation", ErrInvalidStore)
	}
	return nil
}

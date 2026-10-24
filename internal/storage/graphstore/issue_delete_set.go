package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

// issueDeleteSetPlan is a read-only, transaction-local description of the
// complete native deletion set and its graph projection. The caller must use
// the same transaction for the eventual write; this value is not a durable
// authorization to delete after the transaction closes.
type issueDeleteSetPlan struct {
	Native issueops.DeletionSet
	Issues []IssueRecord
	Links  []LinkRecord
}

func (s *Store) planIssueDeleteSetInTx(ctx context.Context, tx *sql.Tx, paths []string, cascade bool) (issueDeleteSetPlan, error) {
	if err := checkBinding(ctx, tx, s.options); err != nil {
		return issueDeleteSetPlan{}, err
	}
	if len(paths) == 0 {
		return issueDeleteSetPlan{}, fmt.Errorf("%w: delete requires an Issue path", storage.ErrValidation)
	}
	if len(paths) > PreviewSnapshotLimit {
		return issueDeleteSetPlan{}, fmt.Errorf("%w: delete request exceeds %d Issue paths", ErrLimitExceeded, PreviewSnapshotLimit)
	}
	ids := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if err := validatePath(path); err != nil {
			return issueDeleteSetPlan{}, fmt.Errorf("%w: %v", storage.ErrValidation, err)
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		issue, err := s.requireIssueInTx(ctx, tx, path)
		if err != nil {
			return issueDeleteSetPlan{}, err
		}
		ids = append(ids, issue.Properties.ID)
	}
	set, err := issueops.ResolveDeletionSetInTx(ctx, tx, ids, cascade)
	if err != nil {
		return issueDeleteSetPlan{}, err
	}
	if len(set.WispIDs) != 0 {
		return issueDeleteSetPlan{}, fmt.Errorf("%w: deletion set includes a native wisp outside the graph Issue model", ErrCapabilityUnavailable)
	}
	if len(set.All) > PreviewSnapshotLimit {
		return issueDeleteSetPlan{}, fmt.Errorf("%w: deletion set exceeds %d graph Issues", ErrLimitExceeded, PreviewSnapshotLimit)
	}
	plan := issueDeleteSetPlan{Native: set}
	linkByID := make(map[string]LinkRecord)
	for _, id := range set.RegularIDs {
		var path string
		err := tx.QueryRowContext(ctx, `SELECT path FROM graph_preview_catalog WHERE backing='issue' AND backing_key=? AND allocation_state='live'`, id).Scan(&path)
		if err == sql.ErrNoRows {
			return issueDeleteSetPlan{}, fmt.Errorf("%w: native Issue %s has no live graph allocation", ErrCapabilityUnavailable, id)
		}
		if err != nil {
			return issueDeleteSetPlan{}, err
		}
		issue, err := s.showIssueInTx(ctx, tx, path)
		if err != nil {
			return issueDeleteSetPlan{}, err
		}
		if issue.Properties.ID != id {
			return issueDeleteSetPlan{}, fmt.Errorf("%w: Issue allocation changed during deletion plan", ErrInvalidStore)
		}
		plan.Issues = append(plan.Issues, issue)
		links, err := s.incidentLinksInTx(ctx, tx, LinksRequest{BeadPath: path, Direction: "both"}, "")
		if err != nil {
			return issueDeleteSetPlan{}, err
		}
		for _, link := range links {
			linkByID[link.ID] = link
		}
		if len(linkByID) > PreviewSnapshotLimit {
			return issueDeleteSetPlan{}, fmt.Errorf("%w: deletion set exceeds %d incident Links", ErrLimitExceeded, PreviewSnapshotLimit)
		}
	}
	sort.Slice(plan.Issues, func(i, j int) bool { return plan.Issues[i].ID < plan.Issues[j].ID })
	for _, link := range linkByID {
		plan.Links = append(plan.Links, link)
	}
	sort.Slice(plan.Links, func(i, j int) bool { return plan.Links[i].ID < plan.Links[j].ID })
	return plan, nil
}

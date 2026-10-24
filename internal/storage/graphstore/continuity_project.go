package graphstore

import (
	"context"
	"database/sql"

	"github.com/steveyegge/beads/internal/activity"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/memoryops"
)

// ProjectContinuity is the fork viewer's explicit compound read profile. Its
// graph and selected-code acquisitions each retain their 64 MiB/65,536-row
// ceilings; event iteration retains at most 1,000 validated 12 KiB envelopes.
// CurrentSnapshot's ordinary 2,048-Resource/16 MiB contract is unchanged.
type ProjectContinuity struct {
	Memories memoryops.ListResult
	Graph    Snapshot
	Code     codeindex.Index
	Activity activity.View
}

func (s *Store) ReadProjectContinuity(ctx context.Context) (ProjectContinuity, error) {
	var result ProjectContinuity
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		m := s.Memories("")
		var err error
		result.Memories, err = m.listSelectionInTx(ctx, tx, memoryops.ListRequest{ExcludeKeyPrefix: codeindex.Prefix}, activity.Prefix)
		if err != nil {
			return err
		}
		reader := &continuityReader{memories: m, tx: tx}
		reader.active.Store(true)
		defer reader.active.Store(false)
		result.Code, _, err = codeindex.ReadCurrent(ctx, reader)
		if err != nil {
			return err
		}
		result.Graph, err = s.currentSnapshotWithBoundsInTx(ctx, tx, PreviewContinuityReadRowLimit, PreviewContinuityReadByteLimit)
		if err != nil {
			return err
		}
		result.Activity, err = readObservedActivityInTx(ctx, tx, "", "", 1000, false)
		return err
	})
	if err != nil {
		return ProjectContinuity{}, err
	}
	return result, nil
}

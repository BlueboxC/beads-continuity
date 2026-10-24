package graphstore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/activity"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/memoryops"
)

// ReadPrimeContinuity selects the complete context plane, excluding observed
// session payloads that prime never renders and retaining the current index
// summary. General List keeps its exact public namespace semantics.
func (s *Store) ReadPrimeContinuity(ctx context.Context) (memoryops.ListResult, error) {
	plane, _, err := s.ReadPrimeContinuityWithActivity(ctx)
	return plane, err
}

func (s *Store) ReadPrimeContinuityWithActivity(ctx context.Context) (memoryops.ListResult, activity.View, error) {
	var plane memoryops.ListResult
	var observed activity.View
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		var err error
		plane, err = s.readPrimeContinuityInTx(ctx, tx)
		if err != nil {
			return err
		}
		observed, err = readObservedActivityInTx(ctx, tx, "", "", 3, true)
		return err
	})
	if err != nil {
		return memoryops.ListResult{}, activity.View{}, err
	}
	return plane, observed, nil
}

func (s *Store) readPrimeContinuityInTx(ctx context.Context, tx *sql.Tx) (memoryops.ListResult, error) {
	var result memoryops.ListResult
	err := func() error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		m := s.Memories("")
		req := memoryops.ListRequest{ExcludeKeyPrefix: codeindex.Prefix}
		usedRows, usedBytes, err := m.selectionUsageInTx(ctx, tx, req, false, "@activity/")
		if err != nil {
			return err
		}
		result, err = m.listSelectionInTx(ctx, tx, req, "@activity/")
		if err != nil {
			return err
		}
		count, size, err := m.selectionUsageInTx(ctx, tx, memoryops.ListRequest{KeyPrefix: codeindex.ManifestKey}, true)
		if err != nil {
			return err
		}
		if count > PreviewContinuityReadRowLimit-usedRows || size > PreviewContinuityReadByteLimit-usedBytes {
			return ErrLimitExceeded
		}
		summary, found, err := m.resolve(ctx, tx, codeindex.ManifestKey)
		if err != nil {
			return err
		}
		if found {
			result.Memories[codeindex.ManifestKey] = summary
			usedBytes += size
			usedRows += count
		} else {
			codeReq := memoryops.ListRequest{KeyPrefix: codeindex.Prefix}
			count, size, err = m.selectionUsageInTx(ctx, tx, codeReq, false)
			if err != nil {
				return err
			}
			if count > PreviewContinuityReadRowLimit-usedRows || size > PreviewContinuityReadByteLimit-usedBytes {
				return ErrLimitExceeded
			}
			code, err := m.listSelectionInTx(ctx, tx, codeReq)
			if err != nil {
				return err
			}
			for key, value := range code.Memories {
				result.Memories[key] = value
			}
			usedBytes += size
			usedRows += count
		}
		const unmanaged = `c.backing='generic' AND c.allocation_state='live' AND NOT (CASE WHEN JSON_VALID(CAST(p.metadata AS CHAR)) THEN JSON_CONTAINS_PATH(CAST(p.metadata AS CHAR),'one','$.forkContinuityKey') ELSE FALSE END)`
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),CAST(COALESCE(SUM(512+2*OCTET_LENGTH(p.properties)+2*COALESCE(OCTET_LENGTH(p.metadata),0)+OCTET_LENGTH(v.snapshot)),0) AS UNSIGNED) FROM graph_preview_catalog c JOIN graph_preview_payloads p ON p.path=c.path JOIN graph_preview_versions v ON v.path=c.path AND v.version=c.revision WHERE `+unmanaged).Scan(&count, &size); err != nil {
			return err
		}
		if count > PreviewContinuityReadRowLimit-usedRows || size > PreviewContinuityReadByteLimit-usedBytes {
			return ErrLimitExceeded
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.path FROM graph_preview_catalog c JOIN graph_preview_payloads p ON p.path=c.path WHERE `+unmanaged)
		if err != nil {
			return err
		}
		var paths []string
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				_ = rows.Close()
				return err
			}
			paths = append(paths, path)
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for _, path := range paths {
			record, err := s.showMemoryInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			if record.Properties.Body == "" {
				continue
			}
			if previous, found := result.Memories[record.ID]; found && previous != record.Properties.Body {
				return fmt.Errorf("%w: context key collides with a Memory identity", ErrInvalidStore)
			}
			result.Memories[record.ID] = record.Properties.Body
		}
		return nil
	}()
	if err != nil {
		return memoryops.ListResult{}, err
	}
	return result, nil
}

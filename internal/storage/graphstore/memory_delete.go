package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/storage"
)

// ErrIncidentLinkConstraint refuses Bead deletion while a live Link still
// names it. The caller must unlink explicitly; --force never cascades.
var ErrIncidentLinkConstraint = errors.New("deletion requires unlinking live incident Links")

// MemoryDeleteRequest is an internal disposable-preview operation. Preview is
// read-only and may omit a guard; apply requires an observed revision or explicit
// unconditional intent. Neither mode deletes Links or erases retained versions.
type MemoryDeleteRequest struct {
	Path, Actor, ExpectedRevision string
	Unconditional, Preview        bool
}

// MemoryDeleteResult keeps the actual final live snapshot separate from the
// fresh terminal deletion revision. Preview never mints either state.
type MemoryDeleteResult struct {
	Memory   Record               `json:"memory"`
	Terminal *MemoryTerminalState `json:"terminal,omitempty"`
	Preview  bool                 `json:"preview"`
	Deleted  bool                 `json:"deleted"`
}

func (s *Store) DeleteMemory(ctx context.Context, request MemoryDeleteRequest) (MemoryDeleteResult, error) {
	if err := validatePath(request.Path); err != nil {
		return MemoryDeleteResult{}, fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	if !utf8.ValidString(request.Actor) {
		return MemoryDeleteResult{}, fmt.Errorf("%w: actor must be UTF-8", storage.ErrValidation)
	}
	if request.ExpectedRevision != "" {
		if err := validateVersionToken(request.ExpectedRevision); err != nil {
			return MemoryDeleteResult{}, err
		}
	}
	var result MemoryDeleteResult
	transact := s.withTypeValidationTx
	if request.Preview {
		transact = func(ctx context.Context, fn func(*sql.Tx) error) error {
			return s.withTx(ctx, false, fn)
		}
	}
	err := transact(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = s.deleteMemoryInTx(ctx, tx, request)
		return err
	})
	if err != nil {
		return MemoryDeleteResult{}, err
	}
	return result, nil
}

func (s *Store) deleteMemoryInTx(ctx context.Context, tx *sql.Tx, request MemoryDeleteRequest) (MemoryDeleteResult, error) {
	var result MemoryDeleteResult
	err := func() error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		current, revision, err := s.beadEndpointInTx(ctx, tx, request.Path)
		if err != nil {
			return err
		}
		memory, ok := current.(Record)
		if !ok {
			return fmt.Errorf("%w: deletion supports the experimental Memory Type only", ErrCapabilityUnavailable)
		}
		if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, revision, !request.Preview, "Memory"); err != nil {
			return err
		}
		if err := s.checkLinkMappingsInTx(ctx, tx); err != nil {
			return err
		}
		// Only informational Links can have a Memory endpoint in this preview.
		// Complete mapping validation above prevents orphan rows from disappearing
		// in this existence check. Count does not load or truncate incident records.
		var incident int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_links WHERE source_path=? OR target_path=?`, request.Path, request.Path).Scan(&incident); err != nil {
			return err
		}
		if incident != 0 {
			var first string
			if err := tx.QueryRowContext(ctx, `SELECT path FROM graph_preview_links WHERE source_path=? OR target_path=? ORDER BY path LIMIT 1`, request.Path, request.Path).Scan(&first); err != nil {
				return err
			}
			return fmt.Errorf("%w: %s has %d incident Link(s), starting with %s", ErrIncidentLinkConstraint, request.Path, incident, first)
		}
		result = MemoryDeleteResult{Memory: memory, Preview: request.Preview}
		if request.Preview {
			return nil
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		deletionRevision, err := freshToken()
		if err != nil {
			return err
		}
		attribution := writeAttribution(request.Actor)
		attribution.Inception = memory.Attribution.Inception
		terminal := MemoryTerminalState{ID: memory.ID, Revision: deletionRevision, Version: deletionRevision, State: "deleted", PreviousVersion: revision, Attribution: attribution}
		changed, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET allocation_state='deleted',revision=? WHERE path=? AND allocation_state='live' AND revision=?`, deletionRevision, request.Path, revision)
		if err != nil {
			return err
		}
		if n, err := changed.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("%w: Memory deletion lost its allocation", ErrInvalidStore)
		}
		if err := s.afterStage("memory-delete-allocation"); err != nil {
			return err
		}
		removed, err := tx.ExecContext(ctx, `DELETE FROM graph_preview_payloads WHERE path=?`, request.Path)
		if err != nil {
			return err
		}
		if n, err := removed.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("%w: Memory deletion lost its payload", ErrInvalidStore)
		}
		if err := s.afterStage("memory-delete-payload"); err != nil {
			return err
		}
		snapshot, err := canonicalJSON(terminal)
		if err != nil {
			return err
		}
		if err := insertPreviewVersionInTx(ctx, tx, request.Path, deletionRevision, snapshot, request.Actor); err != nil {
			return err
		}
		if err := s.afterStage("memory-delete-retained"); err != nil {
			return err
		}
		if _, err := s.deletedMemoryInTx(ctx, tx, request.Path); err != nil {
			return err
		}
		result.Deleted = true
		if err := s.removeContinuityPointerInTx(ctx, tx, request.Path, memory); err != nil {
			return err
		}
		result.Terminal = &terminal
		return nil
	}()
	if err != nil {
		return MemoryDeleteResult{}, err
	}
	return result, nil
}

// validDeletedMemoryAllocationInTx is the VALIDATION half of deletedMemoryInTx,
// factored out so a LIST can check a deleted Memory's allocation without building
// the Record deletedMemoryInTx returns.
//
// It exists because two readers over one catalog had drifted apart: readVersionInTx
// validated a deleted subject and versionsInTx did not, so the listing reader
// accepted allocations the single-version reader refused. Sharing the predicate is
// the point -- a second copy would re-create exactly that divergence.
//
// What it does NOT cover is a CATEGORY rather than one predicate, and an earlier
// version of this comment got that wrong by naming only the owned-Links check.
// deletedMemoryInTx goes on to decode the retained snapshot, and NONE of the
// content-level checks that follow live here. See versionsInTx for where that line
// is drawn and what it means for a caller.
func (s *Store) validDeletedMemoryAllocationInTx(ctx context.Context, tx *sql.Tx, path, kind, typ, head, state, backing string, key sql.NullString) error {
	if validatePath(path) != nil || kind != "bead" || !s.isMemoryType(typ) ||
		!authorityID.MatchString(head) || state != "deleted" || backing != "generic" || key.Valid {
		return fmt.Errorf("%w: invalid deleted Memory allocation", ErrInvalidStore)
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM graph_preview_payloads WHERE path=?) + (SELECT COUNT(*) FROM graph_preview_links WHERE path=? OR source_path=? OR target_path=?) + (SELECT COUNT(*) FROM graph_preview_issue_versions WHERE path=?)`, path, path, path, path, path).Scan(&current); err != nil {
		return err
	}
	if current != 0 {
		return fmt.Errorf("%w: deleted Memory has current payload, incident Links or Issue mapping", ErrInvalidStore)
	}
	return nil
}

// deletedMemoryInTx supplies the separately retained last-current baseline.
// Ordinary current readers still return ErrGone, not this historical payload.
func (s *Store) deletedMemoryInTx(ctx context.Context, tx *sql.Tx, path string) (Record, error) {
	memory, _, err := s.deletedMemoryStateInTx(ctx, tx, path)
	return memory, err
}

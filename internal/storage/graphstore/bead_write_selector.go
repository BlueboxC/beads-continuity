package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
)

// resolveLiveBeadWriteSelectorInTx gives Link writers the same public-ID-first
// selection as mixed reads, while keeping selection and mutation in one tx.
// Only a canonical URL (localID empty) selects a graph path directly.
func (s *Store) resolveLiveBeadWriteSelectorInTx(ctx context.Context, tx *sql.Tx, candidatePath, localID string) (string, error) {
	if err := validateBeadWriteOperand(candidatePath, localID); err != nil {
		return "", err
	}
	if localID == "" {
		return candidatePath, nil
	}
	// An exact public ID owns its spelling for the Scope's lifetime. Include
	// Link and deleted allocations here so neither can fall through to a
	// different Bead found by compatibility suffix selection below.
	rows, err := tx.QueryContext(ctx, `SELECT path, resource_kind, allocation_state FROM graph_preview_catalog WHERE
		((backing='issue' AND backing_key=?) OR
		 (backing IN ('generic','dependency','informational') AND COALESCE(public_id,path)=?)) LIMIT 2`, localID, localID)
	if err != nil {
		return "", err
	}
	var exact struct{ path, kind, state string }
	found := false
	for rows.Next() {
		var selected struct{ path, kind, state string }
		if err := rows.Scan(&selected.path, &selected.kind, &selected.state); err != nil {
			_ = rows.Close()
			return "", err
		}
		if found {
			_ = rows.Close()
			return "", fmt.Errorf("%w: public ID %q has multiple graph allocations", ErrInvalidStore, localID)
		}
		exact, found = selected, true
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	if found {
		switch exact.state {
		case "deleted":
			return "", ErrGone
		case "live":
			if exact.kind != "bead" {
				if exact.kind == "link" {
					return "", fmt.Errorf("%w: public ID %q is a Link, not a Bead", storage.ErrValidation, localID)
				}
				return "", fmt.Errorf("%w: public ID %q has invalid Resource kind", ErrInvalidStore, localID)
			}
			return exact.path, nil
		default:
			return "", fmt.Errorf("%w: public ID %q has invalid allocation state", ErrInvalidStore, localID)
		}
	}
	issuePath, _, issueErr := s.resolveLiveIssueSelectorInTx(ctx, tx, IssueSelector{Path: candidatePath, NativeID: localID})
	if issueErr != nil && !errors.Is(issueErr, ErrNotFound) && !errors.Is(issueErr, ErrGone) && !errors.Is(issueErr, errNonIssueSelector) {
		return "", issueErr
	}
	var backing string
	err = tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=? AND allocation_state='live' AND (backing<>'generic' OR public_id IS NULL)`, candidatePath).Scan(&backing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if issueErr == nil && err == nil && backing == "generic" {
		return "", fmt.Errorf("%w: local shorthand %q identifies multiple Beads; use a public ID or canonical Scope URL", storage.ErrValidation, localID)
	}
	if issueErr == nil {
		return issuePath, nil
	}
	if err == nil && backing == "generic" {
		return candidatePath, nil
	}
	if errors.Is(issueErr, ErrGone) {
		return "", ErrGone
	}
	return "", ErrNotFound
}

// A local ID is opaque data; the candidate is only its tagged operand carrier.
// Requiring the exact pairing prevents a caller from smuggling a different path
// beside an ID and making validation or mutation select another Resource.
func validateBeadWriteOperand(candidatePath, localID string) error {
	if localID == "" {
		return validatePath(candidatePath)
	}
	if err := validatePublicID(localID); err != nil {
		return err
	}
	if candidatePath != "beads/"+localID {
		return fmt.Errorf("%w: local Bead ID does not match its candidate path", storage.ErrValidation)
	}
	return nil
}

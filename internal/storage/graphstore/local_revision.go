package graphstore

import (
	"context"
	"fmt"
)

// ResolveLocalRevision resolves a stable store-local per-Resource ordinal to
// its full opaque token. The checked version ledger reader supplies both acquisition
// bounds and corruption detection. It never uses the row's position as an
// ordinal, and deletion events remain selectable for guards but not content.
// Callers must still perform their normal exact-token admission for the chosen
// operation; in particular ReadVersion rejects a deletion event with ErrGone.
func (s *Store) ResolveLocalRevision(ctx context.Context, path string, ordinal int64) (VersionRow, error) {
	if ordinal <= 0 {
		return VersionRow{}, fmt.Errorf("%w: local revision must be positive", ErrVersionUnknown)
	}
	_, versions, err := s.Versions(ctx, path)
	if err != nil {
		return VersionRow{}, err
	}
	for _, row := range versions {
		if row.Ordinal == ordinal {
			return row, nil
		}
	}
	return VersionRow{}, fmt.Errorf("%w: local revision %d is unavailable for this Resource", ErrVersionUnknown, ordinal)
}

// LocalRevisionForToken returns the local display alias for an exact retained
// token. It refuses absence rather than guessing a number from another state.
func (s *Store) LocalRevisionForToken(ctx context.Context, path, token string) (VersionRow, error) {
	if err := validateVersionToken(token); err != nil {
		return VersionRow{}, err
	}
	_, versions, err := s.Versions(ctx, path)
	if err != nil {
		return VersionRow{}, err
	}
	for _, row := range versions {
		if row.Version == token {
			return row, nil
		}
	}
	return VersionRow{}, ErrVersionUnknown
}

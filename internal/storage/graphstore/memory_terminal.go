package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ResourceTerminalState is retained deletion evidence, not a live Resource.
// Links include their immutable Type; Beads omit it. Properties, metadata and
// owned Links remain in the separately acquired last-live protected baseline.
type ResourceTerminalState struct {
	ID              string      `json:"id"`
	Type            string      `json:"type,omitempty"`
	Revision        string      `json:"revision"`
	Version         string      `json:"version"`
	State           string      `json:"state"`
	PreviousVersion string      `json:"previousVersion"`
	Attribution     Attribution `json:"attribution"`
}

// Existing Go names remain aliases of the shared terminal History carrier.
type BeadTerminalState = ResourceTerminalState
type MemoryTerminalState = ResourceTerminalState

func (s *Store) decodeMemoryTerminal(ctx context.Context, tx *sql.Tx, path, version string, raw []byte, actor string) (MemoryTerminalState, error) {
	expectedID, err := s.publicResourceURLInTx(ctx, tx, path)
	if err != nil {
		return MemoryTerminalState{}, err
	}
	var terminal MemoryTerminalState
	if json.Unmarshal(raw, &terminal) != nil || !sameVersionJSON(raw, terminal) ||
		terminal.ID != expectedID || terminal.Revision != version || terminal.Version != version ||
		terminal.Type != "" || terminal.State != "deleted" || !authorityID.MatchString(version) ||
		!authorityID.MatchString(terminal.PreviousVersion) || terminal.PreviousVersion == version ||
		terminal.Attribution.HistoricalUpdate != nil || !validVersionAttribution(terminal.Attribution, actor) {
		return MemoryTerminalState{}, fmt.Errorf("%w: invalid retained Bead deletion marker", ErrInvalidStore)
	}
	return terminal, nil
}

// deletedMemoryStateInTx acquires both the terminal head and its last-current
// baseline. Legacy preview deletions whose head is still live remain readable,
// but return no terminal: that old live token can never stand in for deletion D.
func (s *Store) deletedMemoryStateInTx(ctx context.Context, tx *sql.Tx, path string) (Record, *MemoryTerminalState, error) {
	var kind, typ, revision, state, backing string
	var key sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key FROM graph_preview_catalog WHERE path=?`, path).Scan(&kind, &typ, &revision, &state, &backing, &key); err != nil {
		return Record{}, nil, fmt.Errorf("%w: missing deleted Memory allocation: %v", ErrInvalidStore, err)
	}
	if err := s.validDeletedMemoryAllocationInTx(ctx, tx, path, kind, typ, revision, state, backing, key); err != nil {
		return Record{}, nil, err
	}
	if err := checkDeletedMemoryReadBytes(ctx, tx, path, revision, ""); err != nil {
		return Record{}, nil, err
	}
	raw, actor, err := readVersionBytes(ctx, tx, path, revision, revision)
	if err != nil {
		return Record{}, nil, err
	}
	var marker struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return Record{}, nil, fmt.Errorf("%w: malformed deleted Memory head", ErrInvalidStore)
	}
	var terminal *MemoryTerminalState
	baselineRevision := revision
	if marker.State != "" {
		decoded, err := s.decodeMemoryTerminal(ctx, tx, path, revision, raw, actor)
		if err != nil {
			return Record{}, nil, err
		}
		terminal = &decoded
		baselineRevision = decoded.PreviousVersion
		raw, actor, err = readVersionBytes(ctx, tx, path, baselineRevision, baselineRevision)
		if err != nil {
			return Record{}, nil, err
		}
	}
	memory, err := s.decodeMemoryVersion(ctx, tx, path, typ, baselineRevision, raw, actor)
	if err != nil {
		return Record{}, nil, err
	}
	if len(memory.Owned) != 0 || (terminal != nil && terminal.Attribution.Inception != memory.Attribution.Inception) {
		return Record{}, nil, fmt.Errorf("%w: deleted Memory baseline differs from protected terminal facts", ErrInvalidStore)
	}
	return memory, terminal, nil
}

// VersionHistoryResult is the native authorized History carrier for current terminal
// evidence. Rows and current state are acquired in one checked snapshot. It is
// separate from ordinary Read, which continues refusing a deleted subject.
type VersionHistoryResult struct {
	PublicID     string               `json:"-"`
	Kind         ResourceKind         `json:"kind"`
	Versions     []VersionRow         `json:"versions"`
	CurrentState *MemoryTerminalState `json:"currentState,omitempty"`
}

func (s *Store) History(ctx context.Context, path string) (VersionHistoryResult, error) {
	if err := validateResourcePath(path); err != nil {
		return VersionHistoryResult{}, err
	}
	var result VersionHistoryResult
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		result.Kind, result.Versions, err = s.versionsInTx(ctx, tx, path, shippedVersionBounds())
		if err != nil {
			return err
		}
		identity, err := allocatedResourceIdentityForPathInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		result.PublicID = identity.PublicID
		if result.Kind == KindMemory || result.Kind == KindIssue || result.Kind == KindLink {
			var state string
			if err := tx.QueryRowContext(ctx, `SELECT allocation_state FROM graph_preview_catalog WHERE path=?`, path).Scan(&state); err != nil {
				return err
			}
			if state == "deleted" {
				if result.Kind == KindIssue {
					var retained int
					if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_versions v JOIN graph_preview_catalog c ON c.path=v.path AND c.revision=v.version WHERE c.path=?`, path).Scan(&retained); err != nil {
						return err
					}
					if retained == 0 {
						return nil
					} // Legacy Issue deletion retained only a live head.
				}
				result.CurrentState, err = s.resourceTerminalDescriptorInTx(ctx, tx, path, result.Versions)
				return err
			}
		}
		return nil
	})
	if err != nil {
		return VersionHistoryResult{}, err
	}
	return result, nil
}

// A History descriptor discloses D alone. It never needs to hydrate the last
// live payload (which recovery separately acquires and validates).
func (s *Store) resourceTerminalDescriptorInTx(ctx context.Context, tx *sql.Tx, path string, rows []VersionRow) (*MemoryTerminalState, error) {
	var head, typ string
	var terminal bool
	if err := tx.QueryRowContext(ctx, `SELECT c.revision,c.type_url,CASE WHEN JSON_VALID(CAST(v.snapshot AS CHAR)) THEN COALESCE(JSON_UNQUOTE(JSON_EXTRACT(CAST(v.snapshot AS CHAR),'$.state'))='deleted',FALSE) ELSE FALSE END
 FROM graph_preview_catalog c JOIN graph_preview_versions v ON v.path=c.path AND v.version=c.revision WHERE c.path=?`, path).Scan(&head, &typ, &terminal); err != nil {
		return nil, err
	}
	if !terminal {
		return nil, nil
	}
	var used uint64
	for _, row := range rows {
		used += uint64(len(row.Actor)) + 256
	}
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor) FROM graph_preview_versions WHERE path=? AND version=?`, path, head).Scan(&size); err != nil {
		return nil, err
	}
	if used > PreviewCurrentReadByteLimit || size > PreviewCurrentReadByteLimit-used {
		return nil, fmt.Errorf("%w: History metadata and terminal descriptor exceed acquisition budget", ErrLimitExceeded)
	}
	raw, actor, err := readVersionBytes(ctx, tx, path, head, head)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(path, "links/") {
		descriptor, err := s.decodeLinkTerminal(ctx, tx, path, typ, head, raw, actor)
		if err != nil {
			return nil, err
		}
		return &ResourceTerminalState{ID: descriptor.ID, Type: descriptor.Type, Revision: descriptor.Revision, Version: descriptor.Version, State: descriptor.State, PreviousVersion: descriptor.PreviousVersion, Attribution: descriptor.Attribution}, nil
	}
	descriptor, err := s.decodeMemoryTerminal(ctx, tx, path, head, raw, actor)
	if err != nil {
		return nil, err
	}
	return &descriptor, nil
}

// A deleted exact read can acquire D, its baseline and a separate selected
// snapshot. Charge those roles together before decoding any of them.
func checkDeletedMemoryReadBytes(ctx context.Context, tx *sql.Tx, path, head, source string) error {
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(d.snapshot)+OCTET_LENGTH(d.actor)+COALESCE((SELECT OCTET_LENGTH(public_id) FROM graph_preview_catalog WHERE path=d.path),0)
 +COALESCE(OCTET_LENGTH(b.snapshot)+OCTET_LENGTH(b.actor),0)
 +CASE WHEN s.version<>d.version AND s.version<>COALESCE(b.version,'') THEN OCTET_LENGTH(s.snapshot)+OCTET_LENGTH(s.actor) ELSE 0 END
 FROM graph_preview_versions d
 LEFT JOIN graph_preview_versions b ON b.path=d.path AND b.version=CASE WHEN JSON_VALID(CAST(d.snapshot AS CHAR)) THEN JSON_UNQUOTE(JSON_EXTRACT(CAST(d.snapshot AS CHAR),'$.previousVersion')) ELSE NULL END
 LEFT JOIN graph_preview_versions s ON s.path=d.path AND s.version=?
 WHERE d.path=? AND d.version=?`, source, path, head).Scan(&size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: current retained version is missing", ErrInvalidStore)
		}
		return err
	}
	if size > PreviewCurrentReadByteLimit {
		return fmt.Errorf("%w: deleted Memory terminal, baseline and selected snapshot exceed acquisition budget", ErrLimitExceeded)
	}
	return nil
}

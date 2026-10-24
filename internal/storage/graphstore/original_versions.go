package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
)

var originalHistoryDDL = []string{
	`CREATE TABLE graph_preview_conversion_ids (path VARBINARY(1024) PRIMARY KEY,original_url LONGBLOB NOT NULL,current_url LONGBLOB NOT NULL)`,
	`CREATE TABLE graph_preview_original_types (name VARBINARY(64) PRIMARY KEY,descriptor LONGBLOB NOT NULL,fingerprint VARBINARY(64) NOT NULL)`,
	`CREATE TABLE graph_preview_original_versions (path VARBINARY(1024) NOT NULL,version VARBINARY(32) NOT NULL,kind VARBINARY(16) NOT NULL,snapshot LONGBLOB NOT NULL,sha256 VARBINARY(64) NOT NULL,PRIMARY KEY(path,version))`,
	`CREATE TABLE graph_preview_conversion_origin (singleton INT PRIMARY KEY,manifest LONGBLOB NOT NULL,sha256 VARBINARY(64) NOT NULL)`,
}

// OriginalVersion keeps exact source-format bytes and their immutable catalog.
// It does not apply today's Type declarations to yesterday's evidence.
type OriginalVersion struct {
	Snapshot json.RawMessage `json:"snapshot"`
	SHA256   string          `json:"sha256"`
	Type     json.RawMessage `json:"originalType,omitempty"`
}

func (s *Store) ReadOriginalVersion(ctx context.Context, path, version string) (OriginalVersion, error) {
	if err := validateResourcePath(path); err != nil {
		return OriginalVersion{}, err
	}
	if err := validateVersionToken(version); err != nil {
		return OriginalVersion{}, err
	}
	var result OriginalVersion
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		value, _, found, err := s.originalVersionInTx(ctx, tx, path, version)
		if err != nil {
			return err
		}
		if !found {
			return ErrVersionUnknown
		}
		result = value
		return nil
	})
	if err != nil {
		return OriginalVersion{}, err
	}
	return result, nil
}

func (s *Store) originalVersionInTx(ctx context.Context, tx *sql.Tx, path, version string) (OriginalVersion, string, bool, error) {
	var size uint64
	err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(snapshot) FROM graph_preview_original_versions WHERE path=? AND version=?`, path, version).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return OriginalVersion{}, "", false, nil
	}
	if err != nil {
		return OriginalVersion{}, "", false, err
	}
	if size > PreviewCurrentReadByteLimit {
		return OriginalVersion{}, "", false, ErrLimitExceeded
	}
	var result OriginalVersion
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT snapshot,sha256,kind FROM graph_preview_original_versions WHERE path=? AND version=?`, path, version).Scan(&result.Snapshot, &result.SHA256, &kind); err != nil {
		return OriginalVersion{}, "", false, err
	}
	if conversionHash(result.Snapshot) != result.SHA256 {
		return OriginalVersion{}, "", false, ErrInvalidStore
	}
	canonical, err := graph.CanonicalizeJSON(result.Snapshot)
	if err != nil || !bytes.Equal(canonical, result.Snapshot) {
		return OriginalVersion{}, "", false, ErrInvalidStore
	}
	var header struct{ ID, Type, Version, Revision string }
	if json.Unmarshal(result.Snapshot, &header) != nil || header.Version != version || header.Revision != version || header.ID != s.ScopeURL()+path {
		return OriginalVersion{}, "", false, ErrInvalidStore
	}
	if header.Type != "" {
		result.Type, err = originalSchema8TypeInTx(ctx, tx, s.ScopeURL(), header.Type)
		if err != nil {
			return OriginalVersion{}, "", false, err
		}
	}
	return result, kind, true, nil
}

func originalSchema8TypeInTx(ctx context.Context, tx *sql.Tx, scope, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	var fingerprint string
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN OCTET_LENGTH(descriptor)<=? THEN descriptor ELSE NULL END,fingerprint FROM graph_preview_original_types WHERE name=?`, len(scope)*4+8192, originalTypeKey(id, scope)).Scan(&raw, &fingerprint)
	if err != nil {
		return nil, err
	}
	expected, err := schema8Descriptor(scope, strings.TrimPrefix(id, scope+"types/"))
	if err != nil || !bytes.Equal(raw, expected.CanonicalJSON()) || expected.Fingerprint() != fingerprint {
		return nil, ErrInvalidStore
	}
	return raw, nil
}

func originalTypeKey(id, scope string) string {
	switch id {
	case scope + "types/preview-memory-v2":
		return "memory"
	case scope + "types/preview-related-v2":
		return "related"
	case scope + "types/preview-issue-v3":
		return "issue"
	case scope + "types/preview-blocks-v2":
		return "dependency"
	case scope + "types/preview-parent-child-v1":
		return "parent-child"
	case scope + "types/preview-discovered-from-v1":
		return "discovered-from"
	case scope + "types/preview-native-related-v1":
		return "native-related"
	}
	return schema8TypeName(id, scope)
}

func decodeOriginalRecord(raw json.RawMessage, kind string) (any, error) {
	var record any
	switch kind {
	case "memory":
		record = &Record{}
	case "issue":
		record = &IssueRecord{}
	case "link":
		record = &LinkRecord{}
	case "terminal":
		return nil, ErrGone
	default:
		return nil, fmt.Errorf("%w: unknown original record kind", ErrInvalidStore)
	}
	if err := json.Unmarshal(raw, record); err != nil {
		return nil, err
	}
	canonical, err := canonicalJSON(record)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, ErrInvalidStore
	}
	switch r := record.(type) {
	case *Record:
		return *r, nil
	case *IssueRecord:
		return *r, nil
	case *LinkRecord:
		return *r, nil
	}
	return nil, ErrInvalidStore
}

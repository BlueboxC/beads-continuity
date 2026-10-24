package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// historicalLinkReadBytes bounds the distinct retained roles without decoding.
// The writer charges this result together with the current working set before
// hydration. The terminal's previousVersion is examined only in SQL.
func historicalLinkReadBytes(ctx context.Context, tx *sql.Tx, path, head, source string, deleted bool) (uint64, error) {
	var size uint64
	query := `SELECT CAST(LEAST(COALESCE(SUM(OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor)+256),0),?) AS UNSIGNED)
 FROM graph_preview_versions WHERE path=? AND version=?`
	args := []any{PreviewCurrentReadByteLimit + 1, path, source}
	if deleted {
		query = `SELECT CAST(LEAST(COALESCE(SUM(OCTET_LENGTH(v.snapshot)+OCTET_LENGTH(v.actor)+256),0),?) AS UNSIGNED)
 FROM graph_preview_versions v WHERE v.path=? AND (v.version=? OR v.version=? OR v.version=(
 SELECT CASE WHEN JSON_VALID(CAST(d.snapshot AS CHAR)) THEN JSON_UNQUOTE(JSON_EXTRACT(CAST(d.snapshot AS CHAR),'$.previousVersion')) ELSE NULL END
 FROM graph_preview_versions d WHERE d.path=? AND d.version=?))`
		args = []any{PreviewCurrentReadByteLimit + 1, path, source, head, path, head}
	}
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&size); err != nil {
		return 0, err
	}
	if size > PreviewCurrentReadByteLimit {
		return 0, fmt.Errorf("%w: retained Link inputs exceed acquisition budget", ErrLimitExceeded)
	}
	return size, nil
}

func (s *Store) decodeLinkTerminal(ctx context.Context, tx *sql.Tx, path, typ, revision string, raw []byte, actor string) (LinkTombstone, error) {
	expectedID, err := s.publicResourceURLInTx(ctx, tx, path)
	if err != nil {
		return LinkTombstone{}, err
	}
	var terminal LinkTombstone
	if json.Unmarshal(raw, &terminal) != nil || !sameVersionJSON(raw, terminal) ||
		terminal.ID != expectedID || terminal.Type != typ || terminal.Revision != revision || terminal.Version != revision ||
		terminal.State != "deleted" || !authorityID.MatchString(revision) || !authorityID.MatchString(terminal.PreviousVersion) || terminal.PreviousVersion == revision ||
		terminal.Attribution.HistoricalUpdate != nil || !validVersionAttribution(terminal.Attribution, actor) {
		return LinkTombstone{}, fmt.Errorf("%w: invalid retained Link deletion marker", ErrInvalidStore)
	}
	return terminal, nil
}

// deletedLinkStateInTx returns true terminal D plus its separately validated
// last-live protected baseline. It never substitutes the selected content source
// for that baseline, and does not require deleted endpoints to be current.
func (s *Store) deletedLinkStateInTx(ctx context.Context, tx *sql.Tx, path string) (LinkRecord, *LinkTombstone, error) {
	var kind, typ, revision, state, backing string
	var key sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key FROM graph_preview_catalog WHERE path=?`, path).Scan(&kind, &typ, &revision, &state, &backing, &key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LinkRecord{}, nil, ErrNotFound
		}
		return LinkRecord{}, nil, err
	}
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return LinkRecord{}, nil, err
	}
	if state != "deleted" || !s.validDeletedLinkAllocation(kind, typ, backing, key, issueType) || !authorityID.MatchString(revision) {
		return LinkRecord{}, nil, fmt.Errorf("%w: invalid deleted Link allocation", ErrInvalidStore)
	}
	if _, err := historicalLinkReadBytes(ctx, tx, path, revision, "", true); err != nil {
		return LinkRecord{}, nil, err
	}
	raw, actor, err := readVersionBytes(ctx, tx, path, revision, revision)
	if err != nil {
		return LinkRecord{}, nil, err
	}
	terminal, err := s.decodeLinkTerminal(ctx, tx, path, typ, revision, raw, actor)
	if err != nil {
		return LinkRecord{}, nil, err
	}
	raw, actor, err = readVersionBytes(ctx, tx, path, terminal.PreviousVersion, terminal.PreviousVersion)
	if err != nil {
		return LinkRecord{}, nil, err
	}
	var baseline LinkRecord
	if json.Unmarshal(raw, &baseline) != nil || baseline.ID != terminal.ID || baseline.Type != terminal.Type || baseline.Revision != terminal.PreviousVersion || baseline.Version != terminal.PreviousVersion {
		return LinkRecord{}, nil, fmt.Errorf("%w: invalid deleted Link protected baseline", ErrInvalidStore)
	}
	if err := s.validateVersionLink(ctx, tx, baseline, raw, actor); err != nil {
		return LinkRecord{}, nil, err
	}
	if terminal.Attribution.Inception != baseline.Attribution.Inception {
		return LinkRecord{}, nil, fmt.Errorf("%w: deleted Link baseline differs from protected inception", ErrInvalidStore)
	}
	var payloads int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM graph_preview_links WHERE path=?) + (SELECT COUNT(*) FROM graph_preview_payloads WHERE path=?)`, path, path).Scan(&payloads); err != nil {
		return LinkRecord{}, nil, err
	}
	if payloads != 0 {
		return LinkRecord{}, nil, fmt.Errorf("%w: deleted Link has current payload", ErrInvalidStore)
	}
	return baseline, &terminal, nil
}

func (s *Store) historicalLinkBaseInTx(ctx context.Context, tx *sql.Tx, path, selected string, baseline LinkRecord) (LinkRecord, error) {
	value, err := s.readVersionInTx(ctx, tx, path, selected)
	if err != nil {
		return LinkRecord{}, err
	}
	source, ok := value.(LinkRecord)
	if !ok || source.ID != baseline.ID {
		return LinkRecord{}, fmt.Errorf("%w: historical source requires the same Link identity", ErrInvalidStore)
	}
	if isConcreteNativeDependency(s.ScopeURL(), baseline.Type) && source.Type != baseline.Type {
		originalKind := map[string]string{"dependency": "blocks", "parent-child": "parent-child", "discovered-from": "discovered-from", "native-related": "related"}[originalTypeKey(source.Type, s.ScopeURL())]
		if originalKind == "" || baseline.Properties["dep_type"] != originalKind {
			return LinkRecord{}, ErrInvalidStore
		}
		source.Properties["dep_type"] = originalKind
	}
	// Type, endpoints, stored pins and ownership derive from current state (or
	// the terminal's last-live protected baseline), never from old content.
	baseline.Properties, baseline.Metadata = source.Properties, source.Metadata
	return baseline, nil
}

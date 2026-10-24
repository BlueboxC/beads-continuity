package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
)

// A graph Issue terminal occupies the graph history sequence, never a native
// issue_versions ordinal or durable Issue payload. Existing native consumers
// keep their own ordering; graph mappings and graph-only D share this counter.
func nextIssueHistoryOrdinalInTx(ctx context.Context, tx *sql.Tx, path string, minimum int64) (int64, error) {
	var previous sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(n) FROM (
 SELECT COALESCE(history_ordinal,issue_revision) AS n FROM graph_preview_issue_versions WHERE path=?
 UNION ALL SELECT ordinal AS n FROM graph_preview_versions WHERE path=?) h`, path, path).Scan(&previous); err != nil {
		return 0, err
	}
	if previous.Valid && previous.Int64 == math.MaxInt64 {
		return 0, fmt.Errorf("%w: native graph history sequence exhausted", ErrLimitExceeded)
	}
	next := previous.Int64 + 1
	if minimum > next {
		next = minimum
	}
	return next, nil
}

func (s *Store) nativeIssueVersionsInTx(ctx context.Context, tx *sql.Tx, path, key, head string, bounds versionBounds) ([]VersionRow, error) {
	var total int
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM graph_preview_issue_versions WHERE path=?)+(SELECT COUNT(*) FROM graph_preview_versions WHERE path=?),
 CAST(LEAST((SELECT COALESCE(SUM(COALESCE(OCTET_LENGTH(v.change_actor),0)+COALESCE(OCTET_LENGTH(v.attribution_status),0)+256),0) FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=?)+(SELECT COALESCE(SUM(OCTET_LENGTH(actor)+256),0) FROM graph_preview_versions WHERE path=?),?) AS UNSIGNED)`, path, path, path, path, bounds.bytes+1).Scan(&total, &size); err != nil {
		return nil, err
	}
	if total > bounds.rows || size > bounds.bytes {
		return nil, fmt.Errorf("%w: native graph history exceeds acquisition bounds", ErrLimitExceeded)
	}
	native, err := issueVersionsInTx(ctx, tx, path, key, "", bounds)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT version,ordinal,change_at,actor FROM graph_preview_versions WHERE path=? ORDER BY ordinal DESC`, path)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var row VersionRow
		if err := rows.Scan(&row.Version, &row.Ordinal, &row.ChangeAt, &row.Actor); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		row.ChangeAt = row.ChangeAt.UTC()
		row.Removed = true
		native = append(native, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	sort.Slice(native, func(i, j int) bool { return native[i].Ordinal > native[j].Ordinal })
	seen := make(map[string]bool, len(native))
	for _, row := range native {
		if seen[row.Version] {
			return nil, fmt.Errorf("%w: native history token names multiple states", ErrInvalidStore)
		}
		seen[row.Version] = true
	}
	native, err = checkedVersionOrder(native)
	if err != nil {
		return nil, err
	}
	if !listsVersion(native, head) {
		return nil, fmt.Errorf("%w: current Issue retained mapping is missing", ErrInvalidStore)
	}
	return native, nil
}

// NativeIssueAllocation reads immutable backing metadata, including for a
// deleted allocation, without disclosing or hydrating its current payload.
// Actual mutation admission and guards remain in the writing transaction.
func (s *Store) NativeIssueAllocation(ctx context.Context, path string) (bool, error) {
	if err := validatePath(path); err != nil {
		return false, err
	}
	var native bool
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var backing, kind, state string
		var typ, key sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT backing,resource_kind,allocation_state,CASE WHEN OCTET_LENGTH(type_url)<=? THEN type_url ELSE NULL END,backing_key FROM graph_preview_catalog WHERE path=?`, len(s.ScopeURL())+512, path).Scan(&backing, &kind, &state, &typ, &key)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if state != "live" && state != "deleted" {
			return ErrInvalidStore
		}
		if backing != "issue" {
			return nil
		}
		issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
		if err != nil {
			return err
		}
		if kind != "bead" || (!typ.Valid || !nativeIssueTypeMatches(s.ScopeURL(), typ.String, issueType)) || !key.Valid || key.String == "" {
			return ErrInvalidStore
		}
		native = true
		return nil
	})
	return native, err
}

// D and the last-current native baseline are independently acquired. The
// admission generation is private terminal evidence, not a historical content
// field. Its absence may still describe a legacy deletion but cannot authorize
// minting a newly participating native identity during recovery.
func (s *Store) deletedIssueStateInTx(ctx context.Context, tx *sql.Tx, path string) (IssueRecord, *BeadTerminalState, sql.NullInt64, error) {
	var kind, typ, head, state, backing string
	var key sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key FROM graph_preview_catalog WHERE path=?`, path).Scan(&kind, &typ, &head, &state, &backing, &key); err != nil {
		return IssueRecord{}, nil, sql.NullInt64{}, err
	}
	if err := s.validDeletedIssueAllocationInTx(ctx, tx, path, kind, typ, head, state, backing, key); err != nil {
		return IssueRecord{}, nil, sql.NullInt64{}, err
	}
	if err := checkDeletedIssueReadBytes(ctx, tx, path, head); err != nil {
		return IssueRecord{}, nil, sql.NullInt64{}, err
	}
	terminal, generation, err := s.issueTerminalInTx(ctx, tx, path, head)
	if err != nil {
		return IssueRecord{}, nil, sql.NullInt64{}, err
	}
	baselineRevision := head
	if terminal != nil {
		baselineRevision = terminal.PreviousVersion
	}
	baseline, err := s.readIssueVersionInTx(ctx, tx, path, baselineRevision, baselineRevision, key.String)
	if err != nil {
		return IssueRecord{}, nil, sql.NullInt64{}, err
	}
	if len(baseline.Owned) != 0 || (terminal != nil && terminal.Attribution.Inception != baseline.Attribution.Inception) {
		return IssueRecord{}, nil, sql.NullInt64{}, fmt.Errorf("%w: native deleted baseline disagrees with terminal facts", ErrInvalidStore)
	}
	return baseline, terminal, generation, nil
}

func (s *Store) issueTerminalInTx(ctx context.Context, tx *sql.Tx, path, version string) (*BeadTerminalState, sql.NullInt64, error) {
	var size uint64
	var generation sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor),native_participation_generation FROM graph_preview_versions WHERE path=? AND version=?`, path, version).Scan(&size, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, generation, nil
	}
	if err != nil {
		return nil, generation, err
	}
	var mapped int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_issue_versions WHERE path=? AND version=?`, path, version).Scan(&mapped); err != nil {
		return nil, generation, err
	}
	if mapped != 0 {
		return nil, generation, fmt.Errorf("%w: native terminal token also names a live retained mapping", ErrInvalidStore)
	}
	if size > PreviewCurrentReadByteLimit {
		return nil, generation, fmt.Errorf("%w: native terminal exceeds acquisition budget", ErrLimitExceeded)
	}
	raw, actor, err := readVersionBytes(ctx, tx, path, version, version)
	if err != nil {
		return nil, generation, err
	}
	terminal, err := s.decodeMemoryTerminal(ctx, tx, path, version, raw, actor)
	if err != nil {
		return nil, generation, err
	}
	return &terminal, generation, nil
}

func (s *Store) readNativeIssueRetainedInTx(ctx context.Context, tx *sql.Tx, path, version, head, key string) (IssueRecord, error) {
	terminal, _, err := s.issueTerminalInTx(ctx, tx, path, version)
	if err != nil {
		return IssueRecord{}, err
	}
	if terminal != nil {
		return IssueRecord{}, ErrGone
	}
	return s.readIssueVersionInTx(ctx, tx, path, version, head, key)
}

func checkDeletedIssueReadBytes(ctx context.Context, tx *sql.Tx, path, head string) error {
	var size uint64
	err := tx.QueryRowContext(ctx, `SELECT CAST(LEAST(
 COALESCE((SELECT OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor)+256 FROM graph_preview_versions WHERE path=? AND version=?),0)
 +COALESCE((SELECT SUM(OCTET_LENGTH(m.owned)+COALESCE(OCTET_LENGTH(m.revision_context),0)+OCTET_LENGTH(v.durable_state)+COALESCE(OCTET_LENGTH(v.change_actor),0)+COALESCE(OCTET_LENGTH(v.attribution_status),0)+256)
 FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision
 LEFT JOIN graph_preview_versions d ON d.path=m.path AND d.version=?
 WHERE m.path=? AND m.version=CASE WHEN d.version IS NULL THEN ? WHEN JSON_VALID(CAST(d.snapshot AS CHAR)) THEN JSON_UNQUOTE(JSON_EXTRACT(CAST(d.snapshot AS CHAR),'$.previousVersion')) ELSE NULL END),0),?) AS UNSIGNED)`, path, head, head, path, head, PreviewCurrentReadByteLimit+1).Scan(&size)
	if err != nil {
		return err
	}
	if size > PreviewCurrentReadByteLimit {
		return fmt.Errorf("%w: native terminal and protected baseline exceed acquisition budget", ErrLimitExceeded)
	}
	return nil
}

// Exact retained reads need the selected native snapshot and any current or
// selected D descriptor, never recovery's independent last-current baseline.
func checkNativeIssueExactReadBytes(ctx context.Context, tx *sql.Tx, path, head, selected string) error {
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(LEAST(
 COALESCE((SELECT SUM(OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor)+256) FROM graph_preview_versions WHERE path=? AND(version=? OR version=?)),0)
 +COALESCE((SELECT OCTET_LENGTH(m.owned)+COALESCE(OCTET_LENGTH(m.revision_context),0)+OCTET_LENGTH(v.durable_state)+COALESCE(OCTET_LENGTH(v.change_actor),0)+COALESCE(OCTET_LENGTH(v.attribution_status),0)+256 FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=? AND m.version=? AND v.removed_at IS NULL),0),?) AS UNSIGNED)`, path, head, selected, path, selected, PreviewCurrentReadByteLimit+1).Scan(&size); err != nil {
		return err
	}
	if size > PreviewCurrentReadByteLimit {
		return fmt.Errorf("%w: native terminal and selected retained state exceed acquisition budget", ErrLimitExceeded)
	}
	return nil
}

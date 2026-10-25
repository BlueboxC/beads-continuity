package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage/sqlbuild"
)

// PreviewCurrentReadByteLimit is a conservative acquisition budget for the
// entire live workspace and allocation metadata, before filtering, pagination
// or exact Resource reads. All catalog allocations (including tombstones) and
// informational rows (including corrupt orphans) are charged. It counts persisted bytes with repeated owner/Link acquisition and 256 bytes
// per acquired row. This measures read acquisition, not a schema limit or an
// exact Go heap/SQL-engine memory bound. Append-only preview
// operations also reuse it as a transaction postcondition to prevent unreadable
// growth when no inverse operation is admitted. Ordinary current reads do not acquire or charge unselected historical
// revisions. Historical Update additionally charges its selected retained input.
const PreviewCurrentReadByteLimit = 16 << 20

type currentReadSizeQuery struct {
	from, columns string
	copies        int
}

// All SQL fragments are package-owned constants or the shared Issue hydration
// projection. No table/column name comes from a request or persisted payload.
func currentReadSizeQueries() []currentReadSizeQuery {
	const live = "c.allocation_state='live'"
	return []currentReadSizeQuery{
		{"graph_preview_scope WHERE singleton=1", "workspace,scope_url,authority_id,writer_token", 1},
		{"graph_preview_types", "name,descriptor,fingerprint", 1},
		{"config WHERE `key`='issue_prefix'", "value", 1},
		// Exact reads inspect allocation metadata before refusing deleted/invalid
		// states. Include tombstones; deleted Memory final heads are charged below.
		{"graph_preview_catalog c", "c.path,c.type_url,c.revision,c.resource_kind,c.backing,c.backing_key,c.public_id", 1},
		{"graph_preview_payloads p JOIN graph_preview_catalog c ON c.path=p.path WHERE " + live, "p.properties,p.metadata", 1},
		// A live Link is read as a top-level Resource and at most once as source-owned
		// state. The owner's retained snapshot/owned JSON is charged separately.
		// Owner enumeration acquires paths before validating their allocation.
		// Count orphan rows too, so corruption cannot create an unbounded path
		// slice before the existing mapping/authority refusal.
		{"graph_preview_links l", "l.path,l.source_path,l.target_path,l.source_revision,l.target_revision,l.properties,l.metadata,l.attribution", 2},
		{"graph_preview_versions v JOIN graph_preview_catalog c ON c.path=v.path AND c.revision=v.version WHERE " + live + " AND c.resource_kind='bead'", "v.snapshot,v.actor", 1},
		{"graph_preview_versions v JOIN graph_preview_catalog c ON c.path=v.path AND c.revision=v.version WHERE " + live + " AND c.resource_kind='link'", "v.snapshot,v.actor,v.native_attribution", 2},
		// Deleted Memory current-state validation inspects the preserved final
		// live head; charge those bytes too, while older snapshots stay excluded.
		{"graph_preview_versions v JOIN graph_preview_catalog c ON c.path=v.path AND c.revision=v.version WHERE c.allocation_state='deleted' AND c.backing='generic'", "v.snapshot,v.actor", 1},
		// A real terminal head also acquires its protected last-current baseline.
		{"graph_preview_versions v JOIN graph_preview_catalog c ON c.path=v.path JOIN graph_preview_versions d ON d.path=c.path AND d.version=c.revision WHERE c.allocation_state='deleted' AND c.backing='generic' AND v.version=CASE WHEN JSON_VALID(CAST(d.snapshot AS CHAR)) THEN JSON_UNQUOTE(JSON_EXTRACT(CAST(d.snapshot AS CHAR),'$.previousVersion')) ELSE NULL END", "v.snapshot,v.actor", 1},
		// Native graph deletion keeps D separate from its native last-current
		// body. Legacy deletions use their final live mapping directly.
		{"graph_preview_versions d JOIN graph_preview_catalog c ON c.path=d.path AND c.revision=d.version WHERE c.allocation_state='deleted' AND c.backing='issue'", "d.snapshot,d.actor,d.native_participation_generation", 1},
		{"graph_preview_issue_versions m JOIN graph_preview_catalog c ON c.path=m.path LEFT JOIN graph_preview_versions d ON d.path=c.path AND d.version=c.revision JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE c.allocation_state='deleted' AND c.backing='issue' AND d.version IS NULL AND m.version=c.revision", "m.owned,m.revision_context,v.durable_state,v.change_actor,v.attribution_status", 1},
		{"issues i JOIN graph_preview_catalog c ON c.backing='issue' AND c.backing_key=i.id " + sqlbuild.LeaseJoin("i") + " WHERE " + live, sqlbuild.QualifyColumns(sqlbuild.IssueBaseColumns, "i.") + ", " + sqlbuild.LeaseSelectColumns, 1},
		{"graph_preview_issue_versions m JOIN graph_preview_catalog c ON c.path=m.path AND c.revision=m.version JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE " + live, "m.owned,m.revision_context,v.durable_state,v.change_actor,v.attribution_status", 1},
		// Full hydration reads labels twice. Comments are acquired before the existing
		// unsupported-comment refusal, so their bytes must be bounded too.
		{"labels l JOIN graph_preview_catalog c ON c.backing='issue' AND c.backing_key=l.issue_id WHERE " + live, "l.label", 2},
		{"comments m JOIN graph_preview_catalog c ON c.backing='issue' AND c.backing_key=m.issue_id WHERE " + live, "m.author,m.text", 1},
		// Dependencies appear in Issue hydration, owner state, and top-level Links.
		{"dependencies d JOIN graph_preview_catalog c ON c.backing='issue' AND c.backing_key=d.issue_id WHERE " + live, "d.id,d.issue_id,d.depends_on_issue_id,d.depends_on_wisp_id,d.depends_on_external,d.type,d.created_by,d.metadata,d.thread_id", 3},
	}
}

func checkCurrentReadBytes(ctx context.Context, tx *sql.Tx) error {
	return checkCurrentReadBytesAdditional(ctx, tx, 0)
}

// Additional retained input is charged before acquisition together with all
// current state this operation can hydrate. This is the same 16 MiB budget,
// not a separate per-input allowance.
func checkCurrentReadBytesAdditional(ctx context.Context, tx *sql.Tx, additional uint64) error {
	return checkCurrentReadBytesBudget(ctx, tx, additional, PreviewCurrentReadByteLimit)
}

func checkCurrentReadBytesBudget(ctx context.Context, tx *sql.Tx, additional, limit uint64) error {
	if additional > limit {
		return fmt.Errorf("%w: historical input exceeds acquisition budget", ErrLimitExceeded)
	}
	used := additional
	for _, part := range currentReadSizeQueries() {
		fields := []string{"256"}
		for _, column := range strings.Split(part.columns, ",") {
			fields = append(fields, "COALESCE(OCTET_LENGTH("+strings.TrimSpace(column)+"),0)")
		}
		// OCTET_LENGTH returns sizes on the SQL side; no payload column is selected
		// into Go until all these checks have succeeded in this same transaction.
		// SUM may otherwise surface as floating point. Clamp above the only
		// relevant threshold and cast in SQL so both ordinary drivers return
		// an exact integer, including when the raw sum is enormous.
		query := "SELECT CAST(LEAST(COALESCE(SUM((" + strings.Join(fields, "+") + ")*?),0),?) AS UNSIGNED) FROM " + part.from
		var size uint64
		if err := tx.QueryRowContext(ctx, query, part.copies, limit+1).Scan(&size); err != nil {
			return err
		}
		if size > limit-used {
			return fmt.Errorf("%w: complete current read exceeds the %d-byte acquisition budget before payload decoding", ErrLimitExceeded, limit)
		}
		used += size
	}
	// Generic Issue hydration routes relations through a same-ID active wisp.
	// Graph preview does not admit that alternative authority. Refuse it before
	// the hydrator can acquire unbudgeted wisp payload/labels/comments/edges.
	var wisps int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wisps w JOIN graph_preview_catalog c ON c.backing='issue' AND c.backing_key=w.id WHERE c.allocation_state='live'`).Scan(&wisps); err != nil {
		return err
	}
	if wisps != 0 {
		return fmt.Errorf("%w: live graph Issue also names a wisp", ErrInvalidStore)
	}
	return nil
}

func (s *Store) checkHistoricalMemoryReadBytes(ctx context.Context, tx *sql.Tx, path, revision string) error {
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(LEAST(COALESCE(SUM(OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor)+256),0),?) AS UNSIGNED) FROM graph_preview_versions WHERE path=? AND version=?`, s.CapacityLimits().CurrentBytes+1, path, revision).Scan(&size); err != nil {
		return err
	}
	return s.checkCurrentReadBytesAdditional(ctx, tx, size)
}

func (s *Store) checkCurrentReadBytes(ctx context.Context, tx *sql.Tx) error {
	return s.checkCurrentReadBytesAdditional(ctx, tx, 0)
}
func (s *Store) checkCurrentReadBytesAdditional(ctx context.Context, tx *sql.Tx, additional uint64) error {
	return checkCurrentReadBytesBudget(ctx, tx, additional, s.CapacityLimits().CurrentBytes)
}

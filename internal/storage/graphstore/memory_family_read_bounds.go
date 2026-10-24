package graphstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage/sqlbuild"
)

// Package-owned fragments only; all paths/IDs are bound values. These checks
// precede helper enumeration/JSON decoding, including nonselected Link Types and
// informational orphan authority rows. Native rows outside mapped incidence
// are refused by the existing integrity counts before enumeration. These checks
// bound persisted record bytes/rows, not engine scans or the separately bounded,
// repeated fixed-Type descriptor/control reads performed by existing helpers.
func (r *currentFamilyReader) chargeRows(ctx context.Context, from, columns string, copies int, args ...any) (uint64, error) {
	fields := []string{"256"}
	for _, c := range strings.Split(columns, ",") {
		fields = append(fields, "COALESCE(OCTET_LENGTH("+strings.TrimSpace(c)+"),0)")
	}
	q := "SELECT CAST(LEAST(COALESCE(SUM(" + fmt.Sprint(copies) + "*(" + strings.Join(fields, "+") + ")),0),?) AS UNSIGNED),COUNT(*) FROM " + from
	count, err := r.budget.charge(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	if count > uint64(r.request.Limits.MaxInspected-r.budget.inspected) {
		return 0, fmt.Errorf("%w: cumulative current acquisition rows", ErrLimitExceeded)
	}
	r.budget.inspected += int(count)
	return count, nil
}

func (r *currentFamilyReader) chargeInformational(ctx context.Context, predicate string, args ...any) error {
	// LEFT joins retain raw orphans for charging before existing integrity checks.
	_, err := r.chargeRows(ctx, `graph_preview_links l
 LEFT JOIN graph_preview_catalog c ON c.path=l.path
 LEFT JOIN graph_preview_versions v ON v.path=c.path AND v.version=c.revision
 LEFT JOIN graph_preview_catalog src ON src.path=l.source_path
 LEFT JOIN graph_preview_catalog dst ON dst.path=l.target_path WHERE `+predicate,
		`l.path,l.source_path,l.target_path,l.source_revision,l.target_revision,l.properties,l.metadata,l.attribution,c.path,c.type_url,c.revision,c.resource_kind,c.allocation_state,c.backing,c.backing_key,c.public_id,v.snapshot,v.actor,src.path,src.resource_kind,src.type_url,src.revision,src.allocation_state,src.backing,src.backing_key,src.public_id,dst.path,dst.resource_kind,dst.type_url,dst.revision,dst.allocation_state,dst.backing,dst.backing_key,dst.public_id`, 4, args...)
	return err
}

func (r *currentFamilyReader) chargeNative(ctx context.Context, predicate string, args ...any) error {
	_, err := r.chargeRows(ctx, `dependencies d
 LEFT JOIN graph_preview_catalog c ON c.backing='dependency' AND c.backing_key=d.id
 LEFT JOIN graph_preview_versions v ON v.path=c.path AND v.version=c.revision
 LEFT JOIN graph_preview_catalog src ON src.backing='issue' AND src.backing_key=d.issue_id
 LEFT JOIN graph_preview_catalog dst ON dst.backing='issue' AND dst.backing_key=d.depends_on_issue_id WHERE `+predicate,
		`d.id,d.issue_id,d.depends_on_issue_id,d.depends_on_wisp_id,d.depends_on_external,d.type,d.created_by,d.metadata,d.thread_id,c.path,c.type_url,c.revision,c.resource_kind,c.allocation_state,c.backing,c.backing_key,c.public_id,v.snapshot,v.actor,src.path,src.resource_kind,src.type_url,src.allocation_state,src.backing,src.backing_key,src.public_id,dst.path,dst.resource_kind,dst.type_url,dst.allocation_state,dst.backing,dst.backing_key,dst.public_id`, 4, args...)
	return err
}

func (r *currentFamilyReader) chargeIssue(ctx context.Context, path, key, version string) error {
	// Existing native relation helpers route a same-ID active wisp to alternative
	// tables. Refuse before they can acquire an unbudgeted alternative authority.
	var wisps int
	if err := r.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM wisps WHERE id=?`, key).Scan(&wisps); err != nil {
		return err
	}
	if wisps != 0 {
		return fmt.Errorf("%w: live graph Issue also names a wisp", ErrInvalidStore)
	}
	if _, err := r.chargeRows(ctx, "issues i "+sqlbuild.LeaseJoin("i")+" WHERE i.id=?", sqlbuild.QualifyColumns(sqlbuild.IssueBaseColumns, "i.")+","+sqlbuild.LeaseSelectColumns, 3, key); err != nil {
		return err
	}
	if _, err := r.chargeRows(ctx, `graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=? AND m.version=?`, "m.path,m.issue_id,m.version,m.owned,m.revision_context,v.durable_state,v.change_actor,v.attribution_status", 3, path, version); err != nil {
		return err
	}
	// GetIssueInTx and HydrateIssueOperationResult each read labels; no comments.
	if _, err := r.chargeRows(ctx, "labels l WHERE l.issue_id=?", "l.label", 2, key); err != nil {
		return err
	}
	return r.chargeNative(ctx, "d.issue_id=?", key)
}

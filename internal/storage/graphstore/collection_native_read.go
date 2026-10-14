package graphstore

import (
	"context"
	"database/sql"
	"fmt"
)

// Acquire native Links once after this transaction's complete-read guards.
// Missing rows, retained snapshots or live Issue endpoints remain errors.
func (s *Store) snapshotNativeLinksInTx(ctx context.Context, tx *sql.Tx) (map[string]LinkRecord, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.path,c.resource_kind,c.type_url,c.revision,c.backing_key,
 src.path,dst.path,d.type,d.metadata,d.thread_id,d.created_by,d.created_at,d.depends_on_wisp_id,d.depends_on_external,v.snapshot,v.actor
 FROM graph_preview_catalog c LEFT JOIN dependencies d ON d.id=c.backing_key
 LEFT JOIN graph_preview_catalog src ON src.backing='issue' AND src.backing_key=d.issue_id AND src.allocation_state='live' AND src.resource_kind='bead' AND src.type_url=?
 LEFT JOIN graph_preview_catalog dst ON dst.backing='issue' AND dst.backing_key=d.depends_on_issue_id AND dst.allocation_state='live' AND dst.resource_kind='bead' AND dst.type_url=?
 LEFT JOIN graph_preview_versions v ON v.path=c.path AND v.version=c.revision
 WHERE c.allocation_state='live' AND c.backing='dependency' LIMIT ?`, s.issueTypeURL(), s.issueTypeURL(), PreviewSnapshotLimit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := map[string]LinkRecord{}
	for rows.Next() {
		var path, kind, typ, revision string
		var key, source, target, depType, metadata, thread, actor, wisp, external, retainedActor sql.NullString
		var at sql.NullTime
		var retained []byte
		if err := rows.Scan(&path, &kind, &typ, &revision, &key, &source, &target, &depType, &metadata, &thread, &actor, &at, &wisp, &external, &retained, &retainedActor); err != nil {
			return nil, err
		}
		if len(result) >= PreviewSnapshotLimit {
			return nil, ErrLimitExceeded
		}
		if kind != "link" || !s.isNativeDependencyType(typ) || !authorityID.MatchString(revision) || validateLinkPath(path) != nil || !key.Valid || key.String == "" ||
			!source.Valid || validatePath(source.String) != nil || !target.Valid || validatePath(target.String) != nil || !depType.Valid || !metadata.Valid || !thread.Valid || !actor.Valid || !at.Valid || wisp.Valid || external.Valid || retained == nil || !retainedActor.Valid {
			return nil, fmt.Errorf("%w: incomplete native Link authority", ErrInvalidStore)
		}
		link, err := s.nativeLinkRecord(path, typ, revision, key.String, source.String, target.String, depType.String, metadata.String, thread.String, actor.String, at.Time)
		if err != nil {
			return nil, err
		}
		link, err = validateRetainedLink(link, retained, retainedActor.String)
		if err != nil {
			return nil, err
		}
		result[path] = link
	}
	return result, rows.Err()
}

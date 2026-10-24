package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/types"
)

// ExportCurrentState is an internal acquisition result, not an interchange
// envelope. Comments are keyed by canonical relative Issue path; their native
// IssueID and comment identity are preserved. Historical endpoint payloads are validation-only and are absent from this
// result, so it is not closed over its exact endpoint References.
// DeletedAllocations counts tombstones outside the live Snapshot.
type ExportCurrentState struct {
	Snapshot            Snapshot
	CommentsByIssuePath map[string][]types.Comment
	IssuePrefix         string
	DeletedAllocations  uint64
}

// Each comment is charged at least the same row overhead used by current reads.
// This explicit count ceiling bounds slice/map growth before scanning a feed;
// the aggregate byte budget will ordinarily refuse well before this ceiling.
const exportReadRowOverhead = 256
const exportCommentLimit = PreviewCurrentReadByteLimit / exportReadRowOverhead

// ExportCurrentSnapshot acquires the controlled graph's live records, installed
// Types, current durable Issue comments, exact persisted issue_prefix and count
// of deleted allocations in one rollback-only transaction. Exact endpoints
// are dereferenced for availability in that transaction; their historical
// payloads are validation-only and never enter Snapshot or the comment feed.
// Deleted Beads are validated through their terminal or legacy final head. These integrity reads
// do not add archival history to the returned inventory or establish archive
// completeness. Event journals, counters and unrelated configuration are excluded.
//
// Present wisp data or legacy KV Memories are refused: they are alternative
// live data planes not represented by this graph inventory. Orphan durable
// comments are likewise refused instead of silently disappearing in a join.
// Every failure, including transaction/connection cleanup, returns zero state.
// The caller still owns Store.Close and must check it before publishing output.
func (s *Store) ExportCurrentSnapshot(ctx context.Context) (ExportCurrentState, error) {
	var result ExportCurrentState
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = s.exportCurrentSnapshotInTx(ctx, tx)
		return err
	})
	// Some existing hydration errors wrap a driver error as text. Preserve
	// cancellation independently without changing those core read helpers.
	if ctx != nil {
		err = errors.Join(err, ctx.Err())
	}
	if err != nil {
		return ExportCurrentState{}, err
	}
	return result, nil
}

func (s *Store) exportCurrentSnapshotInTx(ctx context.Context, tx *sql.Tx) (ExportCurrentState, error) {
	// Check authority before export-specific content refusals. The shared current
	// snapshot reader checks it again in this transaction; retain that check for
	// its other callers rather than changing their admission behavior here.
	if err := checkBinding(ctx, tx, s.options); err != nil {
		return ExportCurrentState{}, err
	}
	count, err := checkExportReadBounds(ctx, tx)
	if err != nil {
		return ExportCurrentState{}, err
	}
	if err := checkExportCommentOwners(ctx, tx); err != nil {
		return ExportCurrentState{}, err
	}
	if err := checkExportPlanes(ctx, tx); err != nil {
		return ExportCurrentState{}, err
	}
	if err := s.afterStage("export-preflight"); err != nil {
		return ExportCurrentState{}, err
	}
	snapshot, err := s.currentSnapshotInTx(ctx, tx)
	if err != nil {
		return ExportCurrentState{}, err
	}
	if err := s.validateExportEndpointPinsInTx(ctx, tx, snapshot); err != nil {
		return ExportCurrentState{}, err
	}
	if err := s.afterStage("export-endpoints"); err != nil {
		return ExportCurrentState{}, err
	}
	issuePaths, err := exportSnapshotClosure(s.ScopeURL(), snapshot)
	if err != nil {
		return ExportCurrentState{}, err
	}
	if err := s.afterStage("export-inventory"); err != nil {
		return ExportCurrentState{}, err
	}
	result := ExportCurrentState{Snapshot: snapshot}
	// ReadConfigPrefix normalizes a trailing hyphen. Acquisition must preserve
	// the persisted value; currentSnapshotInTx already checked configuration.
	if err := tx.QueryRowContext(ctx, "SELECT value FROM config WHERE `key`='issue_prefix'").Scan(&result.IssuePrefix); err != nil {
		return ExportCurrentState{}, err
	}
	if !utf8.ValidString(result.IssuePrefix) {
		return ExportCurrentState{}, fmt.Errorf("%w: persisted issue_prefix is not UTF-8", ErrInvalidStore)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_preview_catalog WHERE allocation_state='deleted'").Scan(&result.DeletedAllocations); err != nil {
		return ExportCurrentState{}, err
	}
	result.CommentsByIssuePath, err = exportCommentsInTx(ctx, tx, issuePaths, count)
	if err != nil {
		return ExportCurrentState{}, err
	}
	if err := s.afterStage("export-comments"); err != nil {
		return ExportCurrentState{}, err
	}
	return result, nil
}

// Reuse the existing persisted-byte accounting policy, replacing its joined
// comment projection with the entire feed and every column we acquire. The
// export and record bytes share one budget; two separate 16 MiB checks would
// not bound their aggregate. Refused orphan comments are charged too.
func exportReadSizeQueries() []currentReadSizeQuery {
	parts := append(currentReadSizeQueries(), exportPinnedEndpointSizeQueries()...)
	for i := range parts {
		if strings.HasPrefix(parts[i].from, "comments ") {
			parts[i] = currentReadSizeQuery{"comments", "id,issue_id,author,text,created_at", 1}
		}
		if strings.Contains(parts[i].from, "c.allocation_state='deleted' AND c.backing='issue' AND d.version IS NULL AND m.version=c.revision") {
			// Shared current accounting already charges the legacy final head
			// and context decoded by collection mapping validation. Include its
			// remaining acquired metadata without charging the body twice.
			// A real native D is charged separately and does not read this head.
			parts[i].columns += ",m.issue_id,v.change_at"
		}
	}
	return parts
}

// UNION deduplicates a selected address across both endpoint roles before
// joining its one retained payload. Only addresses present in live Link rows
// are selected; no all-history payload is acquired or materialized.
func exportPinnedEndpointSizeQueries() []currentReadSizeQuery {
	const selected = `(SELECT source_path AS path,source_revision AS revision FROM graph_preview_links WHERE source_revision<>''
 UNION SELECT target_path,target_revision FROM graph_preview_links WHERE target_revision<>'') p`
	return []currentReadSizeQuery{
		{selected + " JOIN graph_preview_versions v ON v.path=p.path AND v.version=p.revision", "v.snapshot,v.actor", 1},
		{selected + " JOIN graph_preview_issue_versions m ON m.path=p.path AND m.version=p.revision JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision", "m.issue_id,m.owned,m.revision_context,v.durable_state,v.change_actor,v.attribution_status,v.change_at", 1},
	}
}

func checkExportReadBounds(ctx context.Context, tx *sql.Tx) (uint64, error) {
	return checkExportReadBoundsAtLimit(ctx, tx, exportCommentLimit, PreviewCurrentReadByteLimit)
}

// Smaller package-private limits let engine tests exercise admission boundaries
// without creating tens of thousands of rows. Production uses fixed limits.
func checkExportReadBoundsAtLimit(ctx context.Context, tx *sql.Tx, commentLimit, byteLimit uint64) (uint64, error) {
	var count uint64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM comments").Scan(&count); err != nil {
		return 0, err
	}
	if count > commentLimit {
		return 0, fmt.Errorf("%w: current export exceeds %d comments before feed acquisition", ErrLimitExceeded, commentLimit)
	}
	var used uint64
	for _, part := range exportReadSizeQueries() {
		fields := []string{fmt.Sprint(exportReadRowOverhead)}
		for _, column := range strings.Split(part.columns, ",") {
			fields = append(fields, "COALESCE(OCTET_LENGTH("+strings.TrimSpace(column)+"),0)")
		}
		// Fragments come only from package-owned projections. SQL clamps and
		// casts the sum before Go sees it, including corrupt oversized rows.
		query := "SELECT CAST(LEAST(COALESCE(SUM((" + strings.Join(fields, "+") + ")*?),0),?) AS UNSIGNED) FROM " + part.from
		var size uint64
		if err := tx.QueryRowContext(ctx, query, part.copies, byteLimit+1).Scan(&size); err != nil {
			return 0, err
		}
		if size > byteLimit-used {
			return 0, fmt.Errorf("%w: current export exceeds the %d-byte acquisition budget before payload decoding", ErrLimitExceeded, byteLimit)
		}
		used += size
	}
	return count, nil
}

func checkExportCommentOwners(ctx context.Context, tx *sql.Tx) error {
	var orphan bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM comments m LEFT JOIN graph_preview_catalog c
 ON c.backing='issue' AND c.backing_key=m.issue_id
 WHERE c.path IS NULL OR c.resource_kind<>'bead' OR c.allocation_state<>'live')`).Scan(&orphan)
	if err != nil {
		return err
	}
	if orphan {
		return fmt.Errorf("%w: current export contains a comment without a live graph Issue", ErrInvalidStore)
	}
	return nil
}

func checkExportPlanes(ctx context.Context, tx *sql.Tx) error {
	// COUNT/EXISTS only; refused planes never allocate their payloads. Events
	// and child counters (including historical wisp ones) are explicitly out
	// of scope. Current wisp payload/relations/comments/labels are not History.
	// With intact foreign keys, wisps refuses before wisp_dependencies can be
	// reached; that separate probe defends against FK-bypassed corruption.
	for _, plane := range []struct{ name, query string }{
		{"wisps", "SELECT EXISTS(SELECT 1 FROM wisps)"},
		{"wisp_dependencies", "SELECT EXISTS(SELECT 1 FROM wisp_dependencies)"},
		{"wisp_comments", "SELECT EXISTS(SELECT 1 FROM wisp_comments)"},
		{"wisp_labels", "SELECT EXISTS(SELECT 1 FROM wisp_labels)"},
		{"legacy KV Memories", "SELECT EXISTS(SELECT 1 FROM config WHERE LEFT(`key`,10)='kv.memory.')"},
	} {
		var present bool
		if err := tx.QueryRowContext(ctx, plane.query).Scan(&present); err != nil {
			return err
		}
		if present {
			return fmt.Errorf("%w: current export does not acquire %s", ErrInvalidStore, plane.name)
		}
	}
	return nil
}

func exportCommentsInTx(ctx context.Context, tx *sql.Tx, issuePaths map[string]string, count uint64) (map[string][]types.Comment, error) {
	result := make(map[string][]types.Comment, len(issuePaths))
	for _, path := range issuePaths {
		result[path] = []types.Comment{}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,issue_id,author,text,created_at FROM comments
 ORDER BY issue_id,created_at,id LIMIT ?`, count+1)
	if err != nil {
		return nil, err
	}
	var acquired uint64
	for rows.Next() {
		var comment types.Comment
		if err := rows.Scan(&comment.ID, &comment.IssueID, &comment.Author, &comment.Text, &comment.CreatedAt); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		// Graph comment authoring admits UTF-8. Refuse corrupt byte strings
		// before a later JSON encoder can silently replace their original bytes.
		if err := validateExportComment(comment); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		path, ok := issuePaths[comment.IssueID]
		if !ok {
			return nil, errors.Join(fmt.Errorf("%w: comment owner missing from graph inventory", ErrInvalidStore), rows.Close())
		}
		acquired++
		if acquired > count {
			return nil, errors.Join(fmt.Errorf("%w: comment feed differs from its bounded inventory", ErrInvalidStore), rows.Close())
		}
		result[path] = append(result[path], comment)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if acquired != count {
		return nil, fmt.Errorf("%w: incomplete comment feed", ErrInvalidStore)
	}
	return result, nil
}

func validateExportComment(comment types.Comment) error {
	if !utf8.ValidString(comment.ID) || !utf8.ValidString(comment.IssueID) || !utf8.ValidString(comment.Author) || !utf8.ValidString(comment.Text) {
		return fmt.Errorf("%w: current comment is not UTF-8", ErrInvalidStore)
	}
	return nil
}

// Validate selected exact addresses under the acquisition transaction. The shared
// current reader preserves References without resolving their historical payloads.
// The export preflight charges each distinct pinned acquisition before decoding.
// Retain only its selected Type for later source ownership checks.
func (s *Store) validateExportEndpointPinsInTx(ctx context.Context, tx *sql.Tx, snapshot Snapshot) error {
	seen := make(map[EndpointReference]string)
	ownedByHistoricalType := make(map[string][]string)
	for _, record := range snapshot.Records {
		link, ok := record.(LinkRecord)
		if !ok {
			continue
		}
		for _, ref := range []EndpointReference{link.SourceReference(), link.TargetReference()} {
			if ref.Revision == "" || seen[ref] != "" {
				continue
			}
			path, err := s.publicResourcePathInTx(ctx, tx, ref.URI, graph.KindBead)
			if err != nil {
				return err
			}
			if validateVersionToken(ref.Revision) != nil {
				return fmt.Errorf("%w: invalid exact export endpoint Reference", ErrInvalidStore)
			}
			// This shared availability check rejects removed native Issue editions
			// before the exact reader can hydrate their still-retained bytes.
			if err := s.checkRetainedSubjectReference(ctx, tx, path, ref.Revision); err != nil {
				return err
			}
			value, err := s.readVersionInTx(ctx, tx, path, ref.Revision)
			if err != nil {
				return err
			}
			var id, revision string
			switch endpoint := value.(type) {
			case Record:
				id, revision = endpoint.ID, endpoint.Revision
			case IssueRecord:
				id, revision = endpoint.ID, endpoint.Revision
			default:
				return fmt.Errorf("%w: exact export endpoint must select a Bead", ErrInvalidStore)
			}
			if id != ref.URI || revision != ref.Revision {
				return fmt.Errorf("%w: exact export endpoint differs from selected address", ErrInvalidStore)
			}
			seen[ref] = recordType(value)
		}
		if source := seen[link.SourceReference()]; link.SourceRevision != "" {
			if _, known := ownedByHistoricalType[source]; !known {
				ownedTypes, err := s.SelectedOwnedLinkTypes(source)
				if err != nil {
					return err
				}
				ownedByHistoricalType[source] = ownedTypes
			}
			if slices.Contains(ownedByHistoricalType[source], link.Type) {
				return fmt.Errorf("%w: pinned owned source observation point is not selected", ErrCapabilityUnavailable)
			}
		}
	}
	return nil
}

// This pure check establishes current identity/owned membership, not retained
// endpoint closure. Complete Link serialization preserves each endpoint's pin;
// URI map keys below establish identity membership only.
func exportSnapshotClosure(scope string, snapshot Snapshot) (map[string]string, error) {
	typesByID := make(map[string]graph.TypeDescriptor, len(snapshot.Types))
	for _, descriptor := range snapshot.Types {
		if _, exists := typesByID[descriptor.ID()]; exists || descriptor.IsZero() {
			return nil, fmt.Errorf("%w: duplicate or missing acquired Type", ErrInvalidStore)
		}
		typesByID[descriptor.ID()] = descriptor
	}
	type bead struct {
		typ   string
		owned []json.RawMessage
	}
	beads := make(map[string]bead)
	links := make(map[string]LinkRecord)
	ids := make(map[string]bool, len(snapshot.Records))
	issuePaths := make(map[string]string)
	for _, record := range snapshot.Records {
		var id, typ string
		var kind graph.ResourceKind
		switch record := record.(type) {
		case Record:
			id, typ, kind = record.ID, record.Type, graph.KindBead
			beads[id] = bead{typ, record.Owned}
		case IssueRecord:
			id, typ, kind = record.ID, record.Type, graph.KindBead
			if record.Properties == nil || record.Properties.ID == "" {
				return nil, fmt.Errorf("%w: acquired Issue identity missing", ErrInvalidStore)
			}
			if _, exists := issuePaths[record.Properties.ID]; exists {
				return nil, fmt.Errorf("%w: duplicate acquired native Issue identity", ErrInvalidStore)
			}
			issuePaths[record.Properties.ID] = strings.TrimPrefix(id, scope)
			beads[id] = bead{typ, record.Owned}
		case LinkRecord:
			id, typ, kind = record.ID, record.Type, graph.KindLink
			links[id] = record
		default:
			return nil, fmt.Errorf("%w: unknown acquired record", ErrInvalidStore)
		}
		_, identityErr := graph.ScopeLocalPublicResourceID(scope, id)
		if ids[id] || identityErr != nil {
			return nil, fmt.Errorf("%w: duplicate or noncanonical acquired identity", ErrInvalidStore)
		}
		ids[id] = true
		descriptor, ok := typesByID[typ]
		if !ok || descriptor.Describes() != kind {
			return nil, fmt.Errorf("%w: acquired record Type is missing or has the wrong kind", ErrInvalidStore)
		}
		if kind == graph.KindBead {
			if _, present := snapshot.OwnedLinkTypes[typ]; !present {
				return nil, fmt.Errorf("%w: acquired Bead ownership metadata missing", ErrInvalidStore)
			}
		}
	}
	expected := make(map[string]map[string][]byte)
	for id, link := range links {
		source, sourceOK := beads[link.Source]
		if _, targetOK := beads[link.Target]; !sourceOK || !targetOK {
			return nil, fmt.Errorf("%w: acquired Link endpoint missing", ErrInvalidStore)
		}
		if slices.Contains(snapshot.OwnedLinkTypes[source.typ], link.Type) {
			if link.SourceReference().Revision != "" {
				return nil, fmt.Errorf("%w: pinned owned source observation point is not selected", ErrCapabilityUnavailable)
			}
			raw, err := canonicalJSON(link)
			if err != nil {
				return nil, err
			}
			if expected[link.Source] == nil {
				expected[link.Source] = make(map[string][]byte)
			}
			expected[link.Source][id] = raw
		}
	}
	for id, owner := range beads {
		want := expected[id]
		if len(owner.owned) != len(want) {
			return nil, fmt.Errorf("%w: incomplete acquired owned Link set", ErrInvalidStore)
		}
		seen := make(map[string]bool, len(owner.owned))
		for _, raw := range owner.owned {
			var identity struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &identity) != nil || seen[identity.ID] || !bytes.Equal(want[identity.ID], raw) {
				return nil, fmt.Errorf("%w: acquired owned Link differs from top-level Link", ErrInvalidStore)
			}
			seen[identity.ID] = true
		}
	}
	return issuePaths, nil
}

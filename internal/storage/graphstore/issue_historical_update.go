package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
	publicops "github.com/steveyegge/beads/issueops"
)

// These are the existing graph UpdateIssue setters, not the complete native
// Issue JSON shape. Unmapped editable members must agree with current state;
// protected initialization and service/lifecycle facts always remain current.
var historicalIssueEditableMembers = map[string]bool{
	"title": true, "description": true, "design": true, "acceptance_criteria": true,
	"issue_type": true, "priority": true, "estimated_minutes": true, "assignee": true, "notes": true,
	"external_ref": true, "spec_id": true, "due_at": true,
}
var historicalIssueCurrentMembers = map[string]bool{
	"id": true, "created_at": true, "created_by": true, "updated_at": true,
	"started_at": true, "closed_at": true, "close_reason": true, "closed_by_session": true,
	"lease_expires_at": true, "heartbeat_at": true, "lease_granted_node": true,
	"is_blocked": true, "source_system": true, "storage_class": true,
	"labels": true, "parent": true, "parent_id": true, "dependencies": true,
	"compaction_level": true, "compacted_at": true, "compacted_at_commit": true, "original_size": true,
	"source_formula": true, "source_location": true,
}

func historicalIssueField[T any](source T, edit publicops.Field[T]) publicops.Field[T] {
	if edit.Set {
		return edit
	}
	return publicops.Field[T]{Set: true, Value: source}
}

func composeHistoricalIssuePatch(current, source IssueRecord, edit publicops.IssuePatch) (publicops.IssuePatch, error) {
	if current.Properties == nil || source.Properties == nil || current.ID != source.ID || !sameNativeIssueTypeFamily(current.Type, source.Type) {
		return publicops.IssuePatch{}, fmt.Errorf("%w: historical Issue source requires the same native subject", ErrCapabilityUnavailable)
	}
	objects := make([]map[string]json.RawMessage, 2)
	for i, issue := range []*publicops.Issue{current.Properties, source.Properties} {
		raw, err := canonicalJSON(issue)
		if err != nil {
			return publicops.IssuePatch{}, err
		}
		if err := json.Unmarshal(raw, &objects[i]); err != nil {
			return publicops.IssuePatch{}, err
		}
	}
	for _, name := range comparisonKeys(objects[0], objects[1]) {
		if historicalIssueEditableMembers[name] || historicalIssueCurrentMembers[name] {
			continue
		}
		a, haveA := objects[0][name]
		b, haveB := objects[1][name]
		equal, err := comparisonJSONEqual(a, b)
		if haveA != haveB || err != nil || !equal {
			return publicops.IssuePatch{}, fmt.Errorf("%w: historical Issue member %s requires its native domain mapping", ErrCapabilityUnavailable, name)
		}
	}
	s := source.Properties
	patch := publicops.IssuePatch{
		Title: historicalIssueField(s.Title, edit.Title), Description: historicalIssueField(s.Description, edit.Description),
		Design: historicalIssueField(s.Design, edit.Design), AcceptanceCriteria: historicalIssueField(s.AcceptanceCriteria, edit.AcceptanceCriteria),
		IssueType: historicalIssueField(s.IssueType, edit.IssueType), Priority: historicalIssueField(s.Priority, edit.Priority), EstimatedMinutes: historicalIssueField(s.EstimatedMinutes, edit.EstimatedMinutes),
		Assignee: historicalIssueField(s.Assignee, edit.Assignee), Notes: historicalIssueField(s.Notes, edit.Notes),
		ExternalRef: historicalIssueField(s.ExternalRef, edit.ExternalRef), SpecID: historicalIssueField(s.SpecID, edit.SpecID), DueAt: historicalIssueField(s.DueAt, edit.DueAt),
	}
	if edit.AppendNotes.Set {
		if s.Notes == current.Properties.Notes {
			// Preserve ordinary native append intent and its overwrite fence.
			patch.Notes = publicops.Field[string]{}
			patch.AppendNotes = edit.AppendNotes
		} else {
			resolved, err := issueops.ResolveMergeOps(s, issueops.UpdateFields(publicops.IssuePatch{AppendNotes: edit.AppendNotes}))
			if err != nil {
				return publicops.IssuePatch{}, err
			}
			notes, ok := resolved["notes"].(string)
			if !ok {
				return publicops.IssuePatch{}, fmt.Errorf("%w: native notes composition lost its value", ErrInvalidStore)
			}
			patch.Notes = publicops.Field[string]{Set: true, Value: notes}
		}
	}
	metadata, _, err := issueops.ApplyMetadataPatch(source.Metadata, edit.Metadata)
	if err != nil {
		return publicops.IssuePatch{}, err
	}
	metadata, err = commonMetadata(metadata)
	if err != nil {
		return publicops.IssuePatch{}, err
	}
	patch.Metadata = publicops.MetadataPatch{Replace: publicops.Field[json.RawMessage]{Set: true, Value: metadata}}
	return patch, nil
}

func (s *Store) historicalIssuePatchInTx(ctx context.Context, tx *sql.Tx, path, sourceRevision string, current IssueRecord, edit publicops.IssuePatch, propertiesPatch []byte, recovering bool) (publicops.IssuePatch, error) {
	source := current
	if !recovering || sourceRevision != current.Revision {
		var err error
		original, kind, found, readErr := s.originalVersionInTx(ctx, tx, path, sourceRevision)
		if readErr != nil {
			return publicops.IssuePatch{}, readErr
		}
		if found {
			value, readErr := decodeOriginalRecord(original.Snapshot, kind)
			if readErr != nil {
				return publicops.IssuePatch{}, readErr
			}
			var ok bool
			source, ok = value.(IssueRecord)
			if !ok || source.Properties == nil || source.Properties.ID != current.Properties.ID {
				return publicops.IssuePatch{}, ErrInvalidStore
			}
			// Only the composition view crosses the explicit conversion mapping.
			// Original retained bytes and their public identity stay untouched.
			source.ID = current.ID
			source.Type, err = NativeIssueTypeURL(s.ScopeURL(), string(source.Properties.IssueType))
		} else {
			source, err = s.readNativeIssueRetainedInTx(ctx, tx, path, sourceRevision, current.Revision, current.Properties.ID)
		}
		if err != nil {
			return publicops.IssuePatch{}, err
		}
	}
	if len(propertiesPatch) > 0 {
		var err error
		edit, err = historicalIssuePropertiesPatch(source.Properties, propertiesPatch, edit.Metadata)
		if err != nil {
			return publicops.IssuePatch{}, err
		}
	}
	return composeHistoricalIssuePatch(current, source, edit)
}

func (s *Store) checkHistoricalIssueReadBytes(ctx context.Context, tx *sql.Tx, path, revision string) error {
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(LEAST(
 COALESCE((SELECT SUM(OCTET_LENGTH(m.owned)+COALESCE(OCTET_LENGTH(m.revision_context),0)+OCTET_LENGTH(v.durable_state)+COALESCE(OCTET_LENGTH(v.change_actor),0)+COALESCE(OCTET_LENGTH(v.attribution_status),0)+256)
 FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision
 JOIN graph_preview_catalog c ON c.path=m.path LEFT JOIN graph_preview_versions d ON d.path=c.path AND d.version=c.revision
 WHERE m.path=? AND m.version=? AND v.removed_at IS NULL AND NOT(c.allocation_state='deleted' AND JSON_VALID(CAST(d.snapshot AS CHAR)) AND m.version=JSON_UNQUOTE(JSON_EXTRACT(CAST(d.snapshot AS CHAR),'$.previousVersion')))),0)
 +COALESCE((SELECT SUM(OCTET_LENGTH(m.owned)+COALESCE(OCTET_LENGTH(m.revision_context),0)+OCTET_LENGTH(v.durable_state)+COALESCE(OCTET_LENGTH(v.change_actor),0)+COALESCE(OCTET_LENGTH(v.attribution_status),0)+256)
 FROM graph_preview_catalog c JOIN graph_preview_versions d ON d.path=c.path AND d.version=c.revision JOIN graph_preview_issue_versions m ON m.path=c.path AND m.version=CASE WHEN JSON_VALID(CAST(d.snapshot AS CHAR)) THEN JSON_UNQUOTE(JSON_EXTRACT(CAST(d.snapshot AS CHAR),'$.previousVersion')) ELSE NULL END JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE c.path=? AND c.allocation_state='deleted' AND c.backing='issue'),0)
 +COALESCE((SELECT OCTET_LENGTH(v.snapshot)+OCTET_LENGTH(v.actor)+256 FROM graph_preview_versions v JOIN graph_preview_catalog c ON c.path=v.path WHERE v.path=? AND v.version=? AND NOT(c.allocation_state='deleted' AND c.revision=v.version)),0),?) AS UNSIGNED)`, path, revision, path, path, revision, s.CapacityLimits().CurrentBytes+1).Scan(&size); err != nil {
		return err
	}
	return s.checkCurrentReadBytesAdditional(ctx, tx, size)
}

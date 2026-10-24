package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage/issueops"
)

type conversionAllocation struct {
	path, kind, backing, typ, head, state string
	key                                   sql.NullString
}

func walkConversionCatalog(ctx context.Context, tx *sql.Tx, visit func(conversionAllocation) error) error {
	last := ""
	for {
		rows, err := tx.QueryContext(ctx, `SELECT path,resource_kind,backing,type_url,revision,allocation_state,backing_key FROM graph_preview_catalog WHERE path>? ORDER BY path LIMIT 128`, last)
		if err != nil {
			return err
		}
		var page []conversionAllocation
		for rows.Next() {
			var v conversionAllocation
			if err := rows.Scan(&v.path, &v.kind, &v.backing, &v.typ, &v.head, &v.state, &v.key); err != nil {
				_ = rows.Close()
				return err
			}
			page = append(page, v)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, v := range page {
			if err := visit(v); err != nil {
				return fmt.Errorf("convert %s: %w", v.path, err)
			}
		}
		last = page[len(page)-1].path
	}
}

func (s *Store) reprojectSchema8InTx(ctx context.Context, tx *sql.Tx, actor string) error {

	// Establish all native Types before any endpoint or owned-set acquisition.
	if err := walkConversionCatalog(ctx, tx, func(v conversionAllocation) error {
		typ := v.typ
		switch v.backing {
		case "generic":
			if typ != s.ScopeURL()+"types/preview-memory-v2" {
				return ErrInvalidStore
			}
			typ = MemoryTypeURL(s.ScopeURL())
		case "issue":
			var classification string
			err := tx.QueryRowContext(ctx, `SELECT issue_type FROM issues WHERE id=?`, v.key.String).Scan(&classification)
			if errors.Is(err, sql.ErrNoRows) && v.state == "deleted" {
				var raw []byte
				err = tx.QueryRowContext(ctx, `SELECT v.durable_state FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=? AND m.version=?`, v.path, v.head).Scan(&raw)
				if err == nil {
					var fields struct {
						IssueType string `json:"issue_type"`
					}
					if json.Unmarshal(raw, &fields) != nil {
						return ErrInvalidStore
					}
					classification = fields.IssueType
				}
			}
			if err != nil {
				return err
			}
			typ, err = NativeIssueTypeURL(s.ScopeURL(), classification)
			if err != nil {
				return err
			}
			if err = s.ensureNativeTypeInTx(ctx, tx, typ); err != nil {
				return err
			}
		case "dependency":
			var kind string
			err := tx.QueryRowContext(ctx, `SELECT type FROM dependencies WHERE id=?`, v.key.String).Scan(&kind)
			if errors.Is(err, sql.ErrNoRows) && v.state == "deleted" {
				kind = map[string]string{"dependency": "blocks", "parent-child": "parent-child", "discovered-from": "discovered-from", "native-related": "related"}[originalTypeKey(v.typ, s.ScopeURL())]
				err = nil
			}
			if err != nil {
				return err
			}
			typ, err = NativeDependencyTypeURL(s.ScopeURL(), kind)
			if err != nil {
				return err
			}
			if err = s.ensureNativeTypeInTx(ctx, tx, typ); err != nil {
				return err
			}
		case "informational":
			if !schema8Informational(s.ScopeURL(), typ) {
				return ErrInvalidStore
			}
		default:
			return ErrInvalidStore
		}
		if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET type_url=? WHERE path=?`, typ, v.path); err != nil {
			return err
		}
		current, err := s.publicResourceURLInTx(ctx, tx, v.path)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_conversion_ids VALUES(?,?,?)`, v.path, s.ScopeURL()+v.path, current)
		return err
	}); err != nil {
		return err
	}
	// Retain conversion boundaries for Links before owners acquire their new sets.
	if err := walkConversionCatalog(ctx, tx, func(v conversionAllocation) error {
		if v.kind != "link" {
			return nil
		}
		if v.state == "deleted" {
			return s.convertDeletedLinkInTx(ctx, tx, v, actor)
		}
		token, err := freshToken()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=? WHERE path=?`, token, v.path); err != nil {
			return err
		}
		var record LinkRecord
		if v.backing == "dependency" {
			record, err = s.currentLinkForCreationInTx(ctx, tx, v.path)
		} else {
			record, err = s.currentInformationalLinkInTx(ctx, tx, v.path)
		}
		if err != nil {
			return err
		}
		raw, err := canonicalJSON(record)
		if err != nil {
			return err
		}
		return insertPreviewVersionInTx(ctx, tx, v.path, token, raw, record.Attribution.Actor)
	}); err != nil {
		return err
	}
	// Canonical human bodies and metadata are copied exactly. Only their new
	// graph revision and conversion attribution change; accepted learning envelopes
	// are neither rewritten nor newly verified.
	if err := walkConversionCatalog(ctx, tx, func(v conversionAllocation) error {
		if v.backing != "generic" {
			return nil
		}
		return s.convertMemoryBoundaryInTx(ctx, tx, v, actor)
	}); err != nil {
		return err
	}
	unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
	defer unscope()
	if err := walkConversionCatalog(ctx, tx, func(v conversionAllocation) error {
		if v.backing != "issue" || v.state != "live" {
			return nil
		}
		return s.recordIssueMappingContextInTx(ctx, tx, v.path, v.key.String, issueRevisionContext{Inception: "conversion"}, actor)
	}); err != nil {
		return err
	}
	return walkConversionCatalog(ctx, tx, func(v conversionAllocation) error {
		if v.state != "live" {
			return nil
		}
		switch v.backing {
		case "generic":
			_, err := s.showMemoryInTx(ctx, tx, v.path)
			return err
		case "issue":
			_, err := s.showIssueInTx(ctx, tx, v.path)
			return err
		default:
			_, err := s.showLinkInTx(ctx, tx, v.path)
			return err
		}
	})
}

func (s *Store) convertMemoryBoundaryInTx(ctx context.Context, tx *sql.Tx, v conversionAllocation, actor string) error {
	original, kind, found, err := s.originalVersionInTx(ctx, tx, v.path, v.head)
	if err != nil {
		return err
	}
	if !found || kind != "memory" {
		return ErrInvalidStore
	}
	var record Record
	if json.Unmarshal(original.Snapshot, &record) != nil {
		return ErrInvalidStore
	}
	record.Type = MemoryTypeURL(s.ScopeURL())
	record.Revision, err = freshToken()
	if err != nil {
		return err
	}
	record.Version = record.Revision
	record.Attribution = writeAttribution(actor)
	record.Attribution.Inception = "conversion"
	if v.state == "live" {
		record.Owned, err = s.memoryOwnedLinksInTx(ctx, tx, v.path)
		if err != nil {
			return err
		}
	} else if len(record.Owned) != 0 {
		return ErrInvalidStore
	}
	raw, err := canonicalJSON(record)
	if err != nil {
		return err
	}
	if err = insertPreviewVersionInTx(ctx, tx, v.path, record.Version, raw, actor); err != nil {
		return err
	}
	head := record.Version
	if v.state == "deleted" {
		token, err := freshToken()
		if err != nil {
			return err
		}
		terminal := MemoryTerminalState{ID: record.ID, Revision: token, Version: token, State: "deleted", PreviousVersion: head, Attribution: record.Attribution}
		raw, err := canonicalJSON(terminal)
		if err != nil {
			return err
		}
		if err = insertPreviewVersionInTx(ctx, tx, v.path, token, raw, actor); err != nil {
			return err
		}
		head = token
	}
	_, err = tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=? WHERE path=?`, head, v.path)
	return err
}

func (s *Store) convertDeletedLinkInTx(ctx context.Context, tx *sql.Tx, v conversionAllocation, actor string) error {
	original, kind, found, err := s.originalVersionInTx(ctx, tx, v.path, v.head)
	if err != nil {
		return err
	}
	if !found {
		return ErrInvalidStore
	}
	if kind == "terminal" {
		var terminal LinkTombstone
		if json.Unmarshal(original.Snapshot, &terminal) != nil || terminal.PreviousVersion == "" {
			return ErrInvalidStore
		}
		original, kind, found, err = s.originalVersionInTx(ctx, tx, v.path, terminal.PreviousVersion)
		if err != nil {
			return err
		}
		if !found {
			return ErrInvalidStore
		}
	}
	if kind != "link" {
		return ErrInvalidStore
	}
	var record LinkRecord
	if json.Unmarshal(original.Snapshot, &record) != nil {
		return ErrInvalidStore
	}
	record.Type = v.typ
	for _, endpoint := range []*string{&record.Source, &record.Target} {
		if !strings.HasPrefix(*endpoint, s.ScopeURL()) {
			return ErrInvalidStore
		}
		*endpoint, err = s.publicResourceURLInTx(ctx, tx, strings.TrimPrefix(*endpoint, s.ScopeURL()))
		if err != nil {
			return err
		}
	}
	if v.backing == "dependency" {
		_, native, ok := NativeTypeClassification(s.ScopeURL(), v.typ)
		if !ok {
			return ErrInvalidStore
		}
		id, idOK := record.Properties["nativeId"].(string)
		thread, threadOK := record.Properties["threadId"].(string)
		if !idOK || id == "" || !threadOK {
			return fmt.Errorf("%w: original Dependency lost its native facts", ErrInvalidStore)
		}
		record.Properties = nativeDependencyFacts(native, id, thread)
	}
	record.Revision, err = freshToken()
	if err != nil {
		return err
	}
	record.Version = record.Revision
	record.Attribution = writeAttribution(actor)
	record.Attribution.Inception = "conversion"
	raw, err := canonicalJSON(record)
	if err != nil {
		return err
	}
	if err = insertPreviewVersionInTx(ctx, tx, v.path, record.Version, raw, actor); err != nil {
		return err
	}
	token, err := freshToken()
	if err != nil {
		return err
	}
	terminal := LinkTombstone{ID: record.ID, Type: record.Type, Revision: token, Version: token, State: "deleted", PreviousVersion: record.Version, Attribution: record.Attribution}
	raw, err = canonicalJSON(terminal)
	if err != nil {
		return err
	}
	if err = insertPreviewVersionInTx(ctx, tx, v.path, token, raw, actor); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=? WHERE path=?`, token, v.path)
	return err
}

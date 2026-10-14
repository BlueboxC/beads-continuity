package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
)

// PreviewSnapshotLimit bounds this internal, complete current-state read. It is
// an operational preview limit, not BDP pagination or a claimed Read profile.
const PreviewSnapshotLimit = 2048

// Snapshot contains authoritative live records and installed descriptors from
// one transaction. WriterToken identifies controlled-writer state; it is not a
// public revision, historical selector, or proof against out-of-band SQL writes.
type Snapshot struct {
	WriterToken string
	Records     []any
	Types       []graph.TypeDescriptor
}

// CurrentSnapshot returns a complete bounded current inventory, or zero state
// and an error. It does not retain a database transaction between caller requests.
func (s *Store) CurrentSnapshot(ctx context.Context) (Snapshot, error) {
	var result Snapshot
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = s.currentSnapshotInTx(ctx, tx)
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return result, nil
}

func (s *Store) currentSnapshotInTx(ctx context.Context, tx *sql.Tx) (Snapshot, error) {
	if err := checkCurrentReadBytes(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	if err := checkBinding(ctx, tx, s.options); err != nil {
		return Snapshot{}, err
	}
	if err := s.checkCollectionMappingsInTx(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Records: []any{}, Types: []graph.TypeDescriptor{}}
	if err := tx.QueryRowContext(ctx, `SELECT writer_token FROM graph_preview_scope WHERE singleton=1`).Scan(&result.WriterToken); err != nil {
		return Snapshot{}, err
	}
	var err error
	result.Types, err = installedPreviewTypes(ctx, tx, s.ScopeURL(), s.options.Binding.SchemaVersion)
	if err != nil {
		return Snapshot{}, err
	}
	sort.Slice(result.Types, func(i, j int) bool { return graph.CompareCodeUnits(result.Types[i].ID(), result.Types[j].ID()) < 0 })
	var invalid int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM graph_preview_catalog WHERE allocation_state NOT IN ('live','deleted') LIMIT 1`).Scan(&invalid)
	if err == nil {
		return Snapshot{}, fmt.Errorf("%w: unknown allocation state", ErrInvalidStore)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT path,backing,resource_kind FROM graph_preview_catalog WHERE allocation_state='live' LIMIT ?`, PreviewSnapshotLimit+1)
	if err != nil {
		return Snapshot{}, err
	}
	type allocation struct{ path, backing, kind string }
	allocations := []allocation{}
	for rows.Next() {
		var a allocation
		if err := rows.Scan(&a.path, &a.backing, &a.kind); err != nil {
			return Snapshot{}, errors.Join(err, rows.Close())
		}
		allocations = append(allocations, a)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return Snapshot{}, err
	}
	if len(allocations) > PreviewSnapshotLimit {
		return Snapshot{}, fmt.Errorf("%w: complete current inventory exceeds %d live Resources", ErrLimitExceeded, PreviewSnapshotLimit)
	}
	sort.Slice(allocations, func(i, j int) bool { return graph.CompareCodeUnits(allocations[i].path, allocations[j].path) < 0 })
	links, err := s.snapshotNativeLinksInTx(ctx, tx)
	if err != nil {
		return Snapshot{}, err
	}
	memoryOwned := map[string][]json.RawMessage{}
	nativeOwned := map[string][]json.RawMessage{}
	backings := map[string]string{}
	for _, a := range allocations {
		backings[s.ScopeURL()+a.path] = a.backing
	}
	for _, a := range allocations {
		if a.backing != "dependency" && a.backing != "informational" {
			continue
		}
		if a.kind != "link" || validateLinkPath(a.path) != nil {
			return Snapshot{}, fmt.Errorf("%w: invalid Link allocation", ErrInvalidStore)
		}
		link, found := links[a.path]
		if a.backing == "informational" {
			link, err = s.showLinkInTx(ctx, tx, a.path)
			if err != nil {
				return Snapshot{}, err
			}
			links[a.path] = link
		} else if !found {
			return Snapshot{}, fmt.Errorf("%w: missing native Link", ErrInvalidStore)
		}
		if a.backing == "dependency" || backings[link.Source] == "generic" {
			raw, err := canonicalJSON(link)
			if err != nil {
				return Snapshot{}, err
			}
			if a.backing == "dependency" {
				nativeOwned[link.Source] = append(nativeOwned[link.Source], raw)
			} else {
				memoryOwned[link.Source] = append(memoryOwned[link.Source], raw)
			}
		}
	}
	// allocations are already ordered by canonical path, so owned sets keep
	// the same code-unit order as individual record readers.
	memories, err := s.snapshotMemoriesInTx(ctx, tx, memoryOwned)
	if err != nil {
		return Snapshot{}, err
	}

	for _, a := range allocations {
		if err := validateResourcePath(a.path); err != nil {
			return Snapshot{}, fmt.Errorf("%w: malformed catalog path: %v", ErrInvalidStore, err)
		}
		if (a.kind != "bead" && a.kind != "link") || (a.kind == "link") != strings.HasPrefix(a.path, "links/") {
			return Snapshot{}, fmt.Errorf("%w: catalog kind/path mismatch", ErrInvalidStore)
		}
		var record any
		var err error
		switch a.backing {
		case "generic":
			var found bool
			record, found = memories[a.path]
			if !found {
				err = fmt.Errorf("%w: missing current Memory", ErrInvalidStore)
			}
		case "issue":
			owned := snapshotOwned(nativeOwned, s.ScopeURL()+a.path)
			if len(owned) > PreviewOwnedLinkLimit {
				return Snapshot{}, fmt.Errorf("%w: owned Link limit exceeded", ErrInvalidStore)
			}
			record, err = s.showIssueWithOwnedInTx(ctx, tx, a.path, owned)
		case "dependency", "informational":
			record = links[a.path]
		default:
			err = fmt.Errorf("%w: unsupported backing", ErrInvalidStore)
		}
		if err != nil {
			return Snapshot{}, err
		}
		result.Records = append(result.Records, record)
	}
	return result, nil
}

// A collection must not hide current authoritative rows through missing catalog
// joins. This preview is a newly initialized, controlled-writer workspace, not
// an adopted legacy database with separately managed Issues.
func (s *Store) checkCollectionMappingsInTx(ctx context.Context, tx *sql.Tx) error {
	if err := s.checkLinkMappingsInTx(ctx, tx); err != nil {
		return err
	}
	var deletedInvalid int
	ids, err := s.installedInformationalTypes(ctx, tx)
	if err != nil {
		return err
	}
	placeholders, args := informationalTypeSQL(ids)
	nativePlaceholders, nativeArgs := informationalTypeSQL(s.nativeDependencyTypeURLs())
	args = append(args, nativeArgs...)
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_catalog WHERE allocation_state='deleted' AND ((backing='informational' AND type_url NOT IN (`+placeholders+`)) OR (backing='dependency' AND type_url NOT IN (`+nativePlaceholders+`)) OR type_url IS NULL)`, args...).Scan(&deletedInvalid); err != nil {
		return err
	}
	if deletedInvalid != 0 {
		return fmt.Errorf("%w: deleted Link Type differs from its authority", ErrInvalidStore)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM graph_preview_payloads p LEFT JOIN graph_preview_catalog c ON c.path=p.path WHERE c.path IS NULL OR c.resource_kind<>'bead' OR c.backing<>'generic' OR c.allocation_state<>'live'`,
		`SELECT COUNT(*) FROM issues i LEFT JOIN graph_preview_catalog c ON c.backing='issue' AND c.backing_key=i.id WHERE c.path IS NULL OR c.resource_kind<>'bead' OR c.allocation_state<>'live'`,
		`SELECT COUNT(*) FROM graph_preview_catalog WHERE allocation_state='deleted' AND NOT ((resource_kind='link' AND backing IN ('informational','dependency') AND backing_key IS NULL) OR (resource_kind='bead' AND backing='generic' AND backing_key IS NULL) OR (resource_kind='bead' AND backing='issue' AND backing_key IS NOT NULL))`,
	} {
		var invalid int
		if err := tx.QueryRowContext(ctx, query).Scan(&invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return fmt.Errorf("%w: incomplete current authority mapping", ErrInvalidStore)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT path FROM graph_preview_catalog WHERE allocation_state='deleted' AND backing='generic'`)
	if err != nil {
		return err
	}
	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return err
		}
		paths = append(paths, path)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, path := range paths {
		if _, err := s.deletedMemoryInTx(ctx, tx, path); err != nil {
			return err
		}
	}
	issueRows, err := tx.QueryContext(ctx, `SELECT path FROM graph_preview_catalog WHERE allocation_state='deleted' AND backing='issue'`)
	if err != nil {
		return err
	}
	issuePaths := []string{}
	for issueRows.Next() {
		var path string
		if err := issueRows.Scan(&path); err != nil {
			_ = issueRows.Close()
			return err
		}
		issuePaths = append(issuePaths, path)
	}
	if err := errors.Join(issueRows.Err(), issueRows.Close()); err != nil {
		return err
	}
	for _, path := range issuePaths {
		if err := s.validateDeletedIssueInTx(ctx, tx, path); err != nil {
			return err
		}
	}
	return nil
}

func snapshotOwned(owned map[string][]json.RawMessage, id string) []json.RawMessage {
	if items := owned[id]; items != nil {
		return items
	}
	return []json.RawMessage{}
}

// The complete-read byte/count guards and authority mapping checks have already
// run in this transaction. LEFT JOIN retains missing backing/history as an error.
func (s *Store) snapshotMemoriesInTx(ctx context.Context, tx *sql.Tx, owned map[string][]json.RawMessage) (map[string]Record, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.path,c.resource_kind,c.type_url,c.revision,p.properties,p.metadata,v.snapshot,v.actor
 FROM graph_preview_catalog c LEFT JOIN graph_preview_payloads p ON p.path=c.path
 LEFT JOIN graph_preview_versions v ON v.path=c.path AND v.version=c.revision
 WHERE c.allocation_state='live' AND c.backing='generic' LIMIT ?`, PreviewSnapshotLimit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := map[string]Record{}
	for rows.Next() {
		var path, kind, typ, revision string
		var payload, metadata, retained []byte
		var actor sql.NullString
		if err := rows.Scan(&path, &kind, &typ, &revision, &payload, &metadata, &retained, &actor); err != nil {
			return nil, err
		}
		if len(result) >= PreviewSnapshotLimit {
			return nil, ErrLimitExceeded
		}
		if kind != "bead" || validatePath(path) != nil || !actor.Valid || payload == nil || metadata == nil || retained == nil {
			return nil, fmt.Errorf("%w: incomplete current Memory authority", ErrInvalidStore)
		}
		links := snapshotOwned(owned, s.ScopeURL()+path)
		if len(links) > PreviewOwnedLinkLimit {
			return nil, fmt.Errorf("%w: owned Link limit exceeded", ErrInvalidStore)
		}
		record, err := s.validateCurrentMemory(path, typ, revision, payload, metadata, retained, actor.String, links)
		if err != nil {
			return nil, err
		}
		result[path] = record
	}
	return result, rows.Err()
}

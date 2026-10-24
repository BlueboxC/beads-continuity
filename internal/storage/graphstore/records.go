package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
)

func validatePath(path string) error {
	if err := graph.ValidateBeadPath(path); err != nil {
		return err
	}
	if len(path) > 1024 {
		return errors.New("preview Bead path exceeds 1024 bytes")
	}
	return nil
}

// Create atomically reserves a never-reused canonical path, writes its current
// payload and records the complete accepted snapshot. There is no keyed upsert
// or optional History switch. Commit uncertainty returns no success record.
func (s *Store) Create(ctx context.Context, req CreateRequest) (Record, error) {
	var err error
	if err := graph.ValidateGeneratedResourceIDRoot(req.IDRoot); err != nil {
		return Record{}, err
	}
	if req.IDRoot != "" && (req.PublicID != "" || req.Path != "") {
		return Record{}, fmt.Errorf("%w: ID root cannot accompany an explicit ID", graph.ErrValidation)
	}
	if req.PublicID == "" && req.Path == "" {
		req.generatePublicID = true
		req.PublicID, err = graph.GeneratedResourceIDCandidate(req.IDRoot, graph.GeneratedResourceIDInitialLength)
		if err != nil {
			return Record{}, err
		}
	}
	req, err = PrepareMemoryIdentity(req)
	if err != nil {
		return Record{}, err
	}
	if err := validatePath(req.Path); err != nil {
		return Record{}, err
	}
	if !utf8.ValidString(req.Title) || !utf8.ValidString(req.Body) || !utf8.ValidString(req.Actor) {
		return Record{}, errors.New("Memory title, body and actor must be valid UTF-8")
	}
	metadata, err := commonMetadata(req.Metadata)
	if err != nil {
		return Record{}, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(metadata, &fields)
	if _, reserved := fields[ContinuityKeyMetadata]; reserved {
		return Record{}, fmt.Errorf("%w: reserved %s metadata requires the continuity adapter", storage.ErrValidation, ContinuityKeyMetadata)
	}
	revision, err := freshToken()
	if err != nil {
		return Record{}, err
	}
	publicID := req.PublicID
	if publicID == "" {
		publicID = req.Path
	}
	publicURL, err := graph.CanonicalPublicResourceURL(s.ScopeURL(), publicID)
	if err != nil {
		return Record{}, err
	}
	r := Record{PublicID: publicID, ID: publicURL,
		Type: MemoryTypeURL(s.options.Binding.ScopeURL), Revision: revision, Version: revision,
		Properties: Properties{Title: req.Title, Body: req.Body}, Metadata: metadata, Owned: []json.RawMessage{}}
	if req.TypeURL != "" {
		r.Type = req.TypeURL
	}
	if err := s.requireAssessmentAuthoring(r.Type); err != nil {
		return Record{}, err
	}
	if len(req.PropertiesJSON) != 0 {
		if req.Title != "" || req.Body != "" {
			return Record{}, fmt.Errorf("%w: use either complete properties or title/body fields", graph.ErrValidation)
		}
		if err := json.Unmarshal(req.PropertiesJSON, &r.Properties); err != nil {
			return Record{}, err
		}
	}
	r.Attribution = Attribution{Inception: "create", Actor: req.Actor, Status: "unknown", RecordedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if req.Actor != "" {
		r.Attribution.Status = "claimed"
	}
	properties, err := canonicalJSON(r.Properties)
	if err != nil {
		return Record{}, err
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return Record{}, err
	}
	if !catalog.memoryType(r.Type) {
		return Record{}, fmt.Errorf("%w: generic creation requires an installed Memory-family Type", graph.ErrValidation)
	}
	if err := catalog.validateProperties(r.Type, properties, graph.KindBead); err != nil {
		return Record{}, err
	}
	err = s.withTypeValidationTx(ctx, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		accepted, err := s.createPreparedMemoryInTx(ctx, tx, req, r, properties)
		if err == nil {
			r = accepted
		}
		return err
	})
	if err != nil {
		return Record{}, err
	}
	return r, nil
}

// createInTx preserves the existing path-based importer seam. New public-ID
// imports use createPreparedMemoryInTx and its accepted record. Resolve any
// needed private locator from the transaction catalog after allocation.
func (s *Store) createInTx(ctx context.Context, tx *sql.Tx, req CreateRequest, r Record, properties, snapshot []byte) error {
	if err := s.requireAssessmentAuthoring(r.Type); err != nil {
		return err
	}
	if req.PublicID != "" {
		return fmt.Errorf("%w: public-ID import requires returned allocation", ErrCapabilityUnavailable)
	}
	canonical, err := canonicalJSON(r)
	if err != nil {
		return err
	}
	if !bytes.Equal(snapshot, canonical) {
		return fmt.Errorf("%w: legacy create snapshot differs from prepared record", ErrInvalidStore)
	}
	_, err = s.createPreparedMemoryInTx(ctx, tx, req, r, properties)
	return err
}

func (s *Store) createPreparedMemoryInTx(ctx context.Context, tx *sql.Tx, req CreateRequest, r Record, properties []byte) (Record, error) {
	if err := s.requireAssessmentAuthoring(r.Type); err != nil {
		return Record{}, err
	}
	if err := s.touchCoordination(ctx, tx); err != nil {
		return Record{}, err
	}
	publicID := req.PublicID
	if req.generatePublicID {
		var err error
		publicID, err = selectGeneratedPublicIDInTx(ctx, tx, req.IDRoot)
		if err != nil {
			return Record{}, err
		}
		req.Path, err = preferredPrivateResourcePath(graph.KindBead, publicID)
		if err != nil {
			return Record{}, err
		}
	}
	if publicID == "" {
		publicID = req.Path
	}
	if exists, err := publicResourceIDExistsInTx(ctx, tx, publicID); err != nil {
		return Record{}, err
	} else if exists {
		return Record{}, ErrAlreadyExists
	}
	privatePath, err := choosePrivateResourcePathInTx(ctx, tx, req.Path, req.privatePathGenerated, graph.KindBead)
	if err != nil {
		return Record{}, err
	}
	req.Path = privatePath
	publicURL, err := graph.CanonicalPublicResourceURL(s.ScopeURL(), publicID)
	if err != nil {
		return Record{}, err
	}
	r.ID = publicURL
	r.PublicID = publicID
	snapshot, err := canonicalJSON(r)
	if err != nil {
		return Record{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog
        (path, resource_kind, type_url, revision, allocation_state, backing, public_id)
        VALUES (?, 'bead', ?, ?, 'live', 'generic', ?)`, req.Path, r.Type, r.Revision, publicID); err != nil {
		return Record{}, err
	}
	if err := s.afterStage("allocation"); err != nil {
		return Record{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_payloads (path, properties, metadata) VALUES (?, ?, ?)`, req.Path, properties, r.Metadata); err != nil {
		return Record{}, err
	}
	if err := s.afterStage("payload"); err != nil {
		return Record{}, err
	}
	err = insertPreviewVersionInTx(ctx, tx, req.Path, r.Version, snapshot, req.Actor)
	if err != nil {
		return Record{}, err
	}
	if err := s.afterStage("retained"); err != nil {
		return Record{}, err
	}
	// Writes retain their existing request and Type budgets. Complete readers
	// enforce the complete-current acquisition ceiling independently.
	return r, nil
}

func (s *Store) touchCoordination(ctx context.Context, tx *sql.Tx) error {
	// Each controlled writer changes this same cell to a fresh random value.
	// Concurrent snapshots cannot silently merge disjoint successful mutations.
	token, err := freshToken()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE graph_preview_scope SET writer_token = ? WHERE singleton = 1`, token)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: missing writer coordination row", ErrInvalidStore)
	}
	if err := s.afterStage("coordination"); err != nil {
		return err
	}
	return nil
}

func (s *Store) afterStage(stage string) error {
	if s.afterWrite != nil {
		return s.afterWrite(stage)
	}
	return nil
}

// Show reads one canonical Memory and verifies its required retained snapshot
// in the same read transaction. Corrupt/incomplete state is never an absence.
func (s *Store) Show(ctx context.Context, path string) (Record, error) {
	if err := validatePath(path); err != nil {
		return Record{}, err
	}
	var record Record
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		var err error
		record, err = s.showMemoryInTx(ctx, tx, path)
		return err
	})
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Store) showMemoryInTx(ctx context.Context, tx *sql.Tx, path string) (Record, error) {
	var record Record
	if err := checkBinding(ctx, tx, s.options); err != nil {
		return Record{}, err
	}
	var kind, typ, revision, state, backing string
	err := tx.QueryRowContext(ctx, `SELECT resource_kind, type_url, revision, allocation_state, backing
            FROM graph_preview_catalog WHERE path = ?`, path).Scan(&kind, &typ, &revision, &state, &backing)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, err
	}
	if state == "deleted" {
		if _, err := s.deletedMemoryInTx(ctx, tx, path); err != nil {
			return Record{}, err
		}
		return Record{}, ErrGone
	}
	if kind != "bead" || !s.isMemoryType(typ) || !authorityID.MatchString(revision) || state != "live" || backing != "generic" {
		return Record{}, fmt.Errorf("%w: unsupported or corrupt allocation", ErrInvalidStore)
	}
	var payload, metadata, snapshot []byte
	var actor string
	if err := tx.QueryRowContext(ctx, `SELECT properties, metadata FROM graph_preview_payloads WHERE path = ?`, path).Scan(&payload, &metadata); err != nil {
		return Record{}, fmt.Errorf("%w: missing current payload: %v", ErrInvalidStore, err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT snapshot, actor FROM graph_preview_versions WHERE path = ? AND version = ?`, path, revision).Scan(&snapshot, &actor); err != nil {
		return Record{}, fmt.Errorf("%w: missing retained state: %v", ErrInvalidStore, err)
	}
	publicURL, err := s.publicResourceURLInTx(ctx, tx, path)
	if err != nil {
		return Record{}, err
	}
	record = Record{ID: publicURL, Type: typ,
		Revision: revision, Version: revision, Owned: []json.RawMessage{}}
	var retained Record
	if err := json.Unmarshal(snapshot, &retained); err != nil {
		return Record{}, fmt.Errorf("%w: malformed retained state", ErrInvalidStore)
	}
	record.Metadata, err = commonMetadata(metadata)
	if err != nil || !bytes.Equal(record.Metadata, metadata) {
		return Record{}, fmt.Errorf("%w: invalid current Memory metadata", ErrInvalidStore)
	}
	record.Owned, err = s.memoryOwnedLinksInTx(ctx, tx, path)
	if err != nil {
		return Record{}, err
	}
	record.Attribution = retained.Attribution
	if !validVersionAttribution(record.Attribution, actor) {
		return Record{}, fmt.Errorf("%w: invalid retained attribution", ErrInvalidStore)
	}
	if err := json.Unmarshal(payload, &record.Properties); err != nil {
		return Record{}, fmt.Errorf("%w: malformed payload", ErrInvalidStore)
	}
	canonicalPayload, err := canonicalJSON(record.Properties)
	if err != nil {
		return Record{}, err
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return Record{}, err
	}
	if err := catalog.validateProperties(typ, canonicalPayload, graph.KindBead); err != nil {
		return Record{}, fmt.Errorf("%w: current payload violates its Type: %v", ErrInvalidStore, err)
	}
	canonicalSnapshot, err := canonicalJSON(record)
	if err != nil {
		return Record{}, err
	}
	if !bytes.Equal(payload, canonicalPayload) || !bytes.Equal(snapshot, canonicalSnapshot) {
		return Record{}, fmt.Errorf("%w: current and retained state differ", ErrInvalidStore)
	}
	record.PublicID, err = memoryPublicIDInTx(ctx, tx, path)
	if err != nil {
		return Record{}, err
	}
	if record.PublicID == "" {
		record.PublicID = path
	}
	return record, nil
}

// insertPreviewVersionInTx retains one version of a Memory or Link with the
// ordinal that orders its history and the instant it was written. It must run
// inside the caller's write transaction so the ordinal read and the insert see
// the same rows.
func insertPreviewVersionInTx(ctx context.Context, tx *sql.Tx, path, version string, snapshot []byte, actor string) error {
	// MAX+1 is safe here because of a FENCE, not because of luck, and naming the
	// mechanism matters more than naming the effect. All callers have already
	// called touchCoordination in this same transaction (Memory create/update,
	// Dependency add/unlink, informational Link create/update, Link lifecycle,
	// and legacy import),
	// which UPDATEs graph_preview_scope.writer_token WHERE singleton=1. That is
	// a STORE-WIDE cell, not a per-path one, so two concurrent version writers
	// in one store always contend on it and the loser fails at COMMIT with
	// MySQL 1213 / SQLSTATE 40001, which classifyCommitError maps to ErrConflict
	// and the CLI renders as revision_conflict (exit 4).
	//
	// one_path_ordinal is the BACKSTOP, and it is load-bearing rather than
	// decorative: measured on a real Dolt server, two interleaved MAX+1
	// transactions WITHOUT that unique key both commit and leave two rows
	// sharing one ordinal. With it, the loser fails at commit with ERROR 1105.
	//
	// The fence is measured on the real path, not only in a probe. All three
	// /server concurrency tests collide on ONE path by construction:
	// TestMemoryUpdateConcurrentWriters on beads/plan, and
	// TestInformationalOwnedConcurrentMutations and
	// TestLinkUnlinkConcurrentMutations on beads/source, because an owned-Link
	// write bumps its source's retained version. Each pauses both writers after
	// their insert, so both computed MAX+1 against a snapshot lacking the
	// other's row and hold the SAME ordinal at commit -- exactly the precondition
	// that produced 1105 without the fence. All three pass with the loser
	// required to be ErrConflict and NOT ErrOutcomeUnknown, so the fence wins
	// the race and the backstop does not fire.
	//
	// If the backstop ever does fire, the user-facing answer is poor:
	// classifyCommitError treats anything other than 1213/40001 as
	// ErrOutcomeUnknown, so a 1105 renders as outcome_unknown (exit 6, "do not
	// automatically replay") even though Dolt has stated it rolled the
	// transaction back, which makes the outcome known. Pre-existing, and shared
	// with every graph command.
	//
	// So the real hazard is no longer concurrency. It is a FUTURE WRITER that
	// inserts a version without calling touchCoordination first and so never
	// joins the fence. Related native-plane hazard: gastownhall/beads#6379.
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(ordinal),0)+1 FROM graph_preview_versions WHERE path=?`, path).Scan(&ordinal); err != nil {
		return err
	}
	// change_at is DATETIME(6) and must stay untruncated. Dolt's precision-0
	// datetime rounds half-up rather than truncating, which broke same-second
	// ordering on the native plane until migration 0069 widened the column and
	// #6661 removed a Truncate(time.Second) (pinned by
	// TestRecordVersionKeepsSubSecondChangeAt). Ordinal is the ordering key;
	// change_at is for display and --at selection, so do not floor it here.
	//
	// KNOWN: this is a SECOND CLOCK for one write. The record's own
	// attribution.recordedAt is stamped elsewhere, so the two disagree by a few
	// milliseconds -- measured 01:13:20.60181555Z against change_at
	// 01:13:20.606254 for the same version. Harmless while the ordinal rules
	// ordering, but it is a trap laid for `--at` selection (be-hs42e.6.1.1),
	// which will have to pick one of the two and will disagree with `show` at
	// boundaries. The fix is to pass the attributed instant in rather than
	// reading the clock again here; deliberately not done in the change that
	// found it, because it alters all six writers.
	changeAt := time.Now().UTC()
	_, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_versions (path, version, snapshot, actor, ordinal, change_at) VALUES (?, ?, ?, ?, ?, ?)`, path, version, snapshot, actor, ordinal, changeAt)
	return err
}

func nullableMemoryPublicID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

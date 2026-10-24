package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memoryapi"
	txmemory "github.com/steveyegge/beads/internal/storage/memoryops"
	publicops "github.com/steveyegge/beads/issueops"
)

// PlanSchema8Conversion accepts only an explicit copy outside the bound workspace.
// The target binding is schema11; source discovery remains a non-migrating read.
func PlanSchema8Conversion(ctx context.Context, o Options) (plan ConversionPlan, err error) {
	if err = validateConversionOptions(o); err != nil {
		return plan, err
	}
	if withinPath(o.DataDir, o.Binding.WorkspaceID) {
		return plan, fmt.Errorf("%w: schema8 rehearsal requires an isolated copy", ErrCapabilityUnavailable)
	}
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		return plan, err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer func() { err = errors.Join(err, tx.Rollback()) }()
	return planSchema8InTx(ctx, tx, o)
}

func withinPath(path, parent string) bool {
	if path == parent || strings.HasPrefix(path, parent+string(filepath.Separator)) {
		return true
	}
	info, err := os.Stat(parent)
	if err != nil {
		return false
	}
	// Canonical string comparison alone misses case aliases on macOS. Walk
	// existing ancestors as well, including when the output does not exist yet.
	for current := path; ; current = filepath.Dir(current) {
		if candidate, err := os.Stat(current); err == nil && os.SameFile(info, candidate) {
			return true
		}
		if next := filepath.Dir(current); next == current {
			return false
		}
	}
}

// ConversionPathsOverlap also recognizes physical ancestor aliases for copy
// preflight, including an output whose nearest existing parent aliases a source.
func ConversionPathsOverlap(a, b string) bool { return withinPath(a, b) || withinPath(b, a) }

func planSchema8InTx(ctx context.Context, tx *sql.Tx, o Options) (ConversionPlan, error) {
	p := ConversionPlan{SourceSchema: 8, SourceDataDir: o.DataDir, Database: o.Database, Binding: o.Binding, HumanMemories: map[string]string{}}
	var source Binding
	var token string
	if err := tx.QueryRowContext(ctx, `SELECT workspace,scope_url,authority_id,schema_version,writer_token FROM graph_preview_scope WHERE singleton=1`).Scan(&source.WorkspaceID, &source.ScopeURL, &source.AuthorityID, &source.SchemaVersion, &token); err != nil {
		return p, err
	}
	expected := o.Binding
	expected.SchemaVersion = 8
	if source != expected || !authorityID.MatchString(token) {
		return p, ErrInvalidStore
	}
	descriptors, err := schema8Descriptors(o.Binding.ScopeURL)
	if err != nil {
		return p, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_types`).Scan(&count); err != nil {
		return p, err
	}
	if count != len(descriptors) {
		return p, ErrInvalidStore
	}
	for _, d := range descriptors {
		var raw []byte
		var fingerprint string
		if err := tx.QueryRowContext(ctx, `SELECT descriptor,fingerprint FROM graph_preview_types WHERE name=?`, originalTypeKey(d.ID(), source.ScopeURL)).Scan(&raw, &fingerprint); err != nil {
			return p, err
		}
		if !bytes.Equal(raw, d.CanonicalJSON()) || fingerprint != d.Fingerprint() {
			return p, fmt.Errorf("%w: source Type differs", ErrInvalidStore)
		}
	}
	p.State, err = conversionState(ctx, tx)
	if err != nil {
		return p, err
	}
	for branch, dirty := range p.State.BranchDirty {
		if branch != "main" && dirty {
			return p, ErrCapabilityUnavailable
		}
	}
	for name := range p.State.Tables {
		if strings.HasPrefix(name, "graph_preview_original_") || strings.HasPrefix(name, "graph_preview_source_") || name == "graph_preview_conversion_origin" {
			return p, ErrAlreadyExists
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_catalog WHERE allocation_state='live'`).Scan(&p.Resources); err != nil {
		return p, err
	}
	if p.Resources > PreviewContinuityReadRowLimit {
		return p, ErrLimitExceeded
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM issues ORDER BY BINARY id`)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return p, err
		}
		p.IssueIDs = append(p.IssueIDs, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return p, err
	}
	prefix := txmemory.StorageKey("")
	rows, err = tx.QueryContext(ctx, "SELECT `key`,CASE WHEN OCTET_LENGTH(value)<=? THEN value ELSE NULL END FROM config WHERE BINARY LEFT(`key`,CHAR_LENGTH(?))=BINARY ? AND BINARY LEFT(value,CHAR_LENGTH(?))=BINARY ? ORDER BY BINARY `key`", len(continuityPointer)+1024, prefix, prefix, continuityPointer, continuityPointer)
	if err != nil {
		return p, err
	}
	type pointer struct{ key, path string }
	var pointers []pointer
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return p, err
		}
		key := strings.TrimPrefix(k, prefix)
		if derivedContinuityKey(key) {
			_ = rows.Close()
			return p, ErrInvalidStore
		}
		if _, err := memoryapi.ValidateKey(key); err != nil {
			_ = rows.Close()
			return p, err
		}
		pointers = append(pointers, pointer{key, strings.TrimPrefix(v, continuityPointer)})
		if len(pointers) > PreviewSnapshotLimit {
			_ = rows.Close()
			return p, ErrLimitExceeded
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return p, err
	}
	for _, v := range pointers {
		var size uint64
		if err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(properties)+COALESCE(OCTET_LENGTH(metadata),0) FROM graph_preview_payloads WHERE path=?`, v.path).Scan(&size); err != nil {
			return p, err
		}
		if size > PreviewCurrentReadByteLimit {
			return p, ErrLimitExceeded
		}
		var raw, metadata []byte
		if err := tx.QueryRowContext(ctx, `SELECT p.properties,p.metadata FROM graph_preview_payloads p JOIN graph_preview_catalog c ON c.path=p.path WHERE p.path=? AND c.backing='generic' AND c.allocation_state='live'`, v.path).Scan(&raw, &metadata); err != nil {
			return p, err
		}
		var props Properties
		if json.Unmarshal(raw, &props) != nil || continuityKey(metadata) != v.key {
			return p, ErrInvalidStore
		}
		p.HumanMemories[v.key] = conversionHash([]byte(props.Body))
	}
	// Every non-derived key must have been resolved above. A plaintext or
	// malformed key cannot silently disappear from the preservation plan.
	var humanCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM config WHERE BINARY LEFT(`key`,CHAR_LENGTH(?))=BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? AND BINARY `key`<>BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ?", prefix, prefix, prefix+"@knowledge/code/", prefix+"@knowledge/code/", prefix+"@knowledge/catalog", prefix+"@knowledge/catalog-part/", prefix+"@knowledge/catalog-part/", prefix+"@activity/", prefix+"@activity/").Scan(&humanCount); err != nil {
		return p, err
	}
	if humanCount != len(p.HumanMemories) {
		return p, fmt.Errorf("%w: not every human key has a canonical Memory", ErrInvalidStore)
	}
	// Unlike native adoption, every config row remains byte-identical.
	p.OtherConfigSHA256, err = conversionOtherConfigHash(ctx, tx, nil)
	return p, err
}

// UpgradeSchema8Candidate appends a conversion boundary on a separately restored
// candidate. Original tokens, native rows, snapshots and Dolt references survive.
// Partial DDL belongs only to the disposable candidate; the source is untouched.
func UpgradeSchema8Candidate(ctx context.Context, o Options, p ConversionPlan, actor string) (err error) {
	return upgradeSchema8Candidate(ctx, o, p, actor, nil)
}

func upgradeSchema8Candidate(ctx context.Context, o Options, p ConversionPlan, actor string, hook func(string) error) (err error) {
	if p.SourceSchema != 8 || actor == "" || o.Binding != p.Binding || o.Database != p.Database {
		return ErrInvalidStore
	}
	if err = validateConversionOptions(o); err != nil {
		return err
	}
	if withinPath(o.DataDir, p.SourceDataDir) || withinPath(p.SourceDataDir, o.DataDir) || withinPath(o.DataDir, o.Binding.WorkspaceID) {
		return ErrInvalidStore
	}
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		return err
	}
	s.afterWrite = hook
	defer func() { err = errors.Join(err, s.Close()) }()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, conn.Close())
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	got, readErr := planSchema8InTx(ctx, tx, o)
	rollbackErr := tx.Rollback()
	if readErr != nil || rollbackErr != nil {
		return errors.Join(readErr, rollbackErr)
	}
	got.SourceDataDir = p.SourceDataDir
	if !reflect.DeepEqual(got, p) {
		return ErrConflict
	}
	// Small source-format preimages preserve native bookkeeping and unaddressed
	// native versions too. Derived config/index/activity tables are never copied.
	for _, name := range []string{"issues", "issue_versions", "graph_preview_catalog", "graph_preview_versions", "graph_preview_issue_versions"} {
		destination := "graph_preview_source_" + name
		if _, err = conn.ExecContext(ctx, "CREATE TABLE `"+destination+"` LIKE `"+name+"`"); err != nil {
			return err
		}
		//nolint:gosec // G202: both identifiers come only from the package-owned source-preimage allowlist above.
		if _, err = conn.ExecContext(ctx, "INSERT INTO `"+destination+"` SELECT * FROM `"+name+"`"); err != nil {
			return err
		}
	}
	for _, ddl := range append(append([]string(nil), originalHistoryDDL...),
		`ALTER TABLE graph_preview_catalog ADD COLUMN public_id LONGBLOB NULL`,
		`ALTER TABLE graph_preview_links ADD COLUMN source_revision VARBINARY(4096) NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_preview_links ADD COLUMN target_revision VARBINARY(4096) NOT NULL DEFAULT ''`,
		`ALTER TABLE graph_preview_versions ADD COLUMN native_participation_generation BIGINT NULL`,
		`ALTER TABLE graph_preview_versions ADD COLUMN native_attribution LONGBLOB NULL`,
		`ALTER TABLE graph_preview_issue_versions ADD COLUMN revision_context LONGBLOB NULL`,
		`ALTER TABLE graph_preview_issue_versions ADD COLUMN history_ordinal BIGINT NULL`,
		`ALTER TABLE issue_versions ADD COLUMN removed_restriction VARCHAR(32) NULL`) {
		if _, err = conn.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	if err = conn.Close(); err != nil {
		return err
	}
	closed = true
	return s.withTxPolicy(ctx, txPolicy{mayWrite: true, commit: true, validateTypes: true, conversion: true}, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_original_types SELECT * FROM graph_preview_types`); err != nil {
			return err
		}
		if err := s.archiveSchema8InTx(ctx, tx); err != nil {
			return err
		}
		if err := s.afterStage("conversion-history-archived"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM graph_preview_types`); err != nil {
			return err
		}
		for _, definition := range previewTypeDefinitions() {
			d, err := definition.build(s.ScopeURL())
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_types(name,descriptor,fingerprint) VALUES (?,?,?)`, definition.name, d.CanonicalJSON(), d.Fingerprint()); err != nil {
				return err
			}
		}
		token, err := freshToken()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE graph_preview_scope SET schema_version=?,writer_token=? WHERE singleton=1`, SchemaVersion, token); err != nil {
			return err
		}
		if err := s.reprojectSchema8InTx(ctx, tx, actor); err != nil {
			return err
		}
		manifest, err := canonicalJSON(p)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_conversion_origin VALUES(1,?,?)`, manifest, conversionHash(manifest)); err != nil {
			return err
		}
		state, err := conversionState(ctx, tx)
		if err != nil {
			return err
		}
		if state.Head != p.State.Head || state.Staged != p.State.Staged || !reflect.DeepEqual(state.Branches, p.State.Branches) || !reflect.DeepEqual(state.Tags, p.State.Tags) {
			return fmt.Errorf("%w: conversion changed retained Dolt references", ErrInvalidStore)
		}
		for name, hash := range p.State.Tables {
			if !strings.HasPrefix(name, "graph_preview_") && name != "issues" && name != "issue_versions" && state.Tables[name] != hash {
				return fmt.Errorf("%w: conversion changed %s", ErrInvalidStore, name)
			}
		}
		if err := checkBinding(ctx, tx, o); err != nil {
			return err
		}
		return s.afterStage("conversion-history-verified")
	})
}

type originalAddress struct{ path, version, kind string }

func (s *Store) archiveSchema8InTx(ctx context.Context, tx *sql.Tx) error {
	for _, native := range []bool{false, true} {
		table := "graph_preview_versions"
		if native {
			table = "graph_preview_issue_versions"
		}
		lastPath, lastVersion := "", ""
		for {
			rows, err := tx.QueryContext(ctx, "SELECT path,version FROM "+table+" WHERE path>? OR(path=? AND version>?) ORDER BY path,version LIMIT 128", lastPath, lastPath, lastVersion)
			if err != nil {
				return err
			}
			var page []originalAddress
			for rows.Next() {
				var v originalAddress
				if err := rows.Scan(&v.path, &v.version); err != nil {
					_ = rows.Close()
					return err
				}
				page = append(page, v)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			for _, address := range page {
				var raw []byte
				kind := "issue"
				if native {
					raw, err = s.schema8IssueBytesInTx(ctx, tx, address.path, address.version)
				} else {
					var size uint64
					if err = tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(snapshot) FROM graph_preview_versions WHERE path=? AND version=?`, address.path, address.version).Scan(&size); err != nil {
						return err
					}
					if size > PreviewCurrentReadByteLimit {
						return ErrLimitExceeded
					}
					err = tx.QueryRowContext(ctx, `SELECT snapshot FROM graph_preview_versions WHERE path=? AND version=?`, address.path, address.version).Scan(&raw)
					var h struct{ State string }
					if json.Unmarshal(raw, &h) != nil {
						return ErrInvalidStore
					}
					if h.State != "" {
						kind = "terminal"
					} else if strings.HasPrefix(address.path, "links/") {
						kind = "link"
					} else {
						kind = "memory"
					}
				}
				if err != nil {
					return err
				}
				canonical, err := graph.CanonicalizeJSON(raw)
				if err != nil || !bytes.Equal(canonical, raw) {
					return ErrInvalidStore
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_original_versions(path,version,kind,snapshot,sha256) VALUES(?,?,?,?,?)`, address.path, address.version, kind, raw, conversionHash(raw)); err != nil {
					return err
				}
			}
			lastPath, lastVersion = page[len(page)-1].path, page[len(page)-1].version
		}
	}
	return nil
}

func (s *Store) schema8IssueBytesInTx(ctx context.Context, tx *sql.Tx, path, version string) ([]byte, error) {
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(v.durable_state)+OCTET_LENGTH(m.owned)+512 FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=? AND m.version=?`, path, version).Scan(&size); err != nil {
		return nil, err
	}
	if size > PreviewCurrentReadByteLimit {
		return nil, ErrLimitExceeded
	}
	var state, owned []byte
	var actor, status string
	var at time.Time
	if err := tx.QueryRowContext(ctx, `SELECT v.durable_state,m.owned,COALESCE(v.change_actor,''),v.attribution_status,v.change_at FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=? AND m.version=?`, path, version).Scan(&state, &owned, &actor, &status, &at); err != nil {
		return nil, err
	}
	var issue publicops.Issue
	var links []json.RawMessage
	if json.Unmarshal(state, &issue) != nil || !sameVersionJSON(state, &issue) || json.Unmarshal(owned, &links) != nil || !sameVersionJSON(owned, links) {
		return nil, ErrInvalidStore
	}
	metadata, err := commonMetadata(issue.Metadata)
	if err != nil {
		return nil, err
	}
	issue.Metadata = nil
	issue.Dependencies = nil
	return canonicalJSON(IssueRecord{ID: s.ScopeURL() + path, Type: IssueTypeURLForSchema(s.ScopeURL(), 8), Revision: version, Version: version, Properties: &issue, Metadata: metadata, Owned: links, Attribution: Attribution{Actor: actor, Status: status, RecordedAt: at.UTC().Format(time.RFC3339Nano)}})
}

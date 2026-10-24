package graphstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/memoryapi"
	"github.com/steveyegge/beads/internal/storage/issueops"
	txmemory "github.com/steveyegge/beads/internal/storage/memoryops"
	"github.com/steveyegge/beads/internal/storage/schema"
	"github.com/steveyegge/beads/internal/storage/sqlbuild"
)

// ConversionState includes all current tables, branches and working/staged roots.
// It is a private local fingerprint, not a backup or an authorization grant.
type ConversionState struct {
	Head, Working, Staged string
	Branches              map[string]string
	Tags                  map[string]string
	BranchDirty           map[string]bool
	Tables                map[string]string
}

// ConversionPlan contains identities and hashes, never memory bodies.
type ConversionPlan struct {
	SourceSchema int

	SourceDataDir, Database string
	Binding                 Binding
	State                   ConversionState
	IssueColumns            []string
	IssueRowsSHA256         string
	IssueIDs, DependencyIDs []string
	HumanMemories           map[string]string
	OtherConfigSHA256       string
	Resources               int
	PruneObsoleteCode       bool
}

func conversionState(ctx context.Context, q schema.DBConn) (ConversionState, error) {
	s := ConversionState{Branches: map[string]string{}, Tables: map[string]string{}, Tags: map[string]string{}, BranchDirty: map[string]bool{}}
	if err := q.QueryRowContext(ctx, "SELECT DOLT_HASHOF('HEAD'),DOLT_HASHOF_DB('WORKING'),DOLT_HASHOF_DB('STAGED')").Scan(&s.Head, &s.Working, &s.Staged); err != nil {
		return s, err
	}
	rows, err := q.QueryContext(ctx, "SELECT name,hash,dirty FROM dolt_branches ORDER BY name")
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var name, hash string
		var dirty bool
		if err := rows.Scan(&name, &hash, &dirty); err != nil {
			return s, errors.Join(err, rows.Close())
		}
		s.Branches[name] = hash
		s.BranchDirty[name] = dirty
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return s, err
	}
	rows, err = q.QueryContext(ctx, "SELECT tag_name,tag_hash FROM dolt_tags ORDER BY tag_name")
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var name, hash string
		if err := rows.Scan(&name, &hash); err != nil {
			return s, errors.Join(err, rows.Close())
		}
		s.Tags[name] = hash
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return s, err
	}
	rows, err = q.QueryContext(ctx, "SHOW FULL TABLES")
	if err != nil {
		return s, err
	}
	type namedTable struct{ name, kind string }
	var names []namedTable
	for rows.Next() {
		var table namedTable
		if err := rows.Scan(&table.name, &table.kind); err != nil {
			return s, errors.Join(err, rows.Close())
		}
		names = append(names, table)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return s, err
	}
	for _, table := range names {
		var hash string
		switch table.kind {
		case "BASE TABLE":
			if err := q.QueryRowContext(ctx, "SELECT DOLT_HASHOF_TABLE(?)", table.name).Scan(&hash); err != nil {
				return s, err
			}
		case "VIEW":
			var name, definition, charset, collation string
			if err := q.QueryRowContext(ctx, "SHOW CREATE VIEW `"+strings.ReplaceAll(table.name, "`", "``")+"`").Scan(&name, &definition, &charset, &collation); err != nil {
				return s, fmt.Errorf("fingerprint view %s: %w", table.name, err)
			}
			hash = "view:" + conversionHash([]byte(definition))
		default:
			return s, fmt.Errorf("%w: unknown table family %s", ErrInvalidStore, table.kind)
		}
		s.Tables[table.name] = hash
	}
	return s, nil
}

// PlanConversion inspects an existing embedded native database without migrating it.
func PlanConversion(ctx context.Context, o Options) (ConversionPlan, error) {
	return planConversion(ctx, o, false)
}

// PlanConversionForCodePrune admits a bounded larger native plane only for an
// explicit restored-candidate prune. Final graph admission keeps its 64 MiB limit.
func PlanConversionForCodePrune(ctx context.Context, o Options) (ConversionPlan, error) {
	return planConversion(ctx, o, true)
}

func planConversion(ctx context.Context, o Options, prune bool) (plan ConversionPlan, err error) {
	if err = validateConversionOptions(o); err != nil {
		return plan, err
	}
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		return plan, err
	}
	defer func() {
		err = errors.Join(err, s.Close())
		if err != nil {
			plan = ConversionPlan{}
		}
	}()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return plan, err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return plan, err
	}
	defer func() { err = errors.Join(err, tx.Rollback()) }()
	plan, err = planConversionWithPruneInTx(ctx, tx, o, prune)
	return plan, err
}

func validateConversionOptions(o Options) error {
	if err := validateOptions(o); err != nil {
		return err
	}
	actual, err := filepath.EvalSymlinks(o.DataDir)
	if err != nil || actual != o.DataDir {
		return fmt.Errorf("%w: source data directory must be canonical", ErrInvalidStore)
	}
	if o.Backend != "embedded" || o.Binding.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: conversion requires embedded native storage and current graph schema", ErrCapabilityUnavailable)
	}
	if info, err := os.Stat(filepath.Join(o.DataDir, o.Database, ".dolt")); err != nil || !info.IsDir() {
		return fmt.Errorf("%w: native database missing", ErrInvalidStore)
	}
	return nil
}

func planConversionInTx(ctx context.Context, tx *sql.Tx, o Options) (ConversionPlan, error) {
	return planConversionWithPruneInTx(ctx, tx, o, false)
}

func planConversionWithPruneInTx(ctx context.Context, tx *sql.Tx, o Options, prune bool) (ConversionPlan, error) {
	p := ConversionPlan{PruneObsoleteCode: prune, SourceDataDir: o.DataDir, Database: o.Database, Binding: o.Binding, HumanMemories: map[string]string{}}
	var err error
	p.State, err = conversionState(ctx, tx)
	if err != nil {
		return p, err
	}
	for branch, dirty := range p.State.BranchDirty {
		if branch != "main" && dirty {
			return p, fmt.Errorf("%w: uncommitted non-main branch %s is outside initial conversion support", ErrCapabilityUnavailable, branch)
		}
	}
	var wisps int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisps").Scan(&wisps); err != nil {
		return p, err
	}
	if wisps != 0 {
		return p, fmt.Errorf("%w: existing wisps are outside conversion support", ErrCapabilityUnavailable)
	}
	for name := range p.State.Tables {
		if strings.HasPrefix(name, "graph_preview_") || name == "issue_versions" || name == "store_epoch" {
			return p, fmt.Errorf("%w: conversion collides with existing table %s", ErrInvalidStore, name)
		}
	}
	version, err := schema.CurrentVersion(ctx, tx)
	if err != nil {
		return p, err
	}
	if version != schema.LatestVersion() {
		return p, fmt.Errorf("%w: native schema must already be at %d", ErrInvalidStore, schema.LatestVersion())
	}
	if _, err := issueops.ReadConfigPrefix(ctx, tx); err != nil {
		return p, err
	}
	var branch string
	if err := tx.QueryRowContext(ctx, "SELECT active_branch()").Scan(&branch); err != nil {
		return p, err
	}
	if branch != "main" {
		return p, fmt.Errorf("%w: conversion requires branch main", ErrInvalidStore)
	}
	if prune {
		rows, bytes, err := continuityReadUsage(ctx, tx)
		if err != nil {
			return p, err
		}
		if rows > PreviewContinuityReadRowLimit || bytes > 2<<30 {
			return p, fmt.Errorf("%w: native prune rehearsal exceeds 65536 rows or 2 GiB", ErrLimitExceeded)
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='dependencies'")
	if err != nil {
		return p, err
	}
	known := []string{"id", "issue_id", "depends_on_issue_id", "depends_on_wisp_id", "depends_on_external", "type", "created_at", "created_by", "metadata", "thread_id"}
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return p, errors.Join(err, rows.Close())
		}
		if !slices.Contains(known, column) {
			return p, errors.Join(fmt.Errorf("%w: unsupported Dependency column %s", ErrInvalidStore, column), rows.Close())
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return p, err
	}
	// Refuse unsupported native columns rather than hide data outside the graph projection.
	allowed := strings.Split(sqlbuild.IssueBaseColumns+",is_blocked,hook_bead,role_bead,agent_state,last_activity,role_type,rig", ",")
	for i := range allowed {
		allowed[i] = strings.TrimSpace(allowed[i])
	}
	rows, err = tx.QueryContext(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='issues' ORDER BY ordinal_position")
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return p, errors.Join(err, rows.Close())
		}
		if !slices.Contains(allowed, column) {
			return p, errors.Join(fmt.Errorf("%w: unsupported Issue column %s", ErrInvalidStore, column), rows.Close())
		}
		p.IssueColumns = append(p.IssueColumns, column)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return p, err
	}
	for _, column := range []string{"hook_bead", "role_bead", "agent_state", "last_activity", "role_type", "rig"} {
		if !slices.Contains(p.IssueColumns, column) {
			continue
		}
		var populated int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM issues WHERE `"+column+"` IS NOT NULL AND CAST(`"+column+"` AS CHAR)<>''").Scan(&populated); err != nil {
			return p, err
		}
		if populated != 0 {
			return p, fmt.Errorf("%w: populated unsupported Issue field %s", ErrInvalidStore, column)
		}
	}
	quoted := make([]string, len(p.IssueColumns))
	for i, c := range p.IssueColumns {
		quoted[i] = "`" + c + "`"
	}
	var issueCount, depCount int
	if err := tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM issues),(SELECT COUNT(*) FROM dependencies)").Scan(&issueCount, &depCount); err != nil {
		return p, err
	}
	if issueCount+depCount > PreviewSnapshotLimit {
		return p, ErrLimitExceeded
	}
	for _, part := range []struct {
		query string
		ids   *[]string
	}{
		{"SELECT id FROM issues ORDER BY id", &p.IssueIDs},
		{"SELECT id FROM dependencies ORDER BY id", &p.DependencyIDs},
	} {
		rows, err := tx.QueryContext(ctx, part.query)
		if err != nil {
			return p, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return p, errors.Join(err, rows.Close())
			}
			*part.ids = append(*part.ids, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return p, err
		}
	}
	// Bound native payload acquisition before hydration and canonical validation.
	var nativeBytes uint64
	query := "SELECT CAST(COALESCE(SUM(256"
	for _, c := range quoted {
		query += "+COALESCE(OCTET_LENGTH(" + c + "),0)"
	}
	query += "),0) AS UNSIGNED) FROM issues"
	if err := tx.QueryRowContext(ctx, query).Scan(&nativeBytes); err != nil {
		return p, err
	}
	for _, query := range []string{
		"SELECT CAST(COALESCE(SUM(256+OCTET_LENGTH(text)+OCTET_LENGTH(author)),0) AS UNSIGNED) FROM comments",
		"SELECT CAST(COALESCE(SUM(256+OCTET_LENGTH(label)),0) AS UNSIGNED) FROM labels",
		"SELECT CAST(COALESCE(SUM(256+OCTET_LENGTH(metadata)+OCTET_LENGTH(thread_id)+OCTET_LENGTH(created_by)),0) AS UNSIGNED) FROM dependencies",
	} {
		var size uint64
		if err := tx.QueryRowContext(ctx, query).Scan(&size); err != nil {
			return p, err
		}
		nativeBytes += size
	}
	if len(p.IssueIDs)+len(p.DependencyIDs) > PreviewContinuityReadRowLimit || nativeBytes > PreviewCurrentReadByteLimit {
		return p, ErrLimitExceeded
	}
	var humanRows int
	prefix := txmemory.StorageKey("")
	code := txmemory.StorageKey("@knowledge/code/")
	catalog := txmemory.StorageKey("@knowledge/catalog")
	parts := txmemory.StorageKey("@knowledge/catalog-part/")
	activity := txmemory.StorageKey("@activity/")
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM config WHERE BINARY LEFT(`key`,CHAR_LENGTH(?))=BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? AND BINARY `key`<>BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ?", prefix, prefix, code, code, catalog, parts, parts, activity, activity).Scan(&humanRows); err != nil {
		return p, err
	}
	if humanRows > PreviewSnapshotLimit {
		return p, ErrLimitExceeded
	}
	var humanBytes uint64
	if err := tx.QueryRowContext(ctx, "SELECT CAST(COALESCE(SUM(256+OCTET_LENGTH(`key`)+OCTET_LENGTH(value)),0) AS UNSIGNED) FROM config WHERE BINARY LEFT(`key`,CHAR_LENGTH(?))=BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? AND BINARY `key`<>BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ?", prefix, prefix, code, code, catalog, parts, parts, activity, activity).Scan(&humanBytes); err != nil {
		return p, err
	}
	if humanBytes > PreviewCurrentReadByteLimit {
		return p, ErrLimitExceeded
	}
	p.IssueRowsSHA256, err = conversionRowsHash(ctx, tx, "SELECT "+strings.Join(quoted, ",")+" FROM issues ORDER BY id")
	if err != nil {
		return p, err
	}
	for _, id := range p.IssueIDs {
		if err := validatePath("beads/" + id); err != nil {
			return p, err
		}
		issue, err := issueops.GetIssueInTx(ctx, tx, id)
		if err != nil {
			return p, err
		}
		if issueops.IsWisp(issue) {
			return p, fmt.Errorf("%w: unsupported ephemeral/no-history Issue %s", ErrInvalidStore, id)
		}
		if _, err := canonicalJSON(issue); err != nil {
			return p, err
		}
		if err := validateConversionMetadata(issue.Metadata); err != nil {
			return p, fmt.Errorf("Issue %s: %w", id, err)
		}
	}
	for _, id := range p.DependencyIDs {
		if err := validateLinkPath("links/native-" + id); err != nil {
			return p, err
		}
		var source, target, typ, metadata, thread, actor string
		if err := tx.QueryRowContext(ctx, `SELECT issue_id,COALESCE(depends_on_issue_id,''),type,metadata,thread_id,created_by FROM dependencies WHERE id=? AND depends_on_wisp_id IS NULL AND depends_on_external IS NULL`, id).Scan(&source, &target, &typ, &metadata, &thread, &actor); err != nil {
			return p, fmt.Errorf("Dependency %s: %w", id, err)
		}
		if !slices.Contains(p.IssueIDs, source) || !slices.Contains(p.IssueIDs, target) || source == target || !utf8.ValidString(thread) || !utf8.ValidString(actor) {
			return p, fmt.Errorf("%w: Dependency %s has unsupported endpoints/fields", ErrInvalidStore, id)
		}
		if _, err := NativeDependencyTypeURL(o.Binding.ScopeURL, typ); err != nil {
			return p, err
		}
		if err := validateConversionMetadata([]byte(metadata)); err != nil {
			return p, fmt.Errorf("Dependency %s: %w", id, err)
		}
	}
	rows, err = tx.QueryContext(ctx, "SELECT `key`,value FROM config WHERE BINARY LEFT(`key`,CHAR_LENGTH(?))=BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? AND BINARY `key`<>BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? AND BINARY LEFT(`key`,CHAR_LENGTH(?))<>BINARY ? ORDER BY BINARY `key`", prefix, prefix, code, code, catalog, parts, parts, activity, activity)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return p, errors.Join(err, rows.Close())
		}
		key = strings.TrimPrefix(key, txmemory.StorageKey(""))
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			return p, errors.Join(fmt.Errorf("%w: non-UTF8 memory %q", ErrInvalidStore, key), rows.Close())
		}
		if _, err := memoryapi.ValidateKey(key); err != nil {
			return p, errors.Join(err, rows.Close())
		}
		if !derivedContinuityKey(key) {
			if slices.Contains(p.IssueIDs, "continuity-"+conversionHash([]byte(key))) {
				return p, errors.Join(fmt.Errorf("%w: native Issue collides with canonical memory identity", ErrConflict), rows.Close())
			}
			p.HumanMemories[key] = conversionHash([]byte(value))
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return p, err
	}
	p.Resources = len(p.IssueIDs) + len(p.DependencyIDs) + len(p.HumanMemories)
	if p.Resources > PreviewContinuityReadRowLimit {
		return p, ErrLimitExceeded
	}
	p.OtherConfigSHA256, err = conversionOtherConfigHash(ctx, tx, p.HumanMemories)
	return p, err
}

func conversionHash(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func conversionRowsHash(ctx context.Context, q schema.DBConn, query string) (string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	enc := json.NewEncoder(h)
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(values))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		if err := enc.Encode(values); err != nil {
			return "", err
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func conversionOtherConfigHash(ctx context.Context, q schema.DBConn, human map[string]string) (string, error) {
	return conversionConfigHashExcept(ctx, q, human, nil)
}

func conversionConfigHashExcept(ctx context.Context, q schema.DBConn, human, removed map[string]string) (string, error) {
	rows, err := q.QueryContext(ctx, "SELECT `key`, SHA2(value,256) FROM config ORDER BY BINARY `key`")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	h := sha256.New()
	enc := json.NewEncoder(h)
	seen := 0
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return "", err
		}
		if _, ok := human[strings.TrimPrefix(key, txmemory.StorageKey(""))]; ok && strings.HasPrefix(key, txmemory.StorageKey("")) {
			continue
		}
		if expected, ok := removed[strings.TrimPrefix(key, txmemory.StorageKey(""))]; ok && strings.HasPrefix(key, txmemory.StorageKey("")) {
			if !strings.HasPrefix(key, txmemory.StorageKey("@knowledge/code/blob/")) || value != expected {
				return "", errors.New("prune exclusion does not match the original code blob")
			}
			seen++
			continue
		}
		if err := enc.Encode([2]string{key, value}); err != nil {
			return "", err
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", err
	}
	if seen != len(removed) {
		return "", errors.New("prune exclusion names a missing original blob")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// AdoptConversionCandidate operates only on a separate restored data directory.
// DDL failure leaves that disposable candidate incomplete; the source is never edited.
func AdoptConversionCandidate(ctx context.Context, o Options, plan ConversionPlan, actor string) error {
	return adoptConversionCandidate(ctx, o, plan, actor, nil)
}

func adoptConversionCandidate(ctx context.Context, o Options, plan ConversionPlan, actor string, afterWrite func(string) error) (err error) {
	if err = validateConversionOptions(o); err != nil {
		return err
	}
	actual, e := filepath.EvalSymlinks(o.DataDir)
	if e != nil {
		return e
	}
	source, e := filepath.EvalSymlinks(plan.SourceDataDir)
	if e != nil {
		return e
	}
	if actual != o.DataDir || withinPath(actual, o.Binding.WorkspaceID) {
		return fmt.Errorf("%w: candidate must be canonical and outside final workspace", ErrInvalidStore)
	}
	if withinPath(actual, source) || withinPath(source, actual) {
		return fmt.Errorf("%w: candidate overlaps source", ErrInvalidStore)
	}
	if o.Binding != plan.Binding || o.Database != plan.Database || actor == "" || !utf8.ValidString(actor) {
		return fmt.Errorf("%w: candidate identity/operator differs from plan", ErrInvalidStore)
	}
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	connClosed := false
	defer func() {
		if !connClosed {
			err = errors.Join(err, conn.Close())
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	got, err := planConversionInTx(ctx, tx, o)
	rollbackErr := tx.Rollback()
	if err != nil || rollbackErr != nil {
		return errors.Join(err, rollbackErr)
	}
	got.SourceDataDir = plan.SourceDataDir
	if !reflect.DeepEqual(got, plan) {
		return fmt.Errorf("%w: restored candidate differs from plan", ErrConflict)
	}
	s.afterWrite = afterWrite
	if err = installPreviewSchema(ctx, conn, o, false); err != nil {
		return err
	}
	connClosed = true
	if err := conn.Close(); err != nil {
		return err
	}
	return s.withTxPolicy(ctx, txPolicy{mayWrite: true, commit: true, conversion: true}, func(tx *sql.Tx) error {
		for _, id := range plan.IssueIDs {
			token, err := freshToken()
			if err != nil {
				return err
			}
			var classification string
			if err := tx.QueryRowContext(ctx, "SELECT issue_type FROM issues WHERE id=?", id).Scan(&classification); err != nil {
				return err
			}
			typeURL, err := NativeIssueTypeURL(s.ScopeURL(), classification)
			if err != nil {
				return err
			}
			if err = s.ensureNativeTypeInTx(ctx, tx, typeURL); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog(path,resource_kind,type_url,revision,allocation_state,backing,backing_key) VALUES (?,'bead',?,?,'live','issue',?)`, "beads/"+id, typeURL, token, id); err != nil {
				return err
			}
		}
		for _, id := range plan.DependencyIDs {
			var typ string
			if err := tx.QueryRowContext(ctx, "SELECT type FROM dependencies WHERE id=?", id).Scan(&typ); err != nil {
				return err
			}
			typeURL, err := NativeDependencyTypeURL(s.ScopeURL(), typ)
			if err != nil {
				return err
			}
			token, err := freshToken()
			if err != nil {
				return err
			}
			if err = s.ensureNativeTypeInTx(ctx, tx, typeURL); err != nil {
				return err
			}
			path := "links/native-" + id
			if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog(path,resource_kind,type_url,revision,allocation_state,backing,backing_key) VALUES (?,'link',?,?,'live','dependency',?)`, path, typeURL, token, id); err != nil {
				return err
			}
			link, err := s.currentLinkForCreationInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			raw, err := canonicalJSON(link)
			if err != nil {
				return err
			}
			if err := insertPreviewVersionInTx(ctx, tx, path, token, raw, link.Attribution.Actor); err != nil {
				return err
			}
		}
		for _, id := range plan.IssueIDs {
			if err := issueops.AdoptVersionInTx(ctx, tx, id, actor); err != nil {
				return err
			}
			var revision string
			if err := tx.QueryRowContext(ctx, "SELECT revision FROM graph_preview_catalog WHERE path=?", "beads/"+id).Scan(&revision); err != nil {
				return err
			}
			owned, err := s.ownedLinksInTx(ctx, tx, id)
			if err != nil {
				return err
			}
			raw, err := canonicalJSON(owned)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_issue_versions(path,version,issue_id,issue_revision,owned) VALUES (?,?,?,1,?)`, "beads/"+id, revision, id, raw); err != nil {
				return err
			}
		}
		keys := make([]string, 0, len(plan.HumanMemories))
		for key := range plan.HumanMemories {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			value, found, err := txmemory.RecallInTx(ctx, tx, key)
			if err != nil {
				return err
			}
			if !found || conversionHash([]byte(value)) != plan.HumanMemories[key] {
				return fmt.Errorf("%w: memory %q differs from plan", ErrConflict, key)
			}
			token, err := freshToken()
			if err != nil {
				return err
			}
			path := "beads/continuity-" + conversionHash([]byte(key))
			if err := s.Memories(actor).createKeyedMemoryInTx(ctx, tx, key, value, path, token); err != nil {
				return err
			}
		}
		if err := s.afterStage("conversion-seeded"); err != nil {
			return err
		}
		if err := verifyConversionPreservation(ctx, tx, plan); err != nil {
			return err
		}
		for _, id := range plan.IssueIDs {
			if _, err := s.showIssueInTx(ctx, tx, "beads/"+id); err != nil {
				return err
			}
		}
		for _, id := range plan.DependencyIDs {
			if _, err := s.currentLinkInTx(ctx, tx, "links/native-"+id); err != nil {
				return err
			}
		}
		return s.validateTypeStateInTx(ctx, tx)
	})
}

func verifyConversionPreservation(ctx context.Context, tx *sql.Tx, plan ConversionPlan) error {
	state, err := conversionState(ctx, tx)
	if err != nil {
		return err
	}
	if state.Head != plan.State.Head || state.Staged != plan.State.Staged || !reflect.DeepEqual(state.Branches, plan.State.Branches) || !reflect.DeepEqual(state.Tags, plan.State.Tags) {
		return fmt.Errorf("%w: conversion changed committed/staged history", ErrInvalidStore)
	}
	for name, hash := range plan.State.Tables {
		if name != "issues" && name != "config" && state.Tables[name] != hash {
			return fmt.Errorf("%w: conversion changed table %s", ErrInvalidStore, name)
		}
	}
	quoted := make([]string, len(plan.IssueColumns))
	for i, c := range plan.IssueColumns {
		quoted[i] = "`" + c + "`"
	}
	hash, err := conversionRowsHash(ctx, tx, "SELECT "+strings.Join(quoted, ",")+" FROM issues ORDER BY id")
	if err != nil {
		return err
	}
	if hash != plan.IssueRowsSHA256 {
		return fmt.Errorf("%w: conversion changed original Issue fields", ErrInvalidStore)
	}
	hash, err = conversionOtherConfigHash(ctx, tx, plan.HumanMemories)
	if err != nil {
		return err
	}
	if hash != plan.OtherConfigSHA256 {
		return fmt.Errorf("%w: conversion changed derived/configuration rows", ErrInvalidStore)
	}
	return nil
}

// Native JSON columns may already have rounded an old unsafe integer. Adoption
// refuses that ambiguous precision range instead of renewing an exactness claim.
func validateConversionMetadata(raw json.RawMessage) error {
	canonical, err := commonMetadata(raw)
	if err != nil {
		return err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var visit func(any) error
	visit = func(value any) error {
		switch v := value.(type) {
		case json.Number:
			n, ok := new(big.Rat).SetString(string(v))
			if !ok {
				return fmt.Errorf("%w: invalid metadata number", ErrInvalidStore)
			}
			if n.IsInt() && n.Num().BitLen() > 53 {
				return fmt.Errorf("%w: native metadata integer exceeds the unambiguous safe range", ErrCapabilityUnavailable)
			}
		case map[string]any:
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(value)
}

// InspectConversionState reads fingerprints without provisioning or changing format.
func InspectConversionState(ctx context.Context, o Options) (state ConversionState, err error) {
	if err = validateConversionOptions(o); err != nil {
		return state, err
	}
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		return state, err
	}
	defer func() {
		err = errors.Join(err, s.Close())
		if err != nil {
			state = ConversionState{}
		}
	}()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return state, err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return state, err
	}
	defer func() { err = errors.Join(err, tx.Rollback()) }()
	return conversionState(ctx, tx)
}

// VerifyConversionCandidate validates native preservation and every human envelope.
func VerifyConversionCandidate(ctx context.Context, o Options, plan ConversionPlan) (state ConversionState, err error) {
	s, err := OpenExisting(ctx, o)
	if err != nil {
		return state, err
	}
	defer func() {
		err = errors.Join(err, s.Close())
		if err != nil {
			state = ConversionState{}
		}
	}()
	err = s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := verifyConversionPreservation(ctx, tx, plan); err != nil {
			return err
		}
		snapshot, err := s.currentSnapshotWithBoundsInTx(ctx, tx, PreviewContinuityReadRowLimit, PreviewContinuityReadByteLimit)
		if err != nil {
			return err
		}
		if len(snapshot.Records) != plan.Resources {
			return fmt.Errorf("%w: converted inventory differs from plan", ErrInvalidStore)
		}
		for key, hash := range plan.HumanMemories {
			value, found, err := s.Memories("").resolve(ctx, tx, key)
			if err != nil {
				return err
			}
			if !found || conversionHash([]byte(value)) != hash {
				return fmt.Errorf("%w: converted Memory %q differs from plan", ErrInvalidStore, key)
			}
		}
		state, err = conversionState(ctx, tx)
		return err
	})
	return state, err
}

// ConversionConfigAfterPrune proves exact preservation of every source config
// row except the explicitly listed obsolete code blobs. It never mutates either database.
func ConversionConfigAfterPrune(ctx context.Context, o Options, plan ConversionPlan, removed map[string]string) (hash string, err error) {
	if err := validateConversionOptions(o); err != nil {
		return "", err
	}
	if o.DataDir != plan.SourceDataDir || o.Database != plan.Database || o.Binding != plan.Binding {
		return "", ErrConflict
	}
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, tx.Rollback()) }()
	state, err := conversionState(ctx, tx)
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(state, plan.State) {
		return "", ErrConflict
	}
	return conversionConfigHashExcept(ctx, tx, plan.HumanMemories, removed)
}

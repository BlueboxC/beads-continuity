package graphstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// These tables are deliberately preview-specific. They share the standard
// Beads database; Issue and Dependency payloads are not copied into them.
// The catalog alone owns allocation and current revision. A later specialized
// Issue provider must participate in this same transaction/identity model.
var previewDDL = []string{
	`CREATE TABLE graph_preview_links (
        path VARBINARY(1024) PRIMARY KEY, source_path VARBINARY(1024) NOT NULL,
        target_path VARBINARY(1024) NOT NULL,
        source_revision VARBINARY(4096) NOT NULL DEFAULT '',
        target_revision VARBINARY(4096) NOT NULL DEFAULT '', properties LONGBLOB NOT NULL,
        metadata LONGBLOB NULL,
        attribution LONGBLOB NOT NULL, INDEX source_links (source_path))`,
	`CREATE TABLE graph_preview_scope (
        singleton INT PRIMARY KEY, workspace LONGBLOB NOT NULL,
        scope_url LONGBLOB NOT NULL, authority_id VARBINARY(32) NOT NULL,
        schema_version INT NOT NULL, writer_token VARBINARY(32) NOT NULL)`,
	`CREATE TABLE graph_preview_types (
        name VARBINARY(64) PRIMARY KEY, descriptor LONGBLOB NOT NULL,
        fingerprint VARBINARY(64) NOT NULL)`,
	`CREATE TABLE graph_preview_catalog (
        path VARBINARY(1024) PRIMARY KEY, resource_kind VARBINARY(16) NOT NULL,
        type_url LONGBLOB NOT NULL, revision VARBINARY(32) NOT NULL,
        allocation_state VARBINARY(16) NOT NULL, backing VARBINARY(32) NOT NULL,
        backing_key VARBINARY(255) NULL, public_id LONGBLOB NULL, UNIQUE KEY one_backing (backing, backing_key))`,
	`CREATE TABLE graph_preview_payloads (
        path VARBINARY(1024) PRIMARY KEY, properties LONGBLOB NOT NULL,
        metadata LONGBLOB NULL)`,
	`CREATE TABLE graph_preview_versions (
        path VARBINARY(1024) NOT NULL, version VARBINARY(32) NOT NULL,
        snapshot LONGBLOB NOT NULL, actor LONGBLOB NOT NULL,
        ordinal BIGINT NOT NULL, change_at DATETIME(6) NOT NULL, native_participation_generation BIGINT NULL, native_attribution LONGBLOB NULL,
        PRIMARY KEY (path, version), UNIQUE KEY one_path_ordinal (path, ordinal))`,
	`CREATE TABLE graph_preview_issue_versions (
        path VARBINARY(1024) NOT NULL, version VARBINARY(32) NOT NULL,
        issue_id VARBINARY(255) NOT NULL, issue_revision BIGINT NOT NULL, owned LONGBLOB NOT NULL, revision_context LONGBLOB NULL, history_ordinal BIGINT NULL,
        PRIMARY KEY (path, version), UNIQUE KEY one_issue_version (issue_id, issue_revision))`,
}

// MemoryTypeURL is the application-local identity of the selected Memory Type.
func MemoryTypeURL(scope string) string { return scope + "types/memory" }

func memoryDescriptor(scope string) (graph.TypeDescriptor, error) {
	return selectedMemoryDescriptor(scope, "memory")
}

// IssueTypeURL identifies the abstract native Issue family.
func IssueTypeURL(scope string) string { return scope + "types/issue" }

// DependencyTypeURL identifies the abstract native Dependency family.
func DependencyTypeURL(scope string) string { return scope + "types/dependency" }

func dependencyDescriptor(scope string) (graph.TypeDescriptor, error) {
	endpoint, err := graph.NewEndpointConstraint([]string{IssueTypeURL(scope)}, graph.ExternalNone)
	if err != nil {
		return graph.TypeDescriptor{}, err
	}
	return graph.NewTypeDescriptor(graph.TypeDescriptorSpec{
		ID: DependencyTypeURL(scope), Name: "Dependency", Describes: graph.KindLink,
		Description: "Abstract native Dependency family; cannot be declared directly. Concrete kinds refine this family and use live unpinned local Issue endpoints. The required dep_type string property mirrors the native Dependency kind; common metadata is editable; no other properties are supported. One ordered Issue pair has at most one Dependency.",
		Source:      &endpoint, Target: &endpoint,
	})
}

// The abstract native Issue family supplies inherited ownership. Its concrete
// children derive affiliation from the native writer's validated classification.
func issueDescriptor(scope string) (graph.TypeDescriptor, error) {
	owned := make([]graph.OwnedLinkDecl, 0, len(relationshipDependencyTypes))
	for _, relation := range relationshipDependencyTypes {
		link, err := graph.NewOwnedLinkDecl(scope+relation.path, "", PreviewOwnedLinkLimit)
		if err != nil {
			return graph.TypeDescriptor{}, err
		}
		owned = append(owned, link)
	}
	return graph.NewTypeDescriptor(graph.TypeDescriptorSpec{
		ID: IssueTypeURL(scope), Name: "Issue", Describes: graph.KindBead,
		Description:  "Abstract native Issue family; cannot be declared directly. Concrete native classifications refine this family and inherit source-owned Dependency Links. Native Issue writers own lifecycle and retained History; these Link Types do not define a close reason or authorize an edge-plus-close operation.",
		OwnsOutgoing: owned,
	})
}

type relationshipDependencyType struct {
	native      types.DependencyType
	installName string
	path        string
	name        string
}

// Kept temporarily for the old installer seam while the fresh generation's
// Type catalog is consolidated. Every valid kind uses one nominal Type.
var relationshipDependencyTypes = [...]relationshipDependencyType{
	{types.DepBlocks, "dependency", "types/dependency", "Dependency"},
}

func relationshipDependencyTypeURL(scope string, native types.DependencyType) (string, bool) {
	if !native.IsValid() {
		return "", false
	}
	id, err := NativeDependencyTypeURL(scope, string(native))
	return id, err == nil
}

// RelationshipDependencyTypeURL is an identity lookup, not admission permission.
func RelationshipDependencyTypeURL(scope string, native types.DependencyType) (string, bool) {
	return relationshipDependencyTypeURL(scope, native)
}
func RelationshipBlockingTypeURL(scope string) string {
	id, _ := relationshipDependencyTypeURL(scope, types.DepBlocks)
	return id
}
func isNativeRelationshipDependencyType(scope, id string) bool {
	base, kind, ok := NativeTypeClassification(scope, id)
	return ok && base == DependencyTypeURL(scope) && types.DependencyType(kind).IsValid()
}
func relationshipDependencyInstallName(scope, id string) (string, bool) {
	ok := isNativeRelationshipDependencyType(scope, id)
	return strings.TrimPrefix(id, scope+"types/"), ok
}
func relationshipDependencyDescriptor(scope string, native types.DependencyType) (graph.TypeDescriptor, error) {
	id, ok := relationshipDependencyTypeURL(scope, native)
	if !ok {
		return graph.TypeDescriptor{}, fmt.Errorf("unsupported native relationship Dependency %q", native)
	}
	return nativeConcreteDescriptor(scope, id)
}

// RelatedTypeURL retains the legacy command spelling as an alias for References.
// No separate Related Type is installed.
func RelatedTypeURL(scope string) string { return scope + "types/references" }

func relatedDescriptor(scope string) (graph.TypeDescriptor, error) {
	return selectedMemoryDescriptor(scope, "references")
}

func canonicalJSON(value any) ([]byte, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return graph.CanonicalizeJSON(b)
}

func freshToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func installPreview(ctx context.Context, conn *sql.Conn, o Options) error {
	return installPreviewSchema(ctx, conn, o, true)
}
func installPreviewSchema(ctx context.Context, conn *sql.Conn, o Options, fresh bool) (err error) {
	prefix := o.IssuePrefix
	if prefix == "" {
		prefix = "bd"
	}
	if !utf8.ValidString(prefix) || strings.TrimSpace(prefix) != prefix {
		return fmt.Errorf("%w: invalid Issue prefix", ErrInvalidStore)
	}
	for _, ddl := range graphNativeHistoryDDL {
		if _, err = conn.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	for _, ddl := range append(append([]string(nil), previewDDL...), originalHistoryDDL...) {
		if _, err = conn.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	token, err := freshToken()
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	finished := false
	defer func() {
		if !finished {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	b := o.Binding
	if _, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_scope
        (singleton, workspace, scope_url, authority_id, schema_version, writer_token)
        VALUES (1, ?, ?, ?, ?, ?)`, b.WorkspaceID, b.ScopeURL, b.AuthorityID, b.SchemaVersion, token); err != nil {
		return err
	}
	for _, definition := range previewTypeDefinitions() {
		descriptor, buildErr := definition.build(b.ScopeURL)
		if buildErr != nil {
			return buildErr
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_types (name, descriptor, fingerprint) VALUES (?, ?, ?)`, definition.name, descriptor.CanonicalJSON(), descriptor.Fingerprint()); err != nil {
			return err
		}
	}
	if fresh {
		if _, err = tx.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES ('issue_prefix', ?)", prefix); err != nil {
			return err
		}
	}
	if _, err = issueops.ReadConfigPrefix(ctx, tx); err != nil {
		return err
	}
	if err = checkBinding(ctx, tx, o); err != nil {
		return err
	}
	err = tx.Commit()
	finished = true
	if err != nil {
		return errors.Join(ErrOutcomeUnknown, err)
	}
	return nil
}

func checkBinding(ctx context.Context, tx *sql.Tx, o Options) error {
	if checkedReadBinding(tx, o) {
		return nil
	}
	var b Binding
	var token string
	err := tx.QueryRowContext(ctx, `SELECT workspace, scope_url, authority_id,
        schema_version, writer_token FROM graph_preview_scope WHERE singleton = 1
 AND OCTET_LENGTH(workspace)=? AND OCTET_LENGTH(scope_url)=?`, len(o.Binding.WorkspaceID), len(o.Binding.ScopeURL)).Scan(
		&b.WorkspaceID, &b.ScopeURL, &b.AuthorityID, &b.SchemaVersion, &token)
	if err != nil {
		return fmt.Errorf("%w: read binding: %v", ErrInvalidStore, err)
	}
	if b != o.Binding || !authorityID.MatchString(token) {
		return fmt.Errorf("%w: persisted binding differs from workspace", ErrInvalidStore)
	}
	installed, err := installedPreviewTypes(ctx, tx, b.ScopeURL)
	if err != nil {
		return err
	}
	count := len(installed)
	if count < len(previewTypeDefinitions()) {
		return fmt.Errorf("%w: workspace marker and installed Type generation differ", ErrInvalidStore)
	}
	if _, err := issueops.ReadConfigPrefix(ctx, tx); err != nil {
		return fmt.Errorf("%w: Issue configuration: %v", ErrInvalidStore, err)
	}
	// Reject incomplete bootstrap on open, even before a caller selects a row.
	for _, query := range []string{
		`SELECT path, resource_kind, type_url, revision, allocation_state, backing, backing_key, public_id FROM graph_preview_catalog LIMIT 0`,
		`SELECT path, properties, metadata FROM graph_preview_payloads LIMIT 0`,
		`SELECT path, source_path, target_path, source_revision, target_revision, properties, metadata, attribution FROM graph_preview_links LIMIT 0`,
		`SELECT path, version, snapshot, actor, ordinal, change_at, native_participation_generation, native_attribution FROM graph_preview_versions LIMIT 0`,
		`SELECT path, version, issue_id, issue_revision, owned, revision_context, history_ordinal FROM graph_preview_issue_versions LIMIT 0`,
		`SELECT issue_id, revision, durable_state, attribution_status FROM issue_versions LIMIT 0`,
	} {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("%w: incomplete schema: %v", ErrInvalidStore, err)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	rememberReadBinding(tx, o)
	return nil
}

var graphNativeHistoryDDL = []string{
	`CREATE TABLE issue_versions (
issue_id VARCHAR(255) NOT NULL, revision BIGINT NOT NULL, epoch INT NOT NULL,
durable_state LONGBLOB, change_actor VARCHAR(255), change_agent VARCHAR(255),
change_message TEXT, change_at DATETIME(6) NOT NULL, removed_at DATETIME(6),
removed_reason VARCHAR(255), removed_restriction VARCHAR(32), attribution_status VARCHAR(20) NOT NULL,
PRIMARY KEY (issue_id, revision))`,
	`CREATE TABLE store_epoch (id TINYINT NOT NULL DEFAULT 1, epoch INT NOT NULL DEFAULT 1,
bumped_at DATETIME, bumped_reason VARCHAR(255), PRIMARY KEY (id), CHECK (id = 1))`,
	`CREATE TABLE epoch_minted_addresses (address VARCHAR(255) NOT NULL, store_id VARCHAR(255) NOT NULL,
minted_id VARCHAR(255) NOT NULL, minted_epoch INT NOT NULL, minted_at DATETIME NOT NULL,
PRIMARY KEY (address))`,
	`ALTER TABLE issues ADD COLUMN current_revision BIGINT NOT NULL DEFAULT 1`,
	`ALTER TABLE issues ADD COLUMN participation_generation BIGINT NULL`,
}

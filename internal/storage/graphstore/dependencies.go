package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func validateResourcePath(path string) error {
	if strings.HasPrefix(path, "links/") {
		return validateLinkPath(path)
	}
	return validatePath(path)
}

func validateLinkPath(path string) error {
	if err := graph.ValidateLinkPath(path); err != nil {
		return err
	}
	if len(path) > 1024 {
		return fmt.Errorf("%w: preview Link path exceeds 1024 bytes", storage.ErrValidation)
	}
	return nil
}

// AddDependency asserts an ordinary Issue-domain native relation and its
// independently allocated canonical Link, atomically with source-owned History.
// Reasserting the same pair is an exact no-op; it never invokes the legacy
// same-pair metadata updater. This is not generic Link multiedge policy.
func (s *Store) AddDependency(ctx context.Context, request DependencyRequest) (DependencyResult, error) {
	if request.IDRoot != "" {
		if request.Path != "" || request.PublicID != "" {
			return DependencyResult{}, fmt.Errorf("%w: ID root cannot accompany an explicit ID", storage.ErrValidation)
		}
		if err := graph.ValidateGeneratedResourceIDRoot(request.IDRoot); err != nil {
			return DependencyResult{}, err
		}
	}
	preparedPath, generatedPath, err := prepareLinkIdentity(request.Path, request.PublicID)
	if err != nil {
		return DependencyResult{}, err
	}
	request.Path = preparedPath
	for _, endpoint := range []struct{ path, id string }{{request.SourcePath, request.SourceNativeID}, {request.TargetPath, request.TargetNativeID}} {
		if err := s.validateBeadEndpointOperand(endpoint.path, endpoint.id); err != nil {
			return DependencyResult{}, err
		}
	}
	if request.Path != "" {
		if err := validateLinkPath(request.Path); err != nil {
			return DependencyResult{}, err
		}
	}
	if request.Actor == "" || !utf8.ValidString(request.Actor) {
		return DependencyResult{}, fmt.Errorf("%w: Dependency requires an actor and two distinct local Issues", storage.ErrValidation)
	}
	nativeType, err := previewDependencyType(request.Type)
	if err != nil {
		return DependencyResult{}, err
	}
	metadata, err := commonMetadata(request.Metadata)
	if err != nil {
		return DependencyResult{}, err
	}
	if !utf8.ValidString(request.ThreadID) || len(request.ThreadID) > 255 {
		return DependencyResult{}, storage.ErrValidation
	}
	var result DependencyResult
	err = s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
		if err != nil {
			return err
		}
		linkType, ok := nativeDependencyTypeURLForIssueType(s.ScopeURL(), issueType, nativeType)
		if !ok || (request.TypeURL != "" && request.TypeURL != linkType) {
			return fmt.Errorf("%w: selected native Dependency Link Type is unavailable in this workspace generation", ErrCapabilityUnavailable)
		}
		if request.TypeURL != "" {
			if err := s.selectedLinkTypeInTx(ctx, tx, linkType); err != nil {
				return err
			}
		}
		sourcePath, source, err := s.resolveDependencyOperandInTx(ctx, tx, request.SourcePath, request.SourceNativeID)
		if err != nil {
			return err
		}
		if request.ExpectedSourceRevision != "" && request.ExpectedSourceRevision != source.Revision {
			return fmt.Errorf("%w: source revision changed (current %s)", ErrConflict, source.Revision)
		}
		targetPath, target, err := s.resolveDependencyOperandInTx(ctx, tx, request.TargetPath, request.TargetNativeID)
		if err != nil {
			return err
		}
		if sourcePath == targetPath {
			return fmt.Errorf("%w: Dependency requires two distinct local Issues", storage.ErrValidation)
		}
		for _, raw := range source.Owned {
			var existing LinkRecord
			if err := json.Unmarshal(raw, &existing); err != nil {
				return err
			}
			if existing.Target == target.ID {
				var thread string
				if err := tx.QueryRowContext(ctx, "SELECT thread_id FROM dependencies WHERE issue_id=? AND depends_on_issue_id=?", source.Properties.ID, target.Properties.ID).Scan(&thread); err != nil {
					return err
				}
				if existing.Type != linkType || existing.Properties["dep_type"] != string(nativeType) || !bytes.Equal(existing.Metadata, metadata) || thread != request.ThreadID {
					return fmt.Errorf("%w: Dependency pair already has a different kind", storage.ErrValidation)
				}
				if request.PublicID != "" && existing.ID != s.ScopeURL()+request.PublicID {
					return fmt.Errorf("%w: Dependency pair already has canonical Link %s", storage.ErrValidation, existing.ID)
				}
				if request.Path != "" && !generatedPath {
					allocated, err := s.publicResourcePathInTx(ctx, tx, existing.ID, graph.KindLink)
					if err != nil {
						return err
					}
					if allocated != request.Path {
						return fmt.Errorf("%w: Dependency pair already has canonical Link %s", storage.ErrValidation, existing.ID)
					}
				}
				result = DependencyResult{Link: existing, Source: source, Target: target, Changed: false}
				return nil
			}
		}
		if len(source.Owned) >= PreviewOwnedLinkLimit {
			return fmt.Errorf("%w: preview source-owned Link limit %d exceeded", storage.ErrValidation, PreviewOwnedLinkLimit)
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		path := request.Path
		publicID := request.PublicID
		if path == "" && publicID == "" {
			publicID, err = selectGeneratedPublicIDInTx(ctx, tx, request.IDRoot)
			if err != nil {
				return err
			}
			path, err = preferredPrivateResourcePath(graph.KindLink, publicID)
			if err != nil {
				return err
			}
			generatedPath = true
		}
		if publicID == "" {
			publicID = path
		}
		path, err = choosePrivateResourcePathInTx(ctx, tx, path, generatedPath, graph.KindLink)
		if err != nil {
			return err
		}
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		_, _, err = issueops.ExecuteAddDependenciesCheckedLocal(ctx, tx, publicops.AddDependenciesRequest{
			Actor: request.Actor, Edges: []publicops.DependencyEdge{{IssueID: source.Properties.ID, DependsOnID: target.Properties.ID, Type: nativeType}},
		})
		if err != nil {
			if errors.Is(err, publicops.ErrDependencyCycle) {
				return fmt.Errorf("%w: %w", storage.ErrValidation, err)
			}
			return err
		}
		if err := s.afterStage("dependency"); err != nil {
			return err
		}
		var key string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM dependencies WHERE issue_id=? AND depends_on_issue_id=?`, source.Properties.ID, target.Properties.ID).Scan(&key); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE dependencies SET metadata=?,thread_id=? WHERE id=?", string(metadata), request.ThreadID, key); err != nil {
			return err
		}
		if err := s.afterStage("dependency-fields"); err != nil {
			return err
		}
		revision, err := freshToken()
		if err != nil {
			return err
		}
		if err := s.ensureNativeTypeInTx(ctx, tx, linkType); err != nil {
			return err
		}
		// The first-use Type now reserves its own URL in this transaction.
		if exists, err := publicResourceIDExistsInTx(ctx, tx, publicID); err != nil {
			return err
		} else if exists {
			return ErrAlreadyExists
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog (path,resource_kind,type_url,revision,allocation_state,backing,backing_key,public_id) VALUES (?,'link',?,?,'live','dependency',?,?)`, path, linkType, revision, key, publicID); err != nil {
			return err
		}
		if err := s.afterStage("link-catalog"); err != nil {
			return err
		}
		link, err := s.currentLinkForCreationInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		snapshot, err := canonicalJSON(link)
		if err != nil {
			return err
		}
		if err := insertPreviewVersionInTx(ctx, tx, path, revision, snapshot, request.Actor); err != nil {
			return err
		}
		if err := s.afterStage("link-retained"); err != nil {
			return err
		}
		if err := s.recordIssueMappingInTx(ctx, tx, sourcePath, source.Properties.ID, request.Actor); err != nil {
			return err
		}
		changed, err := s.showIssueInTx(ctx, tx, sourcePath)
		if err != nil {
			return err
		}
		result = DependencyResult{Link: link, Source: changed, Target: target, Changed: true}
		return nil
	})
	if err != nil {
		return DependencyResult{}, err
	}
	return result, nil
}

// The native type column is the authority for the unconstrained dep_type
// property. Only the native column width is imposed; unfamiliar kinds have no
// scheduling or hierarchy effect.
func previewDependencyType(requested types.DependencyType) (types.DependencyType, error) {
	if requested == "" {
		requested = types.DepBlocks
	}
	if !requested.IsValid() || !utf8.ValidString(string(requested)) {
		return "", fmt.Errorf("%w: Dependency kind must be nonempty and at most %d bytes", storage.ErrValidation, types.MaxDependencyTypeLen)
	}
	return requested, nil
}

func (s *Store) requireIssueInTx(ctx context.Context, tx *sql.Tx, path string) (IssueRecord, error) {
	var backing string
	err := tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=?`, path).Scan(&backing)
	if errors.Is(err, sql.ErrNoRows) {
		return IssueRecord{}, ErrNotFound
	}
	if err != nil {
		return IssueRecord{}, err
	}
	if backing != "issue" {
		return IssueRecord{}, fmt.Errorf("%w: operation requires a live durable Issue, not %s", storage.ErrValidation, backing)
	}
	return s.showIssueInTx(ctx, tx, path)
}

// currentLinkInTx selects the authoritative specialized or generic Link row.
// A retained snapshot never substitutes for current authority.
func (s *Store) currentLinkInTx(ctx context.Context, tx *sql.Tx, path string) (LinkRecord, error) {
	return s.currentLinkProjectionInTx(ctx, tx, path, false)
}

// Creation callers explicitly permit the interval before retaining the first
// native version. Ordinary current readers never accept a missing head row.
func (s *Store) currentLinkForCreationInTx(ctx context.Context, tx *sql.Tx, path string) (LinkRecord, error) {
	return s.currentLinkProjectionInTx(ctx, tx, path, true)
}

func (s *Store) currentLinkProjectionInTx(ctx context.Context, tx *sql.Tx, path string, creating bool) (LinkRecord, error) {
	var selectedBacking string
	if err := tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=?`, path).Scan(&selectedBacking); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LinkRecord{}, ErrNotFound
		}
		return LinkRecord{}, err
	}
	var selectedState string
	if err := tx.QueryRowContext(ctx, `SELECT allocation_state FROM graph_preview_catalog WHERE path=?`, path).Scan(&selectedState); err != nil {
		return LinkRecord{}, err
	}
	if selectedState == "deleted" {
		return LinkRecord{}, s.deletedLinkErrorInTx(ctx, tx, path)
	}
	if selectedBacking == "informational" {
		return s.currentInformationalLinkInTx(ctx, tx, path)
	}

	var kind, typ, revision, state, backing, key string
	err := tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key FROM graph_preview_catalog WHERE path=?`, path).Scan(&kind, &typ, &revision, &state, &backing, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return LinkRecord{}, ErrNotFound
	}
	if err != nil {
		return LinkRecord{}, err
	}
	if kind != "link" || !authorityID.MatchString(revision) || state != "live" || backing != "dependency" || key == "" {
		return LinkRecord{}, fmt.Errorf("%w: invalid Dependency allocation", ErrInvalidStore)
	}
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return LinkRecord{}, err
	}
	var sourcePath, targetPath, depType, thread, actor string
	var metadata sql.NullString
	var at time.Time
	var nativeBytes uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(OCTET_LENGTH(metadata),0)+COALESCE(OCTET_LENGTH(created_by),0)+COALESCE(OCTET_LENGTH(thread_id),0)+256 FROM dependencies WHERE id=?`, key).Scan(&nativeBytes); err != nil {
		return LinkRecord{}, fmt.Errorf("%w: Dependency backing: %v", ErrInvalidStore, err)
	}
	if nativeBytes > PreviewCurrentReadByteLimit {
		return LinkRecord{}, fmt.Errorf("%w: native Dependency exceeds acquisition budget", ErrLimitExceeded)
	}
	err = tx.QueryRowContext(ctx, `SELECT src.path,dst.path,d.type,d.metadata,d.thread_id,d.created_by,d.created_at
 FROM dependencies d JOIN issues si ON si.id=d.issue_id JOIN issues ti ON ti.id=d.depends_on_issue_id JOIN graph_preview_catalog src ON src.backing='issue' AND src.backing_key=d.issue_id AND src.allocation_state='live' AND src.resource_kind='bead' AND src.type_url=CONCAT(?,LOWER(HEX(si.issue_type)))
 JOIN graph_preview_catalog dst ON dst.backing='issue' AND dst.backing_key=d.depends_on_issue_id AND dst.allocation_state='live' AND dst.resource_kind='bead' AND dst.type_url=CONCAT(?,LOWER(HEX(ti.issue_type)))
 WHERE d.id=? AND d.depends_on_wisp_id IS NULL AND d.depends_on_external IS NULL`, s.ScopeURL()+"types/native-issue/", s.ScopeURL()+"types/native-issue/", key).Scan(&sourcePath, &targetPath, &depType, &metadata, &thread, &actor, &at)
	if err != nil {
		return LinkRecord{}, fmt.Errorf("%w: Dependency backing: %v", ErrInvalidStore, err)
	}
	nativeType := types.DependencyType(depType)
	expectedType, ok := nativeDependencyTypeURLForIssueType(s.ScopeURL(), issueType, nativeType)
	if !ok || !utf8.ValidString(depType) || typ != expectedType || !utf8.ValidString(thread) {
		return LinkRecord{}, fmt.Errorf("%w: Dependency exceeds preview", ErrInvalidStore)
	}
	canonicalMetadata, err := commonMetadata([]byte(metadata.String))
	if err != nil {
		return LinkRecord{}, fmt.Errorf("%w: invalid native Dependency metadata: %v", ErrInvalidStore, err)
	}
	if err := s.requireNativeBindingInTx(ctx, tx, typ); err != nil {
		return LinkRecord{}, err
	}
	status := "unknown"
	if actor != "" {
		status = "claimed"
	}
	attribution := Attribution{Actor: actor, Status: status, RecordedAt: at.UTC().Format(time.RFC3339Nano)}
	attribution, err = nativeLinkAttributionInTx(ctx, tx, path, revision, attribution, creating)
	if err != nil {
		return LinkRecord{}, err
	}
	publicURL, err := s.publicResourceURLInTx(ctx, tx, path)
	if err != nil {
		return LinkRecord{}, err
	}
	sourceURL, err := s.publicResourceURLInTx(ctx, tx, sourcePath)
	if err != nil {
		return LinkRecord{}, err
	}
	targetURL, err := s.publicResourceURLInTx(ctx, tx, targetPath)
	if err != nil {
		return LinkRecord{}, err
	}
	return LinkRecord{ID: publicURL, Type: typ, Revision: revision, Version: revision,
		Source: sourceURL, Target: targetURL, Properties: nativeDependencyFacts(depType, key, thread), Metadata: canonicalMetadata,
		Attribution: attribution}, nil
}

// Concrete native kinds refine the abstract Dependency family.
func nativeDependencyTypeURLForIssueType(scope, issueType string, native types.DependencyType) (string, bool) {
	if (issueType == IssueTypeURL(scope) || isConcreteNativeIssue(scope, issueType)) && native.IsValid() {
		return relationshipDependencyTypeURL(scope, native)
	}
	return "", false
}
func isNativeDependencyLinkType(scope, issueType, linkType string) bool {
	return (issueType == IssueTypeURL(scope) || isConcreteNativeIssue(scope, issueType)) && isConcreteNativeDependency(scope, linkType)
}
func dependencyLinkTypesForIssueType(scope, issueType string) []string {
	if issueType != IssueTypeURL(scope) && !isConcreteNativeIssue(scope, issueType) {
		return nil
	}
	var ids []string
	for _, kind := range types.WellKnownDependencyTypes() {
		id, _ := NativeDependencyTypeURL(scope, string(kind))
		ids = append(ids, id)
	}
	return ids
}

func (s *Store) showLinkInTx(ctx context.Context, tx *sql.Tx, path string) (LinkRecord, error) {
	link, err := s.currentLinkInTx(ctx, tx, path)
	if err != nil {
		return LinkRecord{}, err
	}
	var retained []byte
	var actor string
	if err := tx.QueryRowContext(ctx, `SELECT snapshot,actor FROM graph_preview_versions WHERE path=? AND version=?`, path, link.Version).Scan(&retained, &actor); err != nil {
		return LinkRecord{}, fmt.Errorf("%w: missing Link retained state: %v", ErrInvalidStore, err)
	}
	canonical, err := canonicalJSON(link)
	if err != nil {
		return LinkRecord{}, err
	}
	if !bytes.Equal(canonical, retained) || actor != link.Attribution.Actor {
		return LinkRecord{}, fmt.Errorf("%w: current and retained Link state differ", ErrInvalidStore)
	}
	return link, nil
}

func (s *Store) ShowLink(ctx context.Context, path string) (LinkRecord, error) {
	if err := validateLinkPath(path); err != nil {
		return LinkRecord{}, err
	}
	var result LinkRecord
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		result, err = s.showLinkInTx(ctx, tx, path)
		return err
	})
	if err != nil {
		return LinkRecord{}, err
	}
	return result, nil
}

// ownedLinksInTx refuses an unmapped or unsupported domain edge rather than
// silently omitting it. Source snapshots store these complete canonical bytes.
func (s *Store) ownedLinksInTx(ctx context.Context, tx *sql.Tx, issueID string) ([]json.RawMessage, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.path FROM dependencies d LEFT JOIN graph_preview_catalog c ON c.backing='dependency' AND c.backing_key=d.id AND c.allocation_state='live' WHERE d.issue_id=? ORDER BY c.path`, issueID)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for rows.Next() {
		var path sql.NullString
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !path.Valid {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: unmapped outgoing Dependency", ErrInvalidStore)
		}
		paths = append(paths, path.String)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	var sourcePath string
	if err := tx.QueryRowContext(ctx, `SELECT path FROM graph_preview_catalog WHERE backing='issue' AND backing_key=? AND allocation_state='live'`, issueID).Scan(&sourcePath); err != nil {
		return nil, err
	}
	informational, err := s.informationalOwnedPathsInTx(ctx, tx, sourcePath)
	if err != nil {
		return nil, err
	}
	paths = append(paths, informational...)
	if len(paths) > PreviewOwnedLinkLimit {
		return nil, fmt.Errorf("%w: owned Link limit exceeded", ErrInvalidStore)
	}
	sort.Slice(paths, func(i, j int) bool { return graph.CompareCodeUnits(paths[i], paths[j]) < 0 })
	owned := make([]json.RawMessage, 0, len(paths))
	for _, path := range paths {
		link, err := s.showLinkInTx(ctx, tx, path)
		if err != nil {
			return nil, err
		}
		raw, err := canonicalJSON(link)
		if err != nil {
			return nil, err
		}
		owned = append(owned, raw)
	}
	return owned, nil
}

// recordIssueMappingInTx links the fresh Jim snapshot to opaque graph identity
// and captures the full canonical owned set. It stores no second Issue payload.
func (s *Store) recordIssueMappingInTx(ctx context.Context, tx *sql.Tx, path, issueID string, actors ...string) error {
	return s.recordIssueMappingWithContextInTx(ctx, tx, path, issueID, nil, actors...)
}

func (s *Store) recordIssueMappingWithContextInTx(ctx context.Context, tx *sql.Tx, path, issueID string, historical *HistoricalUpdateContext, actors ...string) error {
	previous, err := currentIssueRevisionContextInTx(ctx, tx, path)
	if err != nil {
		return err
	}
	previous.HistoricalUpdate = historical
	return s.recordIssueMappingContextInTx(ctx, tx, path, issueID, previous, actors...)
}

func (s *Store) recordIssueMappingContextInTx(ctx context.Context, tx *sql.Tx, path, issueID string, previous issueRevisionContext, actors ...string) error {
	var actor string
	if len(actors) > 0 {
		actor = actors[0]
	}
	var current, mapped int64
	if err := tx.QueryRowContext(ctx, "SELECT current_revision FROM issues WHERE id=?", issueID).Scan(&current); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(issue_revision),0) FROM graph_preview_issue_versions WHERE issue_id=?", issueID).Scan(&mapped); err != nil {
		return err
	}
	if current <= mapped || mapped == 0 {
		var err error
		if mapped == 0 {
			err = issueops.RecordVersionForCreateInTx(ctx, tx, issueID, actor)
		} else {
			err = issueops.RecordVersionInTx(ctx, tx, issueID, actor)
		}
		if err != nil {
			return err
		}
	}
	contextJSON, err := encodeIssueRevisionContext(previous)
	if err != nil {
		return err
	}
	// Bind absent context as SQL NULL, not a typed nil byte slice that may
	// become an empty blob. Empty bytes are malformed context, not absence.
	var storedContext any
	if contextJSON != nil {
		storedContext = contextJSON
	}
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT current_revision FROM issues WHERE id=?`, issueID).Scan(&ordinal); err != nil {
		return err
	}
	historyOrdinal, err := nextIssueHistoryOrdinalInTx(ctx, tx, path, ordinal)
	if err != nil {
		return err
	}
	revision, err := freshToken()
	if err != nil {
		return err
	}
	var classification string
	if err := tx.QueryRowContext(ctx, `SELECT issue_type FROM issues WHERE id=?`, issueID).Scan(&classification); err != nil {
		return err
	}
	declaredType, err := NativeIssueTypeURL(s.ScopeURL(), classification)
	if err != nil {
		return err
	}
	if err := s.ensureNativeTypeInTx(ctx, tx, declaredType); err != nil {
		return err
	}
	// Native mutation and its derived affiliation are one authority in this
	// transaction. Publish the new affiliation to internal ownership readers
	// before capturing Links; no partial state escapes a rollback.
	if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET type_url=? WHERE path=? AND backing='issue'`, declaredType, path); err != nil {
		return err
	}
	owned, err := s.ownedLinksInTx(ctx, tx, issueID)
	if err != nil {
		return err
	}
	snapshot, err := canonicalJSON(owned)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=? WHERE path=?`, revision, path); err != nil {
		return err
	}
	if err := s.afterStage("source-catalog"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_issue_versions (path,version,issue_id,issue_revision,owned,revision_context,history_ordinal) VALUES (?,?,?,?,?,?,?)`, path, revision, issueID, ordinal, snapshot, storedContext, historyOrdinal); err != nil {
		return err
	}
	return s.afterStage("source-retained")
}

package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/graphpatch"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// native_attribution is explicit current-head operation authority. Native
// properties and metadata always come from dependencies, never snapshot JSON.
// NULL is the initial creation projection; showLinkInTx separately requires the
// exact retained envelope to agree, so dropping an update override is corruption.
func nativeLinkAttributionInTx(ctx context.Context, tx *sql.Tx, path, revision string, creation Attribution, creating bool) (Attribution, error) {
	var size sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(native_attribution) FROM graph_preview_versions WHERE path=? AND version=?`, path, revision).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		if creating {
			return creation, nil
		}
		return Attribution{}, fmt.Errorf("%w: missing native Link current-head attribution mapping", ErrInvalidStore)
	}
	if err != nil {
		return Attribution{}, err
	}
	if !size.Valid {
		return creation, nil
	}
	if size.Int64 > PreviewCurrentReadByteLimit {
		return Attribution{}, fmt.Errorf("%w: native Link attribution exceeds acquisition budget", ErrLimitExceeded)
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT native_attribution FROM graph_preview_versions WHERE path=? AND version=?`, path, revision).Scan(&raw); err != nil {
		return Attribution{}, err
	}
	var value Attribution
	if json.Unmarshal(raw, &value) != nil || !sameVersionJSON(raw, value) || !validVersionAttribution(value, value.Actor) {
		return Attribution{}, fmt.Errorf("%w: malformed native Link attribution", ErrInvalidStore)
	}
	return value, nil
}

func nativeDependencyFacts(kind, id, thread string) map[string]any {
	return map[string]any{"dep_type": kind, "nativeId": id, "threadId": thread}
}

func nativeDependencyProperties(raw []byte) (types.DependencyType, error) {
	var properties map[string]any
	if json.Unmarshal(raw, &properties) != nil {
		return "", fmt.Errorf("%w: Dependency properties must be an object", storage.ErrValidation)
	}
	kind, ok := properties["dep_type"].(string)
	if !ok || kind == "" {
		return "", fmt.Errorf("%w: Dependency requires the dep_type string property", storage.ErrValidation)
	}
	for name, value := range properties {
		text, ok := value.(string)
		if !ok || !utf8.ValidString(text) || (name != "dep_type" && name != "nativeId" && name != "threadId") {
			return "", fmt.Errorf("%w: invalid Dependency fact %q", storage.ErrValidation, name)
		}
		if name == "threadId" && len(text) > 255 {
			return "", fmt.Errorf("%w: Dependency thread exceeds storage bound", storage.ErrValidation)
		}
	}
	return previewDependencyType(types.DependencyType(kind))
}

func (s *Store) writeNativeLinkPropertiesInTx(ctx context.Context, tx *sql.Tx, request LinkUpdateRequest, properties []byte, patch *graphpatch.Patch, current LinkRecord, terminal *LinkTombstone, head string) (LinkMutationResult, error) {
	if !isConcreteNativeDependency(s.ScopeURL(), current.Type) {
		return LinkMutationResult{}, fmt.Errorf("%w: native Link Type cannot be edited", ErrCapabilityUnavailable)
	}
	for _, pin := range []*EndpointPin{request.SourcePin, request.TargetPin} {
		if pin != nil && (pin.Current || pin.Revision != "") {
			return LinkMutationResult{}, fmt.Errorf("%w: native Dependencies require floating endpoints", storage.ErrValidation)
		}
	}
	if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, head, true, "Link"); err != nil {
		return LinkMutationResult{}, err
	}
	sourcePath, err := s.publicResourcePathInTx(ctx, tx, current.Source, graph.KindBead)
	if err != nil {
		return LinkMutationResult{}, err
	}
	targetPath, err := s.publicResourcePathInTx(ctx, tx, current.Target, graph.KindBead)
	if err != nil {
		return LinkMutationResult{}, err
	}
	source, err := s.requireIssueInTx(ctx, tx, sourcePath)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if err := checkRevisionGuard(request.ExpectedSourceRevision, request.UnconditionalSource, source.Revision, true, "source"); err != nil {
		return LinkMutationResult{}, err
	}
	target, err := s.requireIssueInTx(ctx, tx, targetPath)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if terminal != nil && len(source.Owned) >= PreviewOwnedLinkLimit {
		return LinkMutationResult{}, fmt.Errorf("%w: source-owned Link limit exceeded", storage.ErrValidation)
	}
	base := current
	if request.FromRevision != "" {
		base, err = s.historicalLinkBaseInTx(ctx, tx, request.Path, request.FromRevision, current)
		if err != nil {
			return LinkMutationResult{}, err
		}
	}
	before, err := canonicalJSON(current.Properties)
	if err != nil {
		return LinkMutationResult{}, err
	}
	next, err := canonicalJSON(base.Properties)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if properties != nil {
		if request.MergeProperties {
			var delta map[string]any
			if err := json.Unmarshal(properties, &delta); err != nil {
				return LinkMutationResult{}, err
			}
			merged := make(map[string]any, len(base.Properties)+len(delta))
			for k, v := range base.Properties {
				merged[k] = v
			}
			for k, v := range delta {
				merged[k] = v
			}
			next, err = canonicalJSON(merged)
		} else {
			next = properties
		}
	}
	if patch != nil {
		next, err = applyLinkPropertiesPatch(patch, next)
	}
	if err != nil {
		return LinkMutationResult{}, err
	}
	kind, err := nativeDependencyProperties(next)
	if err != nil {
		return LinkMutationResult{}, err
	}
	var requested map[string]any
	if err := json.Unmarshal(next, &requested); err != nil {
		return LinkMutationResult{}, err
	}
	for _, name := range []string{"nativeId", "threadId"} {
		if value, present := requested[name]; present && value != current.Properties[name] {
			return LinkMutationResult{}, fmt.Errorf("%w: Dependency %s is immutable", storage.ErrValidation, name)
		}
		requested[name] = current.Properties[name]
	}
	next, err = canonicalJSON(requested)
	if err != nil {
		return LinkMutationResult{}, err
	}
	// Native classification is part of the immutable concrete Type identity.
	// Editing metadata or restoring a prior revision cannot reclassify the Link.
	nextType, err := NativeDependencyTypeURL(s.ScopeURL(), string(kind))
	if err != nil {
		return LinkMutationResult{}, err
	}
	if nextType != current.Type {
		return LinkMutationResult{}, fmt.Errorf("%w: dep_type must preserve the declared native Dependency Type", storage.ErrValidation)
	}
	metadata, _, err := issueops.ApplyMetadataPatch(base.Metadata, request.Metadata)
	if err != nil {
		return LinkMutationResult{}, err
	}
	metadata, err = commonMetadata(metadata)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if terminal == nil && bytes.Equal(before, next) && bytes.Equal(current.Metadata, metadata) {
		return LinkMutationResult{Link: current, Source: source}, nil
	}
	if err := s.touchCoordination(ctx, tx); err != nil {
		return LinkMutationResult{}, err
	}
	unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
	defer unscope()
	if err := issueops.WriteGraphDependencyInTx(ctx, tx, types.Dependency{ID: current.Properties["nativeId"].(string), IssueID: source.Properties.ID, DependsOnID: target.Properties.ID, Type: kind, Metadata: string(metadata), ThreadID: current.Properties["threadId"].(string)}, request.Actor, terminal != nil); err != nil {
		var hierarchy *publicops.DependencyHierarchyConflictError
		if errors.Is(err, publicops.ErrDependencyCycle) || errors.As(err, &hierarchy) {
			return LinkMutationResult{}, fmt.Errorf("%w: %w", storage.ErrValidation, err)
		}
		return LinkMutationResult{}, err
	}
	if err := s.afterStage("dependency"); err != nil {
		return LinkMutationResult{}, err
	}
	var key string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM dependencies WHERE issue_id=? AND depends_on_issue_id=?`, source.Properties.ID, target.Properties.ID).Scan(&key); err != nil {
		return LinkMutationResult{}, fmt.Errorf("%w: native Dependency readback: %v", ErrInvalidStore, err)
	}
	revision, err := freshToken()
	if err != nil {
		return LinkMutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=?,allocation_state='live',backing_key=? WHERE path=?`, revision, key, request.Path); err != nil {
		return LinkMutationResult{}, err
	}
	if err := s.afterStage("link-catalog"); err != nil {
		return LinkMutationResult{}, err
	}
	attribution := writeAttribution(request.Actor)
	attribution.Inception = current.Attribution.Inception
	if terminal != nil {
		attribution.Inception = "revert"
	}
	if request.FromRevision != "" {
		attribution.HistoricalUpdate = &HistoricalUpdateContext{FromRevision: request.FromRevision, PreviousRevision: head}
	}
	link := current
	link.Revision, link.Version, link.Attribution, link.Metadata = revision, revision, attribution, metadata
	link.Properties = nativeDependencyFacts(string(kind), key, current.Properties["threadId"].(string))
	snapshot, err := canonicalJSON(link)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if err := insertPreviewVersionInTx(ctx, tx, request.Path, revision, snapshot, request.Actor); err != nil {
		return LinkMutationResult{}, err
	}
	contextJSON, err := canonicalJSON(attribution)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_versions SET native_attribution=? WHERE path=? AND version=?`, contextJSON, request.Path, revision); err != nil {
		return LinkMutationResult{}, err
	}
	if err := s.afterStage("link-retained"); err != nil {
		return LinkMutationResult{}, err
	}
	if err := s.recordIssueMappingInTx(ctx, tx, sourcePath, source.Properties.ID, request.Actor); err != nil {
		return LinkMutationResult{}, err
	}
	updated, err := s.showIssueInTx(ctx, tx, sourcePath)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
		return LinkMutationResult{}, err
	}
	if err := s.validateVersionLink(ctx, tx, link, snapshot, request.Actor); err != nil {
		return LinkMutationResult{}, err
	}
	return LinkMutationResult{Link: link, Source: updated, Changed: true, ReplacedSource: replacedMemory(source, request.UnconditionalSource)}, nil
}

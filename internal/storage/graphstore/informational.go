package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/graphpatch"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

func informationalProperties(properties map[string]any) ([]byte, error) {
	if properties == nil {
		properties = map[string]any{}
	}
	return canonicalJSON(properties)
}

func checkRevisionGuard(expected string, unconditional bool, current string, required bool, subject string) error {
	if (expected != "" && unconditional) || (required && expected == "" && !unconditional) {
		return fmt.Errorf("%w: %s requires exactly one revision guard or explicit unconditional choice", storage.ErrValidation, subject)
	}
	if expected != "" && expected != current {
		return fmt.Errorf("%w: %s revision changed (current %s)", ErrConflict, subject, current)
	}
	return nil
}

// beadEndpointInTx validates complete current/retained endpoint state. Memory
// ownership is checked separately from the selected source and Link Types.
func (s *Store) beadEndpointInTx(ctx context.Context, tx *sql.Tx, path string) (any, string, error) {
	var backing string
	err := tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=?`, path).Scan(&backing)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	switch backing {
	case "generic":
		r, err := s.showMemoryInTx(ctx, tx, path)
		return r, r.Revision, err
	case "issue":
		r, err := s.showIssueInTx(ctx, tx, path)
		return r, r.Revision, err
	default:
		return nil, "", fmt.Errorf("%w: informational endpoints must be live local Issue or Memory Beads", storage.ErrValidation)
	}
}

// AddInformationalLink creates independent identity even for equal endpoints.
// Link payload, retained Link and owned source snapshot commit together.
func (s *Store) AddInformationalLink(ctx context.Context, request LinkCreateRequest) (LinkMutationResult, error) {
	if request.IDRoot != "" {
		if request.Path != "" || request.PublicID != "" {
			return LinkMutationResult{}, fmt.Errorf("%w: ID root cannot accompany an explicit ID", storage.ErrValidation)
		}
		if err := graph.ValidateGeneratedResourceIDRoot(request.IDRoot); err != nil {
			return LinkMutationResult{}, err
		}
	}
	preparedPath, generatedPath, err := prepareLinkIdentity(request.Path, request.PublicID)
	if err != nil {
		return LinkMutationResult{}, err
	}
	request.Path = preparedPath
	if request.TypeURL == "" {
		request.TypeURL = RelatedTypeURL(s.ScopeURL())
	}
	if err := s.requireAssessmentAuthoring(request.TypeURL); err != nil {
		return LinkMutationResult{}, err
	}
	if !IsInformationalTypeURL(s.ScopeURL(), request.TypeURL) {
		return LinkMutationResult{}, fmt.Errorf("%w: informational Link Type is not supported", storage.ErrValidation)
	}
	if err := s.validateBeadEndpointOperand(request.SourcePath, request.SourceLocalID); err != nil {
		return LinkMutationResult{}, err
	}
	if err := s.validateBeadEndpointOperand(request.TargetPath, request.TargetLocalID); err != nil {
		return LinkMutationResult{}, err
	}
	if request.Path != "" {
		if err := validateLinkPath(request.Path); err != nil {
			return LinkMutationResult{}, err
		}
	}
	if !utf8.ValidString(request.Actor) {
		return LinkMutationResult{}, fmt.Errorf("%w: actor must be UTF-8", storage.ErrValidation)
	}
	properties, err := informationalProperties(request.Properties)
	if err != nil {
		return LinkMutationResult{}, err
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return LinkMutationResult{}, err
	}
	if err := catalog.validateProperties(request.TypeURL, properties, graph.KindLink); err != nil {
		return LinkMutationResult{}, err
	}
	metadata, err := commonMetadata(request.Metadata)
	if err != nil {
		return LinkMutationResult{}, err
	}
	var result LinkMutationResult
	err = s.withTypeValidationTx(ctx, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		request.SourcePath, err = s.resolveBeadEndpointOperandInTx(ctx, tx, request.SourcePath, request.SourceLocalID)
		if err != nil {
			return err
		}
		request.TargetPath, err = s.resolveBeadEndpointOperandInTx(ctx, tx, request.TargetPath, request.TargetLocalID)
		if err != nil {
			return err
		}
		if err := s.selectedLinkTypeInTx(ctx, tx, request.TypeURL); err != nil {
			return err
		}
		source, revision, err := s.beadEndpointInTx(ctx, tx, request.SourcePath)
		if err != nil {
			return err
		}
		owned, err := s.ownsLink(source, request.TypeURL)
		if err != nil {
			return err
		}
		if err := checkRevisionGuard(request.ExpectedSourceRevision, request.UnconditionalSource, revision, owned, "source"); err != nil {
			return err
		}
		// An owned Link write also authors the complete source Bead state.
		if owned {
			if err := s.requireAssessmentAuthoring(recordType(source)); err != nil {
				return err
			}
		}
		if _, _, err := s.beadEndpointInTx(ctx, tx, request.TargetPath); err != nil {
			return err
		}
		var pinBytes uint64
		sourcePin, err := s.endpointPinInTx(ctx, tx, request.SourcePath, request.SourcePin, &pinBytes)
		if err != nil {
			return err
		}
		targetPin, err := s.endpointPinInTx(ctx, tx, request.TargetPath, request.TargetPin, &pinBytes)
		if err != nil {
			return err
		}
		if owned && sourcePin != "" {
			return fmt.Errorf("%w: pinned owned source observation point is not selected", ErrCapabilityUnavailable)
		}

		if memory, ok := source.(Record); owned && ok && len(memory.Owned) >= PreviewOwnedLinkLimit {
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
		if exists, err := publicResourceIDExistsInTx(ctx, tx, publicID); err != nil {
			return err
		} else if exists {
			return ErrAlreadyExists
		}
		path, err = choosePrivateResourcePathInTx(ctx, tx, path, generatedPath, graph.KindLink)
		if err != nil {
			return err
		}
		linkRevision, err := freshToken()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog (path,resource_kind,type_url,revision,allocation_state,backing,public_id) VALUES (?,'link',?,?,'live','informational',?)`, path, request.TypeURL, linkRevision, publicID); err != nil {
			return err
		}
		if err := s.afterStage("link-catalog"); err != nil {
			return err
		}
		attribution, err := canonicalJSON(writeAttribution(request.Actor))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_links (path,source_path,target_path,source_revision,target_revision,properties,metadata,attribution) VALUES (?,?,?,?,?,?,?,?)`, path, request.SourcePath, request.TargetPath, sourcePin, targetPin, properties, metadata, attribution); err != nil {
			return err
		}
		if err := s.afterStage("link-payload"); err != nil {
			return err
		}
		result, err = s.finishInformationalWriteInTx(ctx, tx, path, request.SourcePath, request.Actor, source)
		if err == nil && owned {
			result.ReplacedSource = replacedMemory(source, request.UnconditionalSource)
		}
		return err
	})
	if err != nil {
		return LinkMutationResult{}, err
	}
	return result, nil
}

// UpdateLink replaces the closed descriptor properties and checks both guards
// before recognizing a no-op. Endpoint/Type edits have no writer in this API.
func (s *Store) UpdateLink(ctx context.Context, request LinkUpdateRequest) (LinkMutationResult, error) {
	if err := validateLinkPath(request.Path); err != nil {
		return LinkMutationResult{}, err
	}
	if !utf8.ValidString(request.Actor) {
		return LinkMutationResult{}, fmt.Errorf("%w: actor must be UTF-8", storage.ErrValidation)
	}
	if request.FromRevision != "" {
		if err := validateVersionToken(request.FromRevision); err != nil {
			return LinkMutationResult{}, err
		}
	}
	if request.MetadataOnly || (request.Properties == nil && (request.SourcePin != nil || request.TargetPin != nil || request.FromRevision != "")) {
		if request.FromRevision == "" && !hasCommonMetadataPatch(request.Metadata) && request.SourcePin == nil && request.TargetPin == nil {
			return LinkMutationResult{}, fmt.Errorf("%w: Link update requires metadata or properties", storage.ErrValidation)
		}
		if err := validateCommonMetadataPatch(request.Metadata); err != nil {
			return LinkMutationResult{}, err
		}
		return s.writeLinkProperties(ctx, request, nil, nil)
	}
	properties, err := informationalProperties(request.Properties)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if err := validateCommonMetadataPatch(request.Metadata); err != nil {
		return LinkMutationResult{}, err
	}
	return s.writeLinkProperties(ctx, request, properties, nil)
}

// writeLinkProperties is the single checked writer for replacement and ordered
// patch intents. Parsed patches own their input; replacement bytes are captured
// before entering the transaction. Both guards precede evaluation and no-op.
func (s *Store) writeLinkProperties(ctx context.Context, request LinkUpdateRequest, properties []byte, patch *graphpatch.Patch) (LinkMutationResult, error) {
	if patch != nil && properties != nil {
		return LinkMutationResult{}, fmt.Errorf("%w: Link replacement and ordered patch are mutually exclusive", storage.ErrValidation)
	}
	if request.MergeProperties && (properties == nil || patch != nil) {
		return LinkMutationResult{}, fmt.Errorf("%w: Link property merge requires one properties object", storage.ErrValidation)
	}
	if err := validateCommonMetadataPatch(request.Metadata); err != nil {
		return LinkMutationResult{}, err
	}
	var result LinkMutationResult
	err := s.withTypeValidationTx(ctx, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var terminal *LinkTombstone
		var link LinkRecord
		var head string
		var historicalBytes uint64
		if request.FromRevision != "" {
			var state string
			if err := tx.QueryRowContext(ctx, `SELECT allocation_state,revision FROM graph_preview_catalog WHERE path=?`, request.Path).Scan(&state, &head); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrNotFound
				}
				return err
			}
			if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, head, true, "Link"); err != nil {
				return err
			}
			var err error
			historicalBytes, err = historicalLinkReadBytes(ctx, tx, request.Path, head, request.FromRevision, state == "deleted")
			if err != nil {
				return err
			}
			if err := s.checkCurrentReadBytesAdditional(ctx, tx, historicalBytes); err != nil {
				return err
			}
			if state == "deleted" {
				link, terminal, err = s.deletedLinkStateInTx(ctx, tx, request.Path)
			} else {
				link, err = s.showLinkInTx(ctx, tx, request.Path)
			}
			if err != nil {
				return err
			}
		} else {
			var err error
			link, err = s.showLinkInTx(ctx, tx, request.Path)
			if err != nil {
				return err
			}
			head = link.Revision
		}
		if !IsInformationalTypeURL(s.ScopeURL(), link.Type) {
			var err error
			result, err = s.writeNativeLinkPropertiesInTx(ctx, tx, request, properties, patch, link, terminal, head)
			return err
		}
		if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, head, true, "Link"); err != nil {
			return err
		}
		sourcePath, err := s.publicResourcePathInTx(ctx, tx, link.Source, graph.KindBead)
		if err != nil {
			return err
		}
		targetPath, err := s.publicResourcePathInTx(ctx, tx, link.Target, graph.KindBead)
		if err != nil {
			return err
		}
		source, revision, err := s.beadEndpointInTx(ctx, tx, sourcePath)
		if err != nil {
			return err
		}
		owned, err := s.ownsLink(source, link.Type)
		if err != nil {
			return err
		}
		if err := checkRevisionGuard(request.ExpectedSourceRevision, request.UnconditionalSource, revision, owned, "source"); err != nil {
			return err
		}
		// An owned Link write also authors the complete source Bead state.
		if owned {
			if err := s.requireAssessmentAuthoring(recordType(source)); err != nil {
				return err
			}
		}

		if err := s.requireAssessmentAuthoring(link.Type); err != nil {
			return err
		}

		base := link
		if request.FromRevision != "" {
			base, err = s.historicalLinkBaseInTx(ctx, tx, request.Path, request.FromRevision, link)
			if err != nil {
				return err
			}
		}
		if terminal != nil {
			if _, _, err := s.beadEndpointInTx(ctx, tx, targetPath); err != nil {
				return err
			}
			if memory, ok := source.(Record); owned && ok && len(memory.Owned) >= PreviewOwnedLinkLimit {
				return fmt.Errorf("%w: preview source-owned Link limit %d exceeded", storage.ErrValidation, PreviewOwnedLinkLimit)
			}
		}
		pinBytes := historicalBytes
		sourcePin, targetPin := link.SourceRevision, link.TargetRevision
		if request.SourcePin != nil {
			sourcePin, err = s.endpointPinInTxBudget(ctx, tx, sourcePath, *request.SourcePin, &pinBytes, request.FromRevision != "")
			if err != nil {
				return err
			}
		}
		if request.TargetPin != nil {
			targetPin, err = s.endpointPinInTxBudget(ctx, tx, targetPath, *request.TargetPin, &pinBytes, request.FromRevision != "")
			if err != nil {
				return err
			}
		}
		if owned && sourcePin != "" {
			return fmt.Errorf("%w: pinned owned source observation point is not selected", ErrCapabilityUnavailable)
		}
		pinsChanged := sourcePin != link.SourceRevision || targetPin != link.TargetRevision
		if patch != nil {
			if note, ok := base.Properties["note"].(string); ok && len(note) > graphpatch.MaxDocumentBytes {
				return fmt.Errorf("%w: Link properties exceed the patch working-document limit", ErrLimitExceeded)
			}
		}
		before, err := canonicalJSON(link.Properties)
		if err != nil {
			return err
		}
		baseProperties, err := canonicalJSON(base.Properties)
		if err != nil {
			return err
		}
		nextProperties := properties
		if request.MergeProperties {
			var delta map[string]any
			if err := json.Unmarshal(properties, &delta); err != nil {
				return err
			}
			merged := make(map[string]any, len(base.Properties)+len(delta))
			for key, value := range base.Properties {
				merged[key] = value
			}
			for key, value := range delta {
				merged[key] = value
			}
			nextProperties, err = informationalProperties(merged)
			if err != nil {
				return err
			}
		}
		if nextProperties == nil && patch == nil {
			nextProperties = baseProperties
		}
		if patch != nil {
			nextProperties, err = applyLinkPropertiesPatch(patch, baseProperties)
			if err != nil {
				return err
			}
		}
		catalog, err := s.selectedTypes()
		if err != nil {
			return err
		}
		if err := catalog.validateProperties(link.Type, nextProperties, graph.KindLink); err != nil {
			return err
		}
		metadata, _, err := issueops.ApplyMetadataPatch(base.Metadata, request.Metadata)
		if err != nil {
			return err
		}
		metadata, err = commonMetadata(metadata)
		if err != nil {
			return err
		}
		if terminal == nil && bytes.Equal(before, nextProperties) && bytes.Equal(link.Metadata, metadata) && !pinsChanged {
			result = LinkMutationResult{Link: link, Source: source}
			return nil
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		linkRevision, err := freshToken()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=?,allocation_state='live' WHERE path=?`, linkRevision, request.Path); err != nil {
			return err
		}
		if err := s.afterStage("link-catalog"); err != nil {
			return err
		}
		acceptedAttribution := writeAttribution(request.Actor)
		acceptedAttribution.Inception = link.Attribution.Inception
		if terminal != nil {
			acceptedAttribution.Inception = "revert"
		}
		if request.FromRevision != "" {
			acceptedAttribution.HistoricalUpdate = &HistoricalUpdateContext{FromRevision: request.FromRevision, PreviousRevision: head}
		}
		attribution, err := canonicalJSON(acceptedAttribution)
		if err != nil {
			return err
		}
		if terminal != nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_links (path,source_path,target_path,source_revision,target_revision,properties,metadata,attribution) VALUES (?,?,?,?,?,?,?,?)`, request.Path, sourcePath, targetPath, sourcePin, targetPin, nextProperties, metadata, attribution)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE graph_preview_links SET properties=?, metadata=?, attribution=?, source_revision=?, target_revision=? WHERE path=?`, nextProperties, metadata, attribution, sourcePin, targetPin, request.Path)
		}
		if err != nil {
			return err
		}
		if err := s.afterStage("link-payload"); err != nil {
			return err
		}
		result, err = s.finishInformationalWriteInTx(ctx, tx, request.Path, sourcePath, request.Actor, source)
		if err == nil {
			if owned {
				result.ReplacedSource = replacedMemory(source, request.UnconditionalSource)
			}
			if request.FromRevision != "" || patch != nil || request.MergeProperties || hasCommonMetadataPatch(request.Metadata) || pinsChanged {
				// The new patch route must not commit a graph that current reads
				// cannot acquire. Replacement retains its existing policy.
				return s.checkCurrentReadBytes(ctx, tx)
			}
		}
		return err
	})
	if err != nil {
		return LinkMutationResult{}, err
	}
	return result, nil
}

func writeAttribution(actor string) Attribution {
	status := "unknown"
	if actor != "" {
		status = "claimed"
	}
	return Attribution{Actor: actor, Status: status, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}

func (s *Store) finishInformationalWriteInTx(ctx context.Context, tx *sql.Tx, path, sourcePath, actor string, source any) (LinkMutationResult, error) {
	link, err := s.currentInformationalLinkInTx(ctx, tx, path)
	if err != nil {
		return LinkMutationResult{}, err
	}
	snapshot, err := canonicalJSON(link)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if err := insertPreviewVersionInTx(ctx, tx, path, link.Version, snapshot, actor); err != nil {
		return LinkMutationResult{}, err
	}
	if err := s.afterStage("link-retained"); err != nil {
		return LinkMutationResult{}, err
	}
	owned, err := s.ownsLink(source, link.Type)
	if err != nil {
		return LinkMutationResult{}, err
	}
	if owned {
		source, err = s.recordOwnedMemoryInTx(ctx, tx, sourcePath, actor, source)
	}
	if err != nil {
		return LinkMutationResult{}, err
	}
	return LinkMutationResult{Link: link, Source: source, Changed: true}, nil
}

func (s *Store) recordOwnedMemoryInTx(ctx context.Context, tx *sql.Tx, sourcePath, actor string, source any) (any, error) {
	return s.recordOwnedMemoryWithContextInTx(ctx, tx, sourcePath, actor, source, nil)
}

func (s *Store) recordOwnedMemoryWithContextInTx(ctx context.Context, tx *sql.Tx, sourcePath, actor string, source any, historical *HistoricalUpdateContext) (any, error) {
	var err error
	if issue, ok := source.(IssueRecord); ok {
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		if err := issueops.RecordVersionInTx(ctx, tx, issue.Properties.ID, actor); err != nil {
			return nil, err
		}
		if err := s.recordIssueMappingInTx(ctx, tx, sourcePath, issue.Properties.ID, actor); err != nil {
			return nil, err
		}
		return s.showIssueInTx(ctx, tx, sourcePath)
	}
	if memory, ok := source.(Record); ok {
		memory.Owned, err = s.memoryOwnedLinksInTx(ctx, tx, sourcePath)
		if err != nil {
			return nil, err
		}
		memory.Revision, err = freshToken()
		if err != nil {
			return nil, err
		}
		memory.Version = memory.Revision
		inception := memory.Attribution.Inception
		memory.Attribution = writeAttribution(actor)
		memory.Attribution.Inception = inception
		memory.Attribution.HistoricalUpdate = historical
		if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=? WHERE path=?`, memory.Revision, sourcePath); err != nil {
			return nil, err
		}
		if err := s.afterStage("source-catalog"); err != nil {
			return nil, err
		}
		snapshot, err := canonicalJSON(memory)
		if err != nil {
			return nil, err
		}
		if err := insertPreviewVersionInTx(ctx, tx, sourcePath, memory.Version, snapshot, actor); err != nil {
			return nil, err
		}
		if err := s.afterStage("source-retained"); err != nil {
			return nil, err
		}
		source, err = s.showMemoryInTx(ctx, tx, sourcePath)
		if err != nil {
			return nil, err
		}
	}
	return source, nil
}

func (s *Store) currentInformationalLinkInTx(ctx context.Context, tx *sql.Tx, path string) (LinkRecord, error) {
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return LinkRecord{}, err
	}
	var kind, typ, revision, state, backing string
	var key sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key FROM graph_preview_catalog WHERE path=?`, path).Scan(&kind, &typ, &revision, &state, &backing, &key); err != nil {
		return LinkRecord{}, err
	}
	if kind != "link" || !IsInformationalTypeURL(s.ScopeURL(), typ) || !authorityID.MatchString(revision) || state != "live" || backing != "informational" || key.Valid {
		return LinkRecord{}, fmt.Errorf("%w: invalid informational Link allocation", ErrInvalidStore)
	}
	if err := s.informationalTypeInTx(ctx, tx, typ); err != nil {
		return LinkRecord{}, err
	}
	var source, target, sourcePin, targetPin string
	var properties, metadata, attribution []byte
	if err := tx.QueryRowContext(ctx, `SELECT source_path,target_path,source_revision,target_revision,properties,metadata,attribution FROM graph_preview_links WHERE path=?`, path).Scan(&source, &target, &sourcePin, &targetPin, &properties, &metadata, &attribution); err != nil {
		return LinkRecord{}, fmt.Errorf("%w: missing informational backing: %v", ErrInvalidStore, err)
	}
	for index, endpoint := range []string{source, target} {
		var kind, typ, state, backing string
		if err := tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,allocation_state,backing FROM graph_preview_catalog WHERE path=?`, endpoint).Scan(&kind, &typ, &state, &backing); err != nil {
			return LinkRecord{}, fmt.Errorf("%w: missing endpoint: %v", ErrInvalidStore, err)
		}
		if validatePath(endpoint) != nil || kind != "bead" || state != "live" || !((backing == "generic" && s.isMemoryType(typ)) || (backing == "issue" && nativeIssueTypeMatches(s.ScopeURL(), typ, issueType))) {
			return LinkRecord{}, fmt.Errorf("%w: invalid informational endpoint", ErrInvalidStore)
		}
		pin := []string{sourcePin, targetPin}[index]
		if pin != "" {
			if err := validateVersionToken(pin); err != nil {
				return LinkRecord{}, fmt.Errorf("%w: invalid persisted endpoint pin", ErrInvalidStore)
			}
			// A Link read returns its stored Reference, not the endpoint's
			// content. Exact availability is checked by admission/resolution;
			// retained endpoint payload is acquired only by explicit lookup or
			// complete-state Type validation, with its own cumulative budget.
		}

	}
	publicURL, err := s.publicResourceURLInTx(ctx, tx, path)
	if err != nil {
		return LinkRecord{}, err
	}
	sourceURL, err := s.publicResourceURLInTx(ctx, tx, source)
	if err != nil {
		return LinkRecord{}, err
	}
	targetURL, err := s.publicResourceURLInTx(ctx, tx, target)
	if err != nil {
		return LinkRecord{}, err
	}
	r := LinkRecord{ID: publicURL, Type: typ, Revision: revision, Version: revision, Source: sourceURL, Target: targetURL, SourceRevision: sourcePin, TargetRevision: targetPin}
	canonicalMetadata, err := commonMetadata(metadata)
	if err != nil || !bytes.Equal(canonicalMetadata, metadata) {
		return LinkRecord{}, fmt.Errorf("%w: invalid informational metadata", ErrInvalidStore)
	}
	r.Metadata = canonicalMetadata
	if json.Unmarshal(properties, &r.Properties) != nil || json.Unmarshal(attribution, &r.Attribution) != nil {
		return LinkRecord{}, fmt.Errorf("%w: malformed informational payload", ErrInvalidStore)
	}
	p, err := informationalProperties(r.Properties)
	if err != nil || !bytes.Equal(p, properties) {
		return LinkRecord{}, fmt.Errorf("%w: invalid informational properties", ErrInvalidStore)
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return LinkRecord{}, err
	}
	if err := catalog.validateProperties(r.Type, p, graph.KindLink); err != nil {
		return LinkRecord{}, fmt.Errorf("%w: Link payload violates selected Type: %v", ErrInvalidStore, err)
	}
	a, err := canonicalJSON(r.Attribution)
	if err != nil || !bytes.Equal(a, attribution) {
		return LinkRecord{}, fmt.Errorf("%w: invalid informational attribution", ErrInvalidStore)
	}
	at, err := time.Parse(time.RFC3339Nano, r.Attribution.RecordedAt)
	if err != nil || at.UTC().Format(time.RFC3339Nano) != r.Attribution.RecordedAt || !utf8.ValidString(r.Attribution.Actor) || (r.Attribution.Actor == "" && r.Attribution.Status != "unknown") || (r.Attribution.Actor != "" && r.Attribution.Status != "claimed") {
		return LinkRecord{}, fmt.Errorf("%w: invalid informational attribution", ErrInvalidStore)
	}
	return r, nil
}

func (s *Store) memoryOwnedLinksInTx(ctx context.Context, tx *sql.Tx, sourcePath string) ([]json.RawMessage, error) {
	paths, err := s.informationalOwnedPathsInTx(ctx, tx, sourcePath)
	if err != nil {
		return nil, err
	}
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

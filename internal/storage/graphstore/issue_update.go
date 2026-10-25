package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// UpdateIssueRequest admits existing Issue scalar edits, notes replacement and append.
// Nil fields preserve their properties. Empty scalar strings clear fields where
// the Issue domain permits; AppendNotes instead preserves native append semantics:
// empty on empty is a no-op, while empty on nonempty appends one newline.
// Notes replacement uses the native writer's overwrite fence; clear is an
// explicit CLI intent, not a special storage operation.
// EstimatedMinutes nil preserves the nullable estimate; zero sets a present zero.
// ClearEstimatedMinutes explicitly clears it to null. The existing Issue validator and SQL column
// retain their ordinary bounds; no scheduler or duration interpretation is added.
// ExternalRef and SpecID preserve literal values; empty clears the external
// reference to NULL and the spec ID to an empty string, as in ordinary update.
// DueAt distinguishes omission from nullable clear. Present dates use the existing
// whole-second storage representation before guarded no-op comparison.
// Priority zero sets P0; nil preserves priority. The guard addresses the complete
// graph revision, including owned blocking Dependencies, not the storage ordinal.
// Assignee nil preserves its value; empty clears it. Neither the graph guard nor
// Unconditional bypasses the ordinary active-assignment transfer fence.
type UpdateIssueRequest struct {
	Path, Actor, ExpectedRevision                             string
	TypeURL                                                   string
	IssueType                                                 *types.IssueType
	FromRevision                                              string
	PropertiesPatch                                           []byte
	NativeID                                                  string
	Unconditional, ForceNotesOverwrite, ForceAssigneeTransfer bool
	PropertiesProvided, ClearEstimatedMinutes                 bool
	Title, Description, Design, AcceptanceCriteria            *string
	Priority                                                  *int
	EstimatedMinutes                                          *int
	Assignee                                                  *string
	Notes, AppendNotes                                        *string
	ExternalRef, SpecID                                       *string
	DueAt                                                     publicops.Field[*time.Time]
	Metadata                                                  publicops.MetadataPatch
}

// UpdateIssue delegates admitted scalar edits to the existing Issue domain writer and
// retained-version recorder. Payload, opaque graph revision and complete owned
// state commit together; this adds no public History ordering or change context.
func (s *Store) UpdateIssue(ctx context.Context, request UpdateIssueRequest) (IssueMutationResult, error) {
	if err := validateIssueSelector(IssueSelector{Path: request.Path, NativeID: request.NativeID}); err != nil {
		return IssueMutationResult{}, fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	if request.FromRevision != "" {
		if err := validateVersionToken(request.FromRevision); err != nil {
			return IssueMutationResult{}, err
		}
	}
	if request.Actor == "" || !utf8.ValidString(request.Actor) {
		return IssueMutationResult{}, fmt.Errorf("%w: Issue update requires a nonempty UTF-8 actor", storage.ErrValidation)
	}
	patch := publicops.IssuePatch{}
	count := 0
	for _, field := range []struct {
		name  string
		value *string
		dest  *publicops.Field[string]
	}{
		{"title", request.Title, &patch.Title},
		{"assignee", request.Assignee, &patch.Assignee},
		{"description", request.Description, &patch.Description},
		{"design", request.Design, &patch.Design},
		{"acceptance_criteria", request.AcceptanceCriteria, &patch.AcceptanceCriteria},
		{"notes", request.Notes, &patch.Notes},
		{"append_notes", request.AppendNotes, &patch.AppendNotes},
	} {
		if field.value == nil {
			continue
		}
		if !utf8.ValidString(*field.value) {
			return IssueMutationResult{}, fmt.Errorf("%w: Issue %s must be UTF-8", storage.ErrValidation, field.name)
		}
		*field.dest = publicops.Field[string]{Set: true, Value: *field.value}
		count++
	}
	if patch.Assignee.Set {
		if err := types.CheckFieldLen("assignee", patch.Assignee.Value); err != nil {
			return IssueMutationResult{}, fmt.Errorf("%w: Issue assignee: %w", storage.ErrValidation, err)
		}
	}
	if request.TypeURL != "" {
		base, classification, ok := NativeTypeClassification(s.ScopeURL(), request.TypeURL)
		if !ok || base != IssueTypeURL(s.ScopeURL()) {
			return IssueMutationResult{}, fmt.Errorf("%w: native reclassification requires a concrete Issue Type", storage.ErrValidation)
		}
		native := types.IssueType(classification)
		if native.Normalize() != native {
			return IssueMutationResult{}, fmt.Errorf("%w: concrete Issue Type must use its normalized native classification", storage.ErrValidation)
		}
		if request.IssueType != nil && request.IssueType.Normalize() != native {
			return IssueMutationResult{}, fmt.Errorf("%w: declared Type contradicts native classification", storage.ErrValidation)
		}
		request.IssueType = &native
	}
	if request.IssueType != nil {
		patch.IssueType = publicops.Field[publicops.IssueType]{Set: true, Value: request.IssueType.Normalize()}
		count++
	}
	if request.Priority != nil {
		patch.Priority = publicops.Field[int]{Set: true, Value: *request.Priority}
		count++
	}
	if request.EstimatedMinutes != nil && request.ClearEstimatedMinutes {
		return IssueMutationResult{}, fmt.Errorf("%w: Issue estimate cannot be set and cleared together", storage.ErrValidation)
	}
	if request.EstimatedMinutes != nil {
		value := *request.EstimatedMinutes
		// The existing Issue column is a signed SQL INT on both backends. Reject
		// unrepresentable input before SQL so strict and coercing engines agree.
		if err := validateIssueEstimateStorage(&value); err != nil {
			return IssueMutationResult{}, err
		}
		patch.EstimatedMinutes = publicops.Field[*int]{Set: true, Value: &value}
		count++
	} else if request.ClearEstimatedMinutes {
		patch.EstimatedMinutes = publicops.Field[*int]{Set: true}
		count++
	}
	// These limits describe the existing VARCHAR columns, not reference syntax.
	// Validate before SQL so strict and coercing engines both refuse data loss.
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"external_ref", request.ExternalRef},
		{"spec_id", request.SpecID},
	} {
		if field.value == nil {
			continue
		}
		value := *field.value
		if err := validateIssueReferenceStorage(field.name, value); err != nil {
			return IssueMutationResult{}, err
		}
		if field.name == "external_ref" {
			patch.ExternalRef = publicops.Field[*string]{Set: true}
			if value != "" {
				patch.ExternalRef.Value = &value
			}
		} else {
			patch.SpecID = publicops.Field[string]{Set: true, Value: value}
		}
		count++
	}
	if request.DueAt.Set {
		value, err := normalizeIssueDue(request.DueAt.Value)
		if err != nil {
			return IssueMutationResult{}, err
		}
		patch.DueAt = publicops.Field[*time.Time]{Set: true, Value: value}
		count++
	}
	scalarCount := count
	if len(request.PropertiesPatch) > 0 && (request.FromRevision == "" || scalarCount > 0) {
		return IssueMutationResult{}, fmt.Errorf("%w: native property operations require a historical source and cannot combine with scalar setters", storage.ErrValidation)
	}
	if err := validateCommonMetadataPatch(request.Metadata); err != nil {
		return IssueMutationResult{}, err
	}
	if request.Metadata.Replace.Set || request.Metadata.Merge.Set || len(request.Metadata.Set) > 0 || len(request.Metadata.Unset) > 0 {
		patch.Metadata = request.Metadata
		count++
	}
	if count == 0 && !request.PropertiesProvided && request.FromRevision == "" {
		return IssueMutationResult{}, fmt.Errorf("%w: Issue update requires an admitted field", storage.ErrValidation)
	}
	attempt := publicops.UpdateRequest{Actor: request.Actor, Patch: patch, IssuePlaneOnly: true, ForceNotesOverwrite: request.ForceNotesOverwrite, ForceAssigneeTransfer: request.ForceAssigneeTransfer}
	// Historical composition supplies native setters (including Notes) before
	// validating setter-dependent force intent. The composed request still goes
	// through the same validator and native authorization fences in the transaction.
	if request.FromRevision == "" {
		if err := issueops.ValidateUpdateRequest(attempt); err != nil {
			return IssueMutationResult{}, err
		}
	}
	var result IssueMutationResult
	err := s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		path := request.Path
		if request.NativeID != "" {
			var err error
			path, _, err = s.resolveLiveIssueSelectorInTx(ctx, tx, IssueSelector{Path: request.Path, NativeID: request.NativeID})
			if err != nil {
				if errors.Is(err, errNonIssueSelector) {
					return ErrCapabilityUnavailable
				}
				return err
			}
		}
		if request.FromRevision != "" {
			if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
				return err
			}
		}
		var err error
		var before IssueRecord
		var revision string
		var terminal *BeadTerminalState
		var generation sql.NullInt64
		var state, backing string
		if err := tx.QueryRowContext(ctx, `SELECT allocation_state,backing,revision FROM graph_preview_catalog WHERE path=?`, path).Scan(&state, &backing, &revision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if state == "deleted" && backing == "issue" && request.FromRevision != "" {
			if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, revision, true, "Issue"); err != nil {
				return err
			}
			if err := s.checkHistoricalIssueReadBytes(ctx, tx, path, request.FromRevision); err != nil {
				return err
			}
			var err error
			before, terminal, generation, err = s.deletedIssueStateInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			if terminal == nil {
				return fmt.Errorf("%w: legacy native deletion has no distinct deletion revision", ErrCapabilityUnavailable)
			}
			if request.FromRevision == terminal.Revision {
				return ErrGone
			}
		} else {
			current, actual, err := s.beadEndpointInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			var ok bool
			before, ok = current.(IssueRecord)
			if !ok {
				return fmt.Errorf("%w: Issue update requires the experimental Issue Type", ErrCapabilityUnavailable)
			}
			revision = actual
		}
		if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, revision, true, "Issue"); err != nil {
			return err
		}
		if request.FromRevision != "" {
			if terminal == nil {
				if err := s.checkHistoricalIssueReadBytes(ctx, tx, path, request.FromRevision); err != nil {
					return err
				}
			}
			patch, err = s.historicalIssuePatchInTx(ctx, tx, path, request.FromRevision, before, patch, request.PropertiesPatch, terminal != nil)
			if err != nil {
				return err
			}
			attempt.Patch = patch
			if err := issueops.ValidateUpdateRequest(attempt); err != nil {
				return err
			}
		}
		if terminal != nil {
			var err error
			result, err = s.recoverIssueInTx(ctx, tx, path, before, *terminal, generation, attempt, request.FromRevision)
			return err
		}
		beforeNative := *before.Properties
		beforeNative.Metadata = before.Metadata
		if err := issueops.AuthorizeTemplateUpdate(&beforeNative, attempt); err != nil {
			return err
		}
		if err := issueops.AuthorizeNotesOverwrite(&beforeNative, attempt); err != nil {
			return err
		}
		// Resolve against the checked snapshot only for no-op planning. The native
		// writer receives the original append intent and owns atomic composition.
		updates, err := issueops.ResolveMergeOps(&beforeNative, issueops.UpdateFields(patch))
		if err != nil {
			return err
		}
		updates, err = issueops.DiscardNoopIssueUpdates(&beforeNative, updates)
		if err != nil {
			return err
		}
		resolvedMetadata, metadataChanged, err := issueops.ApplyMetadataPatch(before.Metadata, patch.Metadata)
		if err != nil {
			return err
		}
		// The native Issue writer owns this update, but graph records must meet
		// the same I-JSON admission as Memory and Link metadata. Validate the
		// resolved object before either the no-op return or any write effect.
		if _, err := commonMetadata(resolvedMetadata); err != nil {
			return err
		}
		if len(updates) == 0 && !metadataChanged {
			result = IssueMutationResult{Issue: before}
			return nil
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unscope()
		attempt.IssueID = before.Properties.ID
		updated, _, err := issueops.ExecuteUpdate(ctx, tx, attempt)
		if err != nil {
			return err
		}
		if !updated.Changed {
			return fmt.Errorf("%w: Issue update unexpectedly became a no-op", ErrInvalidStore)
		}
		// Non-strict SQL may coerce an oversized Go integer into the column's
		// range without returning an error. The shared writer hydrates the actual
		// stored result; never publish or retain an estimate different from the
		// accepted intent. Refusal rolls back sibling edits and audit effects too.
		if patch.EstimatedMinutes.Set {
			if updated.Issue == nil || (patch.EstimatedMinutes.Value == nil) != (updated.Issue.EstimatedMinutes == nil) ||
				(patch.EstimatedMinutes.Value != nil && *updated.Issue.EstimatedMinutes != *patch.EstimatedMinutes.Value) {
				return fmt.Errorf("%w: Issue estimate cannot be represented exactly by storage", storage.ErrValidation)
			}
		}
		if patch.ExternalRef.Set {
			if updated.Issue == nil || (patch.ExternalRef.Value == nil) != (updated.Issue.ExternalRef == nil) ||
				(patch.ExternalRef.Value != nil && *patch.ExternalRef.Value != *updated.Issue.ExternalRef) {
				return fmt.Errorf("%w: Issue external reference cannot be represented exactly by storage", storage.ErrValidation)
			}
		}
		if patch.SpecID.Set && (updated.Issue == nil || updated.Issue.SpecID != patch.SpecID.Value) {
			return fmt.Errorf("%w: Issue spec ID cannot be represented exactly by storage", storage.ErrValidation)
		}
		if patch.DueAt.Set && (updated.Issue == nil || !sameIssueDue(patch.DueAt.Value, updated.Issue.DueAt)) {
			return fmt.Errorf("%w: Issue due date cannot be represented exactly by storage", storage.ErrValidation)
		}
		if err := s.afterStage("issue-update"); err != nil {
			return err
		}
		// The target shared writer owns the one native retained version.
		if err := s.afterStage("issue-retained"); err != nil {
			return err
		}
		var historical *HistoricalUpdateContext
		if request.FromRevision != "" {
			historical = &HistoricalUpdateContext{FromRevision: request.FromRevision, PreviousRevision: revision}
		}
		if err := s.recordIssueMappingWithContextInTx(ctx, tx, path, before.Properties.ID, historical, request.Actor); err != nil {
			return err
		}
		// Charge the new retained head as well as current data; an unreadable
		// notes or metadata edit rolls back every write effect.
		if request.FromRevision != "" || patch.Notes.Set || patch.AppendNotes.Set || metadataChanged {
			if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
				return err
			}
		}
		after, err := s.showIssueInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		result = IssueMutationResult{Issue: after, Changed: true}
		return nil
	})
	if err != nil {
		return IssueMutationResult{}, err
	}
	return result, nil
}

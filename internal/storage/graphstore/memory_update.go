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
	publicops "github.com/steveyegge/beads/issueops"
)

// MemoryUpdateRequest replaces the complete selected Memory properties object.
// Empty strings are values; the caller must distinguish omitted JSON members.
type MemoryUpdateRequest struct {
	TypeURL          string
	Path             string
	Properties       Properties
	Actor            string
	ExpectedRevision string
	FromRevision     string
	Unconditional    bool
	Metadata         publicops.MetadataPatch
}

type MemoryMutationResult struct {
	Memory   Record          `json:"memory"`
	Changed  bool            `json:"changed"`
	Replaced *ReplacedMemory `json:"replaced,omitempty"`
}

// UpdateMemory retains the complete accepted Memory, including unchanged owned
// Links, in the same transaction as its properties and revision. This preview
// operation establishes no public History ordering or change-context contract.
func (s *Store) UpdateMemory(ctx context.Context, request MemoryUpdateRequest) (MemoryMutationResult, error) {
	return s.writeMemory(ctx, memoryWriteRequest{
		typeURL: request.TypeURL, completeProperties: &request.Properties,
		path: request.Path, actor: request.Actor, expectedRevision: request.ExpectedRevision,
		unconditional: request.Unconditional, fromRevision: request.FromRevision, title: request.Properties.Title, body: request.Properties.Body,
		hasTitle: true, hasBody: true, metadataPatch: request.Metadata,
	})
}

// MemoryPatchRequest changes only supplied fields. A nil pointer preserves the
// checked predecessor's field; a pointer to an empty string clears that field.
// At least one field must be supplied. This is an internal preview API.
type MemoryPatchRequest struct {
	TypeURL            string
	PropertiesJSON     json.RawMessage
	PropertiesMerge    json.RawMessage
	Path               string
	Title              *string
	Body               *string
	PropertiesProvided bool
	Actor              string
	ExpectedRevision   string
	FromRevision       string
	Unconditional      bool
	Metadata           publicops.MetadataPatch
}

// PatchMemory resolves omitted fields from the actual predecessor inside the
// mutation transaction, under the same guard and retention rules as UpdateMemory.
func (s *Store) PatchMemory(ctx context.Context, request MemoryPatchRequest) (MemoryMutationResult, error) {
	patch := memoryWriteRequest{path: request.Path, actor: request.Actor,
		typeURL:          request.TypeURL,
		expectedRevision: request.ExpectedRevision, unconditional: request.Unconditional, metadataPatch: request.Metadata,
		propertiesProvided: request.PropertiesProvided, fromRevision: request.FromRevision}
	if len(request.PropertiesMerge) != 0 {
		if len(request.PropertiesJSON) != 0 || request.Title != nil || request.Body != nil {
			return MemoryMutationResult{}, fmt.Errorf("%w: property merge cannot accompany replacement fields", storage.ErrValidation)
		}
		canonical, err := graph.CanonicalizeJSON(request.PropertiesMerge)
		if err != nil {
			return MemoryMutationResult{}, err
		}
		if len(canonical) == 0 || canonical[0] != '{' {
			return MemoryMutationResult{}, fmt.Errorf("%w: property merge requires an object", storage.ErrValidation)
		}
		patch.propertiesMerge = string(canonical)
		patch.propertiesProvided = true
	}
	if len(request.PropertiesJSON) != 0 {
		if request.Title != nil || request.Body != nil {
			return MemoryMutationResult{}, fmt.Errorf("%w: complete properties cannot accompany individual fields", storage.ErrValidation)
		}
		var complete Properties
		if err := json.Unmarshal(request.PropertiesJSON, &complete); err != nil {
			return MemoryMutationResult{}, err
		}
		patch.completeProperties = &complete
	}
	// Capture caller-owned pointers before entering the transaction. No pointer is
	// retained or read by the transaction callback.
	if request.Title != nil {
		patch.title, patch.hasTitle = *request.Title, true
	}
	if request.Body != nil {
		patch.body, patch.hasBody = *request.Body, true
	}
	return s.writeMemory(ctx, patch)
}

type memoryWriteRequest struct {
	path, actor, expectedRevision string
	fromRevision                  string
	unconditional                 bool
	title, body                   string
	typeURL                       string
	completeProperties            *Properties
	propertiesMerge               string
	hasTitle, hasBody             bool
	propertiesPatch               *graphpatch.Patch
	propertiesProvided            bool
	metadataPatch                 publicops.MetadataPatch
}

func (s *Store) writeMemory(ctx context.Context, request memoryWriteRequest) (MemoryMutationResult, error) {
	if err := validatePath(request.path); err != nil {
		return MemoryMutationResult{}, fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	if request.fromRevision != "" {
		if err := validateVersionToken(request.fromRevision); err != nil {
			return MemoryMutationResult{}, err
		}
	}
	if request.fromRevision == "" && request.typeURL == "" && request.completeProperties == nil && !request.hasTitle && !request.hasBody && request.propertiesPatch == nil && !request.propertiesProvided && !hasCommonMetadataPatch(request.metadataPatch) {
		return MemoryMutationResult{}, fmt.Errorf("%w: at least one Memory field must be supplied", storage.ErrValidation)
	}
	if request.propertiesPatch != nil && (request.hasTitle || request.hasBody) {
		return MemoryMutationResult{}, fmt.Errorf("%w: ordered Memory properties patch cannot be combined with field replacement", storage.ErrValidation)
	}
	if err := validateCommonMetadataPatch(request.metadataPatch); err != nil {
		return MemoryMutationResult{}, err
	}
	if !utf8.ValidString(request.title) || !utf8.ValidString(request.body) || !utf8.ValidString(request.actor) {
		return MemoryMutationResult{}, fmt.Errorf("%w: Memory title, body and actor must be UTF-8", storage.ErrValidation)
	}
	var result MemoryMutationResult
	err := s.withTypeValidationTx(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = s.writeMemoryInTx(ctx, tx, request)
		return err
	})
	if err != nil {
		return MemoryMutationResult{}, err
	}
	return result, nil
}

func (s *Store) writeMemoryInTx(ctx context.Context, tx *sql.Tx, request memoryWriteRequest) (MemoryMutationResult, error) {
	var result MemoryMutationResult
	err := func() error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if request.fromRevision != "" {
			if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
				return err
			}
		}
		var current any
		var revision string
		var terminal *MemoryTerminalState
		var err error
		var state, backing string
		if err := tx.QueryRowContext(ctx, `SELECT allocation_state,backing FROM graph_preview_catalog WHERE path=?`, request.path).Scan(&state, &backing); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if state == "deleted" && request.fromRevision != "" {
			if backing != "generic" {
				return fmt.Errorf("%w: historical native Issue recovery is not supported by the Memory writer", ErrCapabilityUnavailable)
			}
			baseline, deleted, readErr := s.deletedMemoryStateInTx(ctx, tx, request.path)
			if readErr != nil {
				return readErr
			}
			if deleted == nil {
				return fmt.Errorf("%w: recovery requires an actual retained deletion revision", ErrCapabilityUnavailable)
			}
			current, revision, terminal = baseline, deleted.Revision, deleted
		} else {
			current, revision, err = s.beadEndpointInTx(ctx, tx, request.path)
			if err != nil {
				return err
			}
		}
		memory, ok := current.(Record)
		if !ok {
			return fmt.Errorf("%w: property replacement supports the experimental Memory Type only", ErrCapabilityUnavailable)
		}
		if err := checkRevisionGuard(request.expectedRevision, request.unconditional, revision, true, "Memory"); err != nil {
			return err
		}
		admittedType := memory.Type
		if request.typeURL != "" {
			admittedType = request.typeURL
		}
		if err := s.requireAssessmentAuthoring(admittedType); err != nil {
			return err
		}
		// Live historical restore and deleted recovery are both authoring.
		// Neither can masquerade as the permitted ordinary retype-out.
		if request.fromRevision != "" {
			if err := s.requireAssessmentAuthoring(memory.Type); err != nil {
				return err
			}
		}
		if terminal != nil && continuityKey(memory.Metadata) != "" {
			return fmt.Errorf("%w: managed continuity recovery uses Remember and a new identity", ErrCapabilityUnavailable)
		}
		base := memory
		if request.fromRevision != "" {
			if request.typeURL != "" && request.typeURL != memory.Type {
				return fmt.Errorf("%w: historical Update preserves the current Type", storage.ErrValidation)
			}
			if err := s.checkHistoricalMemoryReadBytes(ctx, tx, request.path, request.fromRevision); err != nil {
				return err
			}
			// Current allocation/baseline was validated above in this transaction;
			// reuse the exact Memory decoder without hydrating that baseline twice.
			selected, err := s.readMemoryVersionInTx(ctx, tx, request.path, "", request.fromRevision, revision)
			if err != nil {
				return err
			}
			retained := selected
			if retained.ID != memory.ID {
				return fmt.Errorf("%w: historical source requires the same Memory identity", ErrCapabilityUnavailable)
			}
			base.Properties = retained.Properties
			base.Metadata = retained.Metadata
			catalog, err := s.selectedTypes()
			if err != nil {
				return err
			}
			if catalog.evaluationType(memory.Type) {
				base.Properties, err = historicalEvaluationBase(memory.Properties, base.Properties)
				if err != nil {
					return err
				}
			}
		}
		next := base.Properties
		nextType := memory.Type
		if request.typeURL != "" {
			nextType = request.typeURL
		}
		if request.completeProperties != nil {
			next = *request.completeProperties
		}
		if request.propertiesMerge != "" {
			next, err = mergeMemoryProperties(next, []byte(request.propertiesMerge))
			if err != nil {
				return err
			}
		}
		if request.hasTitle {
			next.Title = request.title
		}
		if request.hasBody {
			next.Body = request.body
		}
		if request.propertiesPatch != nil {
			next, err = applyMemoryPropertiesPatch(request.propertiesPatch, base.Properties)
			if err != nil {
				return err
			}
		}
		metadata, _, err := issueops.ApplyMetadataPatch(base.Metadata, request.metadataPatch)
		if err != nil {
			return err
		}
		metadata, err = commonMetadata(metadata)
		if err != nil {
			return err
		}
		if err := preserveContinuityKey(memory.Metadata, metadata); err != nil {
			return err
		}
		properties, err := canonicalJSON(next)
		if err != nil {
			return err
		}
		catalog, err := s.selectedTypes()
		if err != nil {
			return err
		}
		if !catalog.memoryType(nextType) {
			return fmt.Errorf("%w: target Type requires its native domain writer", storage.ErrValidation)
		}
		if err := catalog.validateProperties(nextType, properties, graph.KindBead); err != nil {
			return err
		}
		if catalog.evaluationType(nextType) {
			if catalog.evaluationType(memory.Type) {
				if err := preserveEvaluationSubjectGroup(memory.Properties, next); err != nil {
					return err
				}
			} else if err := s.preserveRetainedEvaluationGroup(ctx, tx, request.path, revision, next); err != nil {
				return err
			}
		}
		if terminal == nil && memory.Properties == next && memory.Type == nextType && bytes.Equal(memory.Metadata, metadata) {
			result = MemoryMutationResult{Memory: memory}
			return nil
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		if terminal == nil {
			if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_payloads SET properties=?, metadata=? WHERE path=?`, properties, metadata, request.path); err != nil {
				return err
			}
		} else {
			changed, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET allocation_state='live' WHERE path=? AND allocation_state='deleted' AND revision=?`, request.path, revision)
			if err != nil {
				return err
			}
			if n, err := changed.RowsAffected(); err != nil {
				return err
			} else if n != 1 {
				return fmt.Errorf("%w: recovery lost its deletion predecessor", ErrConflict)
			}
			if err := s.afterStage("memory-recover-allocation"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_payloads(path,properties,metadata) VALUES(?,?,?)`, request.path, properties, metadata); err != nil {
				return err
			}
			memory.Attribution.Inception = "revert"
		}
		if err := s.afterStage("memory-payload"); err != nil {
			return err
		}
		replaced := replacedMemory(memory, request.unconditional)
		if terminal != nil {
			replaced = nil
		}
		memory.Properties = next
		memory.Type = nextType
		if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET type_url=? WHERE path=?`, nextType, request.path); err != nil {
			return err
		}
		memory.Metadata = metadata
		var historical *HistoricalUpdateContext
		if request.fromRevision != "" {
			historical = &HistoricalUpdateContext{FromRevision: request.fromRevision, PreviousRevision: revision}
		}
		accepted, err := s.recordOwnedMemoryWithContextInTx(ctx, tx, request.path, request.actor, memory, historical)
		if err != nil {
			return err
		}
		// New ordered patches must not publish a state that exceeds the existing
		// current-read acquisition budget. Existing replacement/field routes retain
		// their prior admission behavior. A no-op above writes nothing.
		if request.fromRevision != "" || request.propertiesPatch != nil || hasCommonMetadataPatch(request.metadataPatch) {
			if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
				return err
			}
		}
		result = MemoryMutationResult{Memory: accepted.(Record), Changed: true, Replaced: replaced}
		return nil
	}()
	if err != nil {
		return MemoryMutationResult{}, err
	}
	return result, nil
}

// Claims belong to one collective group. Changing ordinary Evaluation content
// may preserve or reorder that group, but may not retarget existing claims.
func preserveEvaluationSubjectGroup(previous, next Properties) error {
	group := func(properties Properties) (map[exactSubjectBinding]struct{}, error) {
		var fields struct {
			Subjects []exactSubjectBinding `json:"subjects"`
		}
		if err := json.Unmarshal([]byte(properties.extra), &fields); err != nil || len(fields.Subjects) == 0 {
			return nil, fmt.Errorf("%w: Evaluation requires exact subject bindings", storage.ErrValidation)
		}
		members := make(map[exactSubjectBinding]struct{}, len(fields.Subjects))
		for _, subject := range fields.Subjects {
			members[subject] = struct{}{}
		}
		return members, nil
	}
	before, err := group(previous)
	if err != nil {
		return err
	}
	after, err := group(next)
	if err != nil {
		return err
	}
	if len(before) == len(after) {
		same := true
		for member := range before {
			if _, present := after[member]; !present {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	return fmt.Errorf("%w: a different subject group requires a separate Evaluation", storage.ErrValidation)
}

// Retyping away from Evaluation does not release the identity's first subject
// group. Ordinary Memory extension fields never establish that binding. Retained
// history is the authority; incomplete or oversized history refuses admission.
func (s *Store) preserveRetainedEvaluationGroup(ctx context.Context, tx *sql.Tx, path, head string, next Properties) error {
	versions, err := previewVersionsInTx(ctx, tx, path, head, "", true, shippedVersionBounds())
	if err != nil {
		return err
	}
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT CAST(LEAST(COALESCE(SUM(OCTET_LENGTH(snapshot)),0),?) AS UNSIGNED)
 FROM graph_preview_versions WHERE path=?`, PreviewCurrentReadByteLimit+1, path).Scan(&size); err != nil {
		return err
	}
	if size > PreviewCurrentReadByteLimit {
		return fmt.Errorf("%w: Evaluation binding history exceeds acquisition budget", ErrLimitExceeded)
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return err
	}
	for i := len(versions) - 1; i >= 0; i-- {
		version := versions[i].Version
		raw, actor, err := readVersionBytes(ctx, tx, path, version, head)
		if err != nil {
			return err
		}
		if versions[i].Removed {
			if _, err := s.decodeMemoryTerminal(ctx, tx, path, version, raw, actor); err != nil {
				return err
			}
			continue
		}
		memory, err := s.decodeMemoryVersion(ctx, tx, path, "", version, raw, actor)
		if err != nil {
			return err
		}
		if catalog.evaluationType(memory.Type) {
			return preserveEvaluationSubjectGroup(memory.Properties, next)
		}
	}
	return nil
}

// The selected Evaluation subject group is initialized once per identity. A
// historical content source never rolls it back; explicit edits are still
// checked by the ordinary preserveEvaluationSubjectGroup admission below.
func historicalEvaluationBase(current, source Properties) (Properties, error) {
	currentJSON, err := canonicalJSON(current)
	if err != nil {
		return Properties{}, err
	}
	sourceJSON, err := canonicalJSON(source)
	if err != nil {
		return Properties{}, err
	}
	var currentFields, sourceFields map[string]json.RawMessage
	if err := json.Unmarshal(currentJSON, &currentFields); err != nil {
		return Properties{}, err
	}
	if err := json.Unmarshal(sourceJSON, &sourceFields); err != nil {
		return Properties{}, err
	}
	if len(currentFields["subjects"]) == 0 {
		return Properties{}, fmt.Errorf("%w: current Evaluation subject baseline is unavailable", ErrInvalidStore)
	}
	sourceFields["subjects"] = currentFields["subjects"]
	raw, err := canonicalJSON(sourceFields)
	if err != nil {
		return Properties{}, err
	}
	var result Properties
	err = json.Unmarshal(raw, &result)
	return result, err
}

package graphstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/graphpatch"
	"github.com/steveyegge/beads/internal/storage"
	publicops "github.com/steveyegge/beads/issueops"
)

// MemoryPropertiesPatchRequest applies ordered property operations to the
// actual guarded predecessor. All authored properties survive unless explicitly
// edited; the complete resulting object is validated for the selected Type.
type MemoryPropertiesPatchRequest struct {
	TypeURL          string
	Path             string
	Patch            []byte
	Actor            string
	ExpectedRevision string
	FromRevision     string
	Unconditional    bool
	Metadata         publicops.MetadataPatch
}

func (s *Store) PatchMemoryProperties(ctx context.Context, request MemoryPropertiesPatchRequest) (MemoryMutationResult, error) {
	patch, err := graphpatch.Parse(request.Patch)
	if err != nil {
		return MemoryMutationResult{}, memoryPropertiesPatchError(err)
	}
	return s.writeMemory(ctx, memoryWriteRequest{
		typeURL: request.TypeURL,
		path:    request.Path, actor: request.Actor, expectedRevision: request.ExpectedRevision,
		unconditional: request.Unconditional, fromRevision: request.FromRevision, propertiesPatch: patch, metadataPatch: request.Metadata,
	})
}

func memoryPropertiesPatchError(err error) error {
	if errors.Is(err, graphpatch.ErrLimit) {
		return fmt.Errorf("%w: %v", ErrLimitExceeded, err)
	}
	return fmt.Errorf("%w: %v", storage.ErrValidation, err)
}

func applyMemoryPropertiesPatch(patch *graphpatch.Patch, before Properties) (Properties, error) {
	// Refuse obvious oversized subjects before allocating their JSON form. The
	// evaluator also charges canonical escaping and the complete object framing.
	if len(before.Title) > graphpatch.MaxInputBytes || len(before.Body) > graphpatch.MaxInputBytes-len(before.Title) || len(before.extra) > graphpatch.MaxInputBytes-len(before.Title)-len(before.Body) {
		return Properties{}, fmt.Errorf("%w: Memory properties exceed the patch working-document limit", ErrLimitExceeded)
	}
	raw, err := canonicalJSON(before)
	if err != nil {
		return Properties{}, err
	}
	properties, err := graphops.NewProperties(raw)
	if err != nil {
		return Properties{}, err
	}
	after, err := patch.Apply(properties)
	if err != nil {
		return Properties{}, memoryPropertiesPatchError(err)
	}
	var result Properties
	if err := json.Unmarshal(after.Bytes(), &result); err != nil {
		return Properties{}, memoryPropertiesPatchError(err)
	}
	return result, nil
}

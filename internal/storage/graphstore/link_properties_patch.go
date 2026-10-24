package graphstore

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/graphpatch"
	"github.com/steveyegge/beads/internal/storage"
	publicops "github.com/steveyegge/beads/issueops"
)

// LinkPropertiesPatchRequest combines Link property operations, optional
// metadata edits and independent informational endpoint pin intents. Each Type
// validates its final properties. Owned Links require a separate source guard;
// native Dependency edits use the Issue-domain writer and version their source.
// This preview does not expose the public BDP Update profile.
type LinkPropertiesPatchRequest struct {
	FromRevision           string
	Path                   string
	Patch                  []byte
	Actor                  string
	ExpectedRevision       string
	Unconditional          bool
	ExpectedSourceRevision string
	UnconditionalSource    bool
	Metadata               publicops.MetadataPatch
	SourcePin, TargetPin   *EndpointPin
}

func (s *Store) PatchLinkProperties(ctx context.Context, request LinkPropertiesPatchRequest) (LinkMutationResult, error) {
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
	patch, err := graphpatch.Parse(request.Patch)
	if err != nil {
		return LinkMutationResult{}, memoryPropertiesPatchError(err)
	}
	return s.writeLinkProperties(ctx, LinkUpdateRequest{
		Path: request.Path, FromRevision: request.FromRevision, Actor: request.Actor, ExpectedRevision: request.ExpectedRevision,
		Unconditional: request.Unconditional, ExpectedSourceRevision: request.ExpectedSourceRevision,
		UnconditionalSource: request.UnconditionalSource, Metadata: request.Metadata, SourcePin: request.SourcePin, TargetPin: request.TargetPin,
	}, nil, patch)
}

func applyLinkPropertiesPatch(patch *graphpatch.Patch, before []byte) ([]byte, error) {
	if len(before) > graphpatch.MaxDocumentBytes {
		return nil, fmt.Errorf("%w: Link properties exceed the patch working-document limit", ErrLimitExceeded)
	}
	properties, err := graphops.NewProperties(before)
	if err != nil {
		return nil, err
	}
	after, err := patch.Apply(properties)
	if err != nil {
		return nil, memoryPropertiesPatchError(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(after.Bytes(), &fields); err != nil {
		return nil, memoryPropertiesPatchError(err)
	}
	// Validate the complete final representation, including absent versus null
	// or empty note. Intermediate operations may temporarily use richer JSON.
	return informationalProperties(fields)
}

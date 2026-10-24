package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// nativeFamilyReference is the private semantic adapter: a concrete native
// declaration retains its public identity, while inherited endpoint/ownership
// constraints operate on the native family. This never authorizes admission.
func nativeFamilyReference(scope, id string) string {
	if base, _, ok := NativeTypeClassification(scope, id); ok {
		return base
	}
	return id
}
func isConcreteNativeIssue(scope, id string) bool {
	base, _, ok := NativeTypeClassification(scope, id)
	return ok && base == IssueTypeURL(scope)
}
func isConcreteNativeDependency(scope, id string) bool {
	base, kind, ok := NativeTypeClassification(scope, id)
	return ok && base == DependencyTypeURL(scope) && types.DependencyType(kind).IsValid()
}

// selectedDescriptorsInTx combines checked immutable bindings with configured
// unused native Issue classes. Used bindings never disappear after a config
// edit or reclassification, and enumeration never scans retained payloads.
func (s *Store) selectedDescriptorsInTx(ctx context.Context, tx *sql.Tx) ([]graph.TypeDescriptor, error) {
	result, err := installedPreviewTypes(ctx, tx, s.ScopeURL())
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, d := range result {
		seen[d.ID()] = true
	}
	custom, err := issueops.ResolveCustomTypesInTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, value := range custom {
		id, err := NativeIssueTypeURL(s.ScopeURL(), value)
		if err != nil {
			return nil, err
		}
		if !seen[id] {
			d, err := nativeConcreteDescriptor(s.ScopeURL(), id)
			if err != nil {
				return nil, err
			}
			result = append(result, d)
			seen[id] = true
		}
	}
	if len(result) > len(previewTypeDefinitions())+PreviewSnapshotLimit {
		return nil, fmt.Errorf("%w: complete Type inventory exceeds acquisition bound", ErrLimitExceeded)
	}
	sort.Slice(result, func(i, j int) bool { return graph.CompareCodeUnits(result[i].ID(), result[j].ID()) < 0 })
	return result, nil
}

func nativeIssueTypeMatches(scope, actual, family string) bool {
	return family == IssueTypeURL(scope) && isConcreteNativeIssue(scope, actual)
}

func (s *Store) nativeDependencyTypeIDsInTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	descriptors, err := s.selectedDescriptorsInTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, d := range descriptors {
		if isConcreteNativeDependency(s.ScopeURL(), d.ID()) {
			result = append(result, d.ID())
		}
	}
	return result, nil
}

// selectedFamilyIDsInTx resolves a family against the complete checked descriptor
// inventory. It does not infer semantics from a URI basename or native label.
func (s *Store) selectedFamilyIDsInTx(ctx context.Context, tx *sql.Tx, required string, kind graph.ResourceKind) (map[string]bool, error) {
	if required == "" {
		return nil, nil
	}
	descriptors, err := s.selectedDescriptorsInTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	byID := map[string]graph.TypeDescriptor{}
	for _, descriptor := range descriptors {
		byID[descriptor.ID()] = descriptor
	}
	root, exists := byID[required]
	if !exists || root.Describes() != kind {
		return nil, fmt.Errorf("%w: selected family is not an installed %s Type", ErrCapabilityUnavailable, kind)
	}
	result := map[string]bool{}
	for _, descriptor := range descriptors {
		if descriptor.Describes() != kind {
			continue
		}
		pending, seen := []string{descriptor.ID()}, map[string]bool{}
		for len(pending) != 0 {
			id := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if seen[id] {
				continue
			}
			seen[id] = true
			parent, exists := byID[id]
			if !exists || parent.Describes() != kind {
				return nil, fmt.Errorf("%w: incomplete Type ancestry", ErrInvalidStore)
			}
			if id == required {
				result[descriptor.ID()] = true
			}
			pending = append(pending, parent.ConformsTo()...)
		}
	}
	return result, nil
}

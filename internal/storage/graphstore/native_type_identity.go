package graphstore

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/types"
)

// NativeIssueTypeURL returns the Scope-local concrete identity of a native
// classification. Mapping is injective and does not authorize a custom class:
// the native Issue writer still validates its workspace configuration.
func NativeIssueTypeURL(scope, classification string) (string, error) {
	// The native column is VARCHAR(32), measured in Unicode scalars. Refuse
	// unrepresentable values instead of truncating or assigning an unusable ID.
	if utf8.RuneCountInString(classification) > 32 {
		return "", fmt.Errorf("%w: native Issue classification exceeds 32 characters", graph.ErrValidation)
	}
	return nativeConcreteTypeURL(scope, "native-issue", classification)
}

// NativeDependencyTypeURL preserves the exact validated native kind. Its
// namespace is disjoint from Issue and Memory Types, including native kinds
// whose spelling is already used by the Memory catalog.
func NativeDependencyTypeURL(scope, kind string) (string, error) {
	if !types.DependencyType(kind).IsValid() {
		return "", fmt.Errorf("%w: native Dependency kind exceeds its native storage bound", graph.ErrValidation)
	}
	return nativeConcreteTypeURL(scope, "native-dependency", kind)
}

func nativeConcreteTypeURL(scope, family, classification string) (string, error) {
	if err := graph.ValidateScopeURL(scope); err != nil {
		return "", err
	}
	if classification == "" || !utf8.ValidString(classification) {
		return "", fmt.Errorf("%w: native classification must be nonempty UTF-8", graph.ErrValidation)
	}
	id := scope + "types/" + family + "/" + hex.EncodeToString([]byte(classification))
	if err := graph.ValidateTypeURL(id); err != nil {
		return "", err
	}
	return id, nil
}

// NativeTypeClassification decodes only the canonical application-local native
// mapping. It is an identity operation, never installation or admission proof.
// The returned base is the abstract Issue or Dependency family identity.
func NativeTypeClassification(scope, id string) (base, classification string, ok bool) {
	for _, family := range []struct{ name, base string }{
		{"native-issue", IssueTypeURL(scope)}, {"native-dependency", DependencyTypeURL(scope)},
	} {
		prefix := scope + "types/" + family.name + "/"
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		encoded := strings.TrimPrefix(id, prefix)
		raw, err := hex.DecodeString(encoded)
		if err != nil || len(raw) == 0 || !utf8.Valid(raw) || hex.EncodeToString(raw) != encoded {
			return "", "", false
		}
		canonical, err := NativeIssueTypeURL(scope, string(raw))
		if family.base == DependencyTypeURL(scope) {
			canonical, err = NativeDependencyTypeURL(scope, string(raw))
		}
		if err != nil || canonical != id {
			return "", "", false
		}
		return family.base, string(raw), true
	}
	return "", "", false
}

func nativeConcreteDescriptor(scope, id string) (graph.TypeDescriptor, error) {
	base, classification, ok := NativeTypeClassification(scope, id)
	if !ok {
		return graph.TypeDescriptor{}, fmt.Errorf("%w: invalid native concrete Type identity", graph.ErrValidation)
	}
	kind := graph.KindBead
	spec := graph.TypeDescriptorSpec{ID: id, Name: classification, Describes: kind, ConformsTo: []string{base}, Description: "Concrete native adapter Type. The native classification column is authoritative; its domain validator controls admission. This Scope-local identity does not assign a global Type identity."}
	if base == DependencyTypeURL(scope) {
		spec.Describes = graph.KindLink
		endpoint, err := graph.NewEndpointConstraint([]string{IssueTypeURL(scope)}, graph.ExternalNone)
		if err != nil {
			return graph.TypeDescriptor{}, err
		}
		spec.Source, spec.Target = &endpoint, &endpoint
	}
	return graph.NewTypeDescriptor(spec)
}

func sameNativeIssueTypeFamily(left, right string) bool {
	i := strings.LastIndex(left, "types/native-issue/")
	if i < 0 {
		return false
	}
	scope := left[:i]
	a, _, ok := NativeTypeClassification(scope, left)
	if !ok || a != IssueTypeURL(scope) {
		return false
	}
	b, _, ok := NativeTypeClassification(scope, right)
	return ok && b == a
}

// IsNativeConcreteTypeDescriptor recognizes only the exact authoritative native
// adapter declaration. It does not authorize use of a custom classification.
func IsNativeConcreteTypeDescriptor(scope string, descriptor graph.TypeDescriptor) bool {
	expected, err := nativeConcreteDescriptor(scope, descriptor.ID())
	return err == nil && bytes.Equal(expected.CanonicalJSON(), descriptor.CanonicalJSON())
}

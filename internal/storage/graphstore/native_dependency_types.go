package graphstore

import (
	"fmt"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	publicops "github.com/steveyegge/beads/issueops"
)

func IssueTypeURL(scope string) string          { return scope + "types/preview-issue-v3" }
func DependencyTypeURL(scope string) string     { return scope + "types/preview-blocks-v2" }
func ParentChildTypeURL(scope string) string    { return scope + "types/preview-parent-child-v1" }
func DiscoveredFromTypeURL(scope string) string { return scope + "types/preview-discovered-from-v1" }
func NativeRelatedTypeURL(scope string) string  { return scope + "types/preview-native-related-v1" }

func IssueTypeURLForSchema(scope string, version int) string {
	if version == LegacySchemaVersion {
		return legacyIssueTypeURL(scope)
	}
	return IssueTypeURL(scope)
}

func IsIssueTypeURL(scope, id string) bool {
	return id == IssueTypeURL(scope) || id == legacyIssueTypeURL(scope)
}

// NativeDependencyTypeURLForSchema distinguishes native Issue-domain edges
// from independent informational Links, including native related edges.
func NativeDependencyTypeURLForSchema(scope string, typ publicops.DependencyType, version int) (string, error) {
	if version == LegacySchemaVersion && typ == publicops.DepBlocks {
		return legacyDependencyTypeURL(scope), nil
	}
	if version == SchemaVersion {
		switch typ {
		case publicops.DepBlocks:
			return DependencyTypeURL(scope), nil
		case publicops.DepParentChild:
			return ParentChildTypeURL(scope), nil
		case publicops.DepDiscoveredFrom:
			return DiscoveredFromTypeURL(scope), nil
		case publicops.DepRelated:
			return NativeRelatedTypeURL(scope), nil
		}
	}
	return "", fmt.Errorf("%w: native Dependency type %q is not admitted by graph schema %d", ErrCapabilityUnavailable, typ, version)
}

func NativeDependencyTypeFromURL(scope, id string, version int) (publicops.DependencyType, error) {
	if version == LegacySchemaVersion && id == legacyDependencyTypeURL(scope) {
		return publicops.DepBlocks, nil
	}
	if version == SchemaVersion {
		switch id {
		case DependencyTypeURL(scope):
			return publicops.DepBlocks, nil
		case ParentChildTypeURL(scope):
			return publicops.DepParentChild, nil
		case DiscoveredFromTypeURL(scope):
			return publicops.DepDiscoveredFrom, nil
		case NativeRelatedTypeURL(scope):
			return publicops.DepRelated, nil
		}
	}
	return "", fmt.Errorf("%w: native Dependency Type is not installed in this graph format", ErrCapabilityUnavailable)
}

func IsNativeDependencyTypeURL(scope, id string) bool {
	return id == legacyDependencyTypeURL(scope) || id == DependencyTypeURL(scope) ||
		id == ParentChildTypeURL(scope) || id == DiscoveredFromTypeURL(scope) || id == NativeRelatedTypeURL(scope)
}

func (s *Store) issueTypeURL() string {
	return IssueTypeURLForSchema(s.ScopeURL(), s.options.Binding.SchemaVersion)
}
func (s *Store) dependencyTypeURL() string {
	id, _ := NativeDependencyTypeURLForSchema(s.ScopeURL(), publicops.DepBlocks, s.options.Binding.SchemaVersion)
	return id
}
func (s *Store) isNativeDependencyType(id string) bool {
	_, err := NativeDependencyTypeFromURL(s.ScopeURL(), id, s.options.Binding.SchemaVersion)
	return err == nil
}
func (s *Store) nativeDependencyTypeURLs() []string {
	if s.options.Binding.SchemaVersion == LegacySchemaVersion {
		return []string{s.dependencyTypeURL()}
	}
	return []string{DependencyTypeURL(s.ScopeURL()), ParentChildTypeURL(s.ScopeURL()), DiscoveredFromTypeURL(s.ScopeURL()), NativeRelatedTypeURL(s.ScopeURL())}
}

func issueDescriptor(scope string) (graph.TypeDescriptor, error) {
	var owns []graph.OwnedLinkDecl
	for _, id := range []string{DependencyTypeURL(scope), ParentChildTypeURL(scope), DiscoveredFromTypeURL(scope), NativeRelatedTypeURL(scope)} {
		decl, err := graph.NewOwnedLinkDecl(id, "", PreviewOwnedLinkLimit)
		if err != nil {
			return graph.TypeDescriptor{}, err
		}
		owns = append(owns, decl)
	}
	return graph.NewTypeDescriptor(graph.TypeDescriptorSpec{
		ID: IssueTypeURL(scope), Name: "Experimental Issue v3", Describes: graph.KindBead,
		Description:  "Experimental native Issue adapter. Classification remains a property. Preserves native lifecycle policy and owns at most 1000 outgoing Dependencies in total across blocks, parent-child, discovered-from and related. Complete source-owned state is retained with each Issue version. No production Type or complete History contract.",
		OwnsOutgoing: owns,
	})
}

func dependencyDescriptor(scope string) (graph.TypeDescriptor, error) {
	return nativeDependencyDescriptor(scope, publicops.DepBlocks, "Experimental blocking Dependency v2")
}
func parentChildDescriptor(scope string) (graph.TypeDescriptor, error) {
	return nativeDependencyDescriptor(scope, publicops.DepParentChild, "Experimental parent-child Dependency v1")
}
func discoveredFromDescriptor(scope string) (graph.TypeDescriptor, error) {
	return nativeDependencyDescriptor(scope, publicops.DepDiscoveredFrom, "Experimental discovered-from Dependency v1")
}
func nativeRelatedDescriptor(scope string) (graph.TypeDescriptor, error) {
	return nativeDependencyDescriptor(scope, publicops.DepRelated, "Experimental native related Dependency v1")
}

func nativeDependencyDescriptor(scope string, typ publicops.DependencyType, name string) (graph.TypeDescriptor, error) {
	id, err := NativeDependencyTypeURLForSchema(scope, typ, SchemaVersion)
	if err != nil {
		return graph.TypeDescriptor{}, err
	}
	endpoint, err := graph.NewEndpointConstraint([]string{IssueTypeURL(scope)}, graph.ExternalNone)
	if err != nil {
		return graph.TypeDescriptor{}, err
	}
	return graph.NewTypeDescriptor(graph.TypeDescriptorSpec{
		ID: id, Name: name, Describes: graph.KindLink,
		Description: "Experimental native " + string(typ) + " Dependency between unpinned live local durable Issues. Native direction, pair uniqueness and scheduling policy remain authoritative. Source owns this Link. Closed properties are the native row ID (nativeId) and thread ID (threadId); common metadata preserves the native JSON object. These fields are immutable through graph authoring. No production Dependency contract.",
		Source:      &endpoint, Target: &endpoint,
	})
}

func validNativeDependencyProperties(properties map[string]any, version int) bool {
	if properties == nil {
		return false
	}
	if version == LegacySchemaVersion {
		return len(properties) == 0
	}
	id, idOK := properties["nativeId"].(string)
	thread, threadOK := properties["threadId"].(string)
	return len(properties) == 2 && idOK && id != "" && len(id) <= 255 && utf8.ValidString(id) && threadOK && len(thread) <= 255 && utf8.ValidString(thread)
}

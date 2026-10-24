package graphstore

import (
	"fmt"
	publicops "github.com/steveyegge/beads/issueops"
)

// LegacySchemaVersion names the old selector for refusal and explicit conversion.
const LegacySchemaVersion = 7

func IssueTypeURLForSchema(scope string, version int) string {
	if version == 7 {
		return scope + "types/preview-issue-v2"
	}
	if version == 8 {
		return scope + "types/preview-issue-v3"
	}
	return IssueTypeURL(scope)
}
func NativeDependencyTypeURLForSchema(scope string, typ publicops.DependencyType, version int) (string, error) {
	if version != SchemaVersion {
		return "", fmt.Errorf("%w: source format requires explicit conversion", ErrCapabilityUnavailable)
	}
	return NativeDependencyTypeURL(scope, string(typ))
}
func NativeDependencyTypeFromURL(scope, id string, version int) (publicops.DependencyType, error) {
	base, kind, ok := NativeTypeClassification(scope, id)
	if version != SchemaVersion || !ok || base != DependencyTypeURL(scope) {
		return "", ErrCapabilityUnavailable
	}
	return publicops.DependencyType(kind), nil
}
func IsIssueTypeURL(scope, id string) bool {
	return id == IssueTypeURL(scope) || isConcreteNativeIssue(scope, id)
}
func IsNativeDependencyTypeURL(scope, id string) bool { return isConcreteNativeDependency(scope, id) }

func ParentChildTypeURL(scope string) string {
	id, _ := NativeDependencyTypeURL(scope, "parent-child")
	return id
}
func DiscoveredFromTypeURL(scope string) string {
	id, _ := NativeDependencyTypeURL(scope, "discovered-from")
	return id
}
func NativeRelatedTypeURL(scope string) string {
	id, _ := NativeDependencyTypeURL(scope, "related")
	return id
}

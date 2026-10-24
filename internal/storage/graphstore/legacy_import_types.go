package graphstore

import (
	"fmt"
	"sort"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

type legacyImportNativeTypes struct {
	issues       map[string]string
	dependencies map[types.DependencyType]string
	bindings     []string
}

// Input has already passed the legacy/native admission validators. These pure
// mappings give no new permission to use a custom classification or kind.
func prepareLegacyImportNativeTypes(mounted legacyImportMount, scope string) (legacyImportNativeTypes, error) {
	result := legacyImportNativeTypes{issues: map[string]string{}, dependencies: map[types.DependencyType]string{}}
	unique := map[string]bool{}
	for _, issue := range mounted.Issues {
		sourceID := mounted.sourceIssues[issue.ID]
		id, err := NativeIssueTypeURL(scope, string(issue.IssueType))
		if err != nil {
			return legacyImportNativeTypes{}, fmt.Errorf("%w: legacy Issue %q Type: %v", storage.ErrValidation, sourceID, err)
		}
		result.issues[issue.ID] = id
		unique[id] = true
		for _, dep := range issue.Dependencies {
			id, err := NativeDependencyTypeURL(scope, string(dep.Type))
			if err != nil {
				return legacyImportNativeTypes{}, fmt.Errorf("%w: legacy dependency from %q Type: %v", storage.ErrValidation, sourceID, err)
			}
			result.dependencies[dep.Type] = id
			unique[id] = true
		}
	}
	for id := range unique {
		result.bindings = append(result.bindings, id)
	}
	// Every suffix is canonical ASCII hex beneath this same Scope URL.
	sort.Strings(result.bindings)
	return result, nil
}

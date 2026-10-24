package graphstore

import (
	"fmt"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

// legacyImportMount preserves source names for results while the detached native
// batch uses exact destination identities. It never derives public identity from
// private catalog paths or translates an invalid source key into another name.
type legacyImportMount struct {
	preparedLegacyImport
	sourceIssues map[string]string // mounted native Issue ID -> original source ID
	memoryIDs    map[string]string // original Memory key -> mounted public ID
}

// mountLegacyImport consumes an already isolated, source-validated batch. Native
// source-prefix and ownership checks must run before mounting; a root cannot make
// an incompatible source key valid. This consumes the detached batch returned by
// prepareLegacyImportWithPrefix; public entry points must not hand it caller-owned
// pointers. Admission failures occur before any detached row is mutated.
func mountLegacyImport(prepared preparedLegacyImport, root string) (legacyImportMount, error) {
	if root != "" {
		if err := validatePublicID(root); err != nil {
			return legacyImportMount{}, fmt.Errorf("%w: invalid import ID root %q: %v", storage.ErrValidation, root, err)
		}
	}
	result := legacyImportMount{preparedLegacyImport: prepared,
		sourceIssues: make(map[string]string, len(prepared.Issues)),
		memoryIDs:    make(map[string]string, len(prepared.Memories))}
	destinations := make(map[string]string, len(prepared.Issues)+len(prepared.Memories))
	sourceIDs := make(map[string]string, len(prepared.Issues))
	remaining := uint64(PreviewCurrentReadByteLimit)
	mount := func(kind, id string) (string, error) {
		if err := validatePublicID(id); err != nil {
			return "", fmt.Errorf("%w: legacy %s %q: %v", storage.ErrValidation, kind, id, err)
		}
		// Charge expansion before concatenation: a small source package with
		// many Memories and one large root must not allocate an unbounded map
		// before the transaction's aggregate current-read check can refuse it.
		size := uint64(len(id))
		if root != "" {
			size += uint64(len(root)) + 1
		}
		if size > remaining {
			return "", fmt.Errorf("%w: mounted legacy identities exceed the current-read byte budget", ErrLimitExceeded)
		}
		remaining -= size
		mounted, err := graph.MountCanonicalResourceID(root, id)
		if err != nil {
			return "", fmt.Errorf("%w: legacy %s %q: %v", storage.ErrValidation, kind, id, err)
		}
		if previous, exists := destinations[mounted]; exists {
			return "", fmt.Errorf("%w: legacy %s %q collides with %s at public ID %q", storage.ErrValidation, kind, id, previous, mounted)
		}
		destinations[mounted] = kind + " " + fmt.Sprintf("%q", id)
		return mounted, nil
	}
	for _, issue := range prepared.Issues {
		id, err := mount("Issue", issue.ID)
		if err != nil {
			return legacyImportMount{}, err
		}
		// Native issues.id permits 255 characters, but the immutable graph
		// backing_key and retained issue_id mappings are VARBINARY(255).
		// Do not impose this backing-specific limit on Memory public IDs.
		if len(id) > 255 {
			return legacyImportMount{}, fmt.Errorf("%w: legacy Issue %q mounted at %q exceeds the native backing identity limit of 255 bytes", storage.ErrValidation, issue.ID, id)
		}
		sourceIDs[issue.ID] = id
		result.sourceIssues[id] = issue.ID
	}
	for _, memory := range prepared.Memories {
		id, err := mount("Memory key", memory.Key)
		if err != nil {
			return legacyImportMount{}, err
		}
		result.memoryIDs[memory.Key] = id
	}
	// A canonical root can still change native routing semantics. In particular,
	// external: is a native dependency discriminator, not an ordinary local
	// Issue prefix. Recheck mounted pairs before any native writer can classify
	// a same-package edge as external and leave an unreadable graph allocation.
	for _, issue := range prepared.Issues {
		for _, dep := range issue.Dependencies {
			if issueops.IsExternalDepTarget(sourceIDs[dep.IssueID], sourceIDs[dep.DependsOnID]) {
				return legacyImportMount{}, fmt.Errorf("%w: import ID root %q makes legacy dependency %q -> %q use unsupported native external or cross-prefix routing", storage.ErrValidation, root, dep.IssueID, dep.DependsOnID)
			}
		}
	}
	// Finish every admission check before mutating the detached batch. Only
	// these fields are identity references; source content and attribution stay
	// byte-for-byte intact, and comment IDs remain comment identities.
	for _, issue := range prepared.Issues {
		original := issue.ID
		issue.ID = sourceIDs[original]
		for _, dep := range issue.Dependencies {
			dep.IssueID = sourceIDs[dep.IssueID]
			dep.DependsOnID = sourceIDs[dep.DependsOnID]
		}
		for _, comment := range issue.Comments {
			comment.IssueID = sourceIDs[original]
		}
	}
	return result, nil
}

package graphstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/graphpatch"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// IssueEditableProperties is the existing native graph Update setter projection.
// It excludes identity, lifecycle, initialization and owned structure.
func IssueEditableProperties(issue *types.Issue) map[string]any {
	projection := map[string]any{
		"title": issue.Title, "issue_type": string(issue.IssueType), "description": issue.Description, "design": issue.Design,
		"acceptance_criteria": issue.AcceptanceCriteria, "notes": issue.Notes,
		"spec_id": issue.SpecID, "assignee": issue.Assignee, "priority": issue.Priority,
		"estimated_minutes": issue.EstimatedMinutes, "external_ref": issue.ExternalRef,
	}
	if issue.DueAt == nil {
		projection["due_at"] = nil
	} else {
		projection["due_at"] = issue.DueAt.Format(time.RFC3339Nano)
	}
	return projection
}

func historicalIssuePropertiesPatch(source *types.Issue, raw []byte, metadata publicops.MetadataPatch) (publicops.IssuePatch, error) {
	patch, err := ParseIssuePropertyPatch(source.IssueType, raw)
	if err != nil {
		return publicops.IssuePatch{}, memoryPropertiesPatchError(err)
	}
	encoded, err := canonicalJSON(IssueEditableProperties(source))
	if err != nil {
		return publicops.IssuePatch{}, err
	}
	if len(encoded) > graphpatch.MaxDocumentBytes {
		return publicops.IssuePatch{}, fmt.Errorf("%w: Issue properties exceed property change document budget", ErrLimitExceeded)
	}
	before, err := graph.NewProperties(encoded)
	if err != nil {
		return publicops.IssuePatch{}, err
	}
	after, err := patch.Apply(before)
	if err != nil {
		return publicops.IssuePatch{}, memoryPropertiesPatchError(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(after.Bytes(), &fields); err != nil {
		return publicops.IssuePatch{}, memoryPropertiesPatchError(err)
	}
	for name, value := range fields {
		if !historicalIssueEditableMembers[name] {
			return publicops.IssuePatch{}, fmt.Errorf("%w: Issue property %s is not writable through patch", storage.ErrValidation, name)
		}
		if name != "priority" && name != "estimated_minutes" && name != "due_at" && name != "external_ref" {
			var text string
			if string(value) == "null" || json.Unmarshal(value, &text) != nil {
				return publicops.IssuePatch{}, fmt.Errorf("%w: Issue %s must be a JSON string", storage.ErrValidation, name)
			}
		}
	}
	for _, name := range []string{"title", "priority", "issue_type"} {
		if _, ok := fields[name]; !ok || string(fields[name]) == "null" {
			return publicops.IssuePatch{}, fmt.Errorf("%w: Issue %s cannot be removed", storage.ErrValidation, name)
		}
	}
	var values struct {
		Title              string          `json:"title"`
		IssueType          types.IssueType `json:"issue_type"`
		Description        string          `json:"description"`
		Design             string          `json:"design"`
		AcceptanceCriteria string          `json:"acceptance_criteria"`
		Notes              string          `json:"notes"`
		SpecID             string          `json:"spec_id"`
		Assignee           string          `json:"assignee"`
		Priority           int             `json:"priority"`
		EstimatedMinutes   *int            `json:"estimated_minutes"`
		ExternalRef        *string         `json:"external_ref"`
		DueAt              *time.Time      `json:"due_at"`
	}
	if err := json.Unmarshal(after.Bytes(), &values); err != nil {
		return publicops.IssuePatch{}, fmt.Errorf("%w: invalid Issue property value: %v", storage.ErrValidation, err)
	}
	if values.Title != source.Title {
		values.Title = strings.TrimSpace(values.Title)
	}
	if err := types.CheckFieldLen("assignee", values.Assignee); err != nil {
		return publicops.IssuePatch{}, fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	if err := validateIssueEstimateStorage(values.EstimatedMinutes); err != nil {
		return publicops.IssuePatch{}, err
	}
	if err := validateIssueReferenceStorage("spec_id", values.SpecID); err != nil {
		return publicops.IssuePatch{}, err
	}
	if values.ExternalRef != nil {
		if err := validateIssueReferenceStorage("external_ref", *values.ExternalRef); err != nil {
			return publicops.IssuePatch{}, err
		}
		if *values.ExternalRef == "" {
			values.ExternalRef = nil
		}
	}
	due, err := normalizeIssueDue(values.DueAt)
	if err != nil {
		return publicops.IssuePatch{}, err
	}
	return publicops.IssuePatch{
		Title: publicops.Field[string]{Set: true, Value: values.Title}, Description: publicops.Field[string]{Set: true, Value: values.Description},
		Design: publicops.Field[string]{Set: true, Value: values.Design}, AcceptanceCriteria: publicops.Field[string]{Set: true, Value: values.AcceptanceCriteria},
		Notes: publicops.Field[string]{Set: true, Value: values.Notes}, Assignee: publicops.Field[string]{Set: true, Value: values.Assignee}, SpecID: publicops.Field[string]{Set: true, Value: values.SpecID},
		IssueType: publicops.Field[publicops.IssueType]{Set: true, Value: values.IssueType.Normalize()},
		Priority:  publicops.Field[int]{Set: true, Value: values.Priority}, EstimatedMinutes: publicops.Field[*int]{Set: true, Value: values.EstimatedMinutes},
		ExternalRef: publicops.Field[*string]{Set: true, Value: values.ExternalRef}, DueAt: publicops.Field[*time.Time]{Set: true, Value: due}, Metadata: metadata,
	}, nil
}

// ParseIssuePropertyPatch keeps the native classification echo aligned with
// affiliation: replacing ordinary properties without a classification setter
// preserves the preceding Type. Explicit setters/removals retain ordered patch
// semantics; the native writer validates the final result and authorization.
func ParseIssuePropertyPatch(classification types.IssueType, raw []byte) (*graphpatch.Patch, error) {
	if _, err := graphpatch.Parse(raw); err != nil {
		return nil, err
	}
	var operations []struct {
		Op    string          `json:"op"`
		Path  string          `json:"path"`
		Value json.RawMessage `json:"value,omitempty"`
	}
	if err := json.Unmarshal(raw, &operations); err != nil {
		return nil, err
	}
	current, err := json.Marshal(classification)
	if err != nil {
		return nil, err
	}
	for i := range operations {
		op := &operations[i]
		switch op.Path {
		case "":
			var properties map[string]json.RawMessage
			if json.Unmarshal(op.Value, &properties) != nil || properties == nil {
				continue
			}
			if value, present := properties["issue_type"]; present {
				current = value
			} else if current != nil {
				properties["issue_type"] = current
				op.Value, err = canonicalJSON(properties)
				if err != nil {
					return nil, err
				}
			}
		case "/issue_type":
			if op.Op == "remove" {
				current = nil
			} else {
				current = op.Value
			}
		}
	}
	normalized, err := json.Marshal(operations)
	if err != nil {
		return nil, err
	}
	return graphpatch.Parse(normalized)
}

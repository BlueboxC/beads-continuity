package graphstore

import (
	"context"
	"errors"
	"fmt"

	graph "github.com/steveyegge/beads/graphops"
	publicops "github.com/steveyegge/beads/issueops"
)

// CreateIssueWithIDRoot uses the normal authoritative Issue writer while
// selecting an explicit generated public ID under a caller-supplied root.
// Ordinary native Issue creation keeps its shipping generator. Every attempt
// reserves through CreateIssue's fenced lifetime check and transaction; a
// rejected candidate leaves no Issue, History or graph allocation behind.
func (s *Store) CreateIssueWithIDRoot(ctx context.Context, root string, req publicops.CreateRequest) (IssueRecord, error) {
	if req.Issue == nil || req.Issue.ID != "" {
		return IssueRecord{}, fmt.Errorf("%w: ID root requires generated Issue identity", graph.ErrValidation)
	}
	if err := graph.ValidateGeneratedResourceIDRoot(root); err != nil {
		return IssueRecord{}, err
	}
	for length := graph.GeneratedResourceIDInitialLength; length <= graph.GeneratedResourceIDMaxLength; length++ {
		for attempt := 0; attempt < generatedIDAttemptsPerLength; attempt++ {
			if err := ctx.Err(); err != nil {
				return IssueRecord{}, err
			}
			id, err := graph.GeneratedResourceIDCandidate(root, length)
			if err != nil {
				return IssueRecord{}, err
			}
			issue := *req.Issue
			issue.ID = id
			request := req
			request.Issue, request.ForceIDPrefix = &issue, true
			accepted, err := s.CreateIssue(ctx, "", request)
			if !errors.Is(err, ErrAlreadyExists) {
				return accepted, err
			}
		}
	}
	return IssueRecord{}, fmt.Errorf("%w: generated Issue ID candidates exhausted", ErrAlreadyExists)
}

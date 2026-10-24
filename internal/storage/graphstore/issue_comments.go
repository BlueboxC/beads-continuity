package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// IssueComments reads the native comment feed after validating the graph Issue.
// Comments are intentionally outside the retained Issue durable_state.
func (s *Store) IssueComments(ctx context.Context, path string) ([]*types.Comment, error) {
	if err := validatePath(path); err != nil {
		return nil, fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	var comments []*types.Comment
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		issue, err := s.requireIssueInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		comments, err = issueops.GetIssueCommentsInTx(ctx, tx, issue.Properties.ID)
		if err != nil {
			return err
		}
		return nil
	})
	return comments, err
}

// AddIssueComment appends to the native comment feed without minting an Issue
// History or graph version. The CLI calls this after unclaim, just as ordinary
// bd does, so a comment failure never rolls back a successful release. The
// dedicated comment commands use AddIssueCommentWithIssue for their response.
func (s *Store) AddIssueComment(ctx context.Context, path, actor, text string) error {
	_, _, err := s.AddIssueCommentWithIssue(ctx, path, actor, text)
	return err
}

// AddIssueCommentWithIssue returns the comment and Issue observed in the same
// native transaction. Comment writes do not create retained Issue versions.
func (s *Store) AddIssueCommentWithIssue(ctx context.Context, path, actor, text string) (*types.Comment, IssueRecord, error) {
	if err := validatePath(path); err != nil {
		return nil, IssueRecord{}, fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	if !utf8.ValidString(actor) || !utf8.ValidString(text) {
		return nil, IssueRecord{}, fmt.Errorf("%w: comment requires UTF-8 actor and text", storage.ErrValidation)
	}
	var comment *types.Comment
	var current IssueRecord
	err := s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		issue, err := s.requireIssueInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		result, _, err := issueops.ExecuteAddComment(ctx, tx, publicops.AddCommentRequest{IssueID: issue.Properties.ID, Author: actor, Text: text})
		if err != nil {
			return err
		}
		comment = result.Comment
		current, err = s.showIssueInTx(ctx, tx, path)
		return err
	})
	if err != nil {
		return nil, IssueRecord{}, err
	}
	return comment, current, nil
}

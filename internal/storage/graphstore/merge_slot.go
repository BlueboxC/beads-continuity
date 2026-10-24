package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

type mergeSlotMetadata struct {
	Holder  string   `json:"holder,omitempty"`
	Waiters []string `json:"waiters,omitempty"`
}

// mergeSlotPath uses the ordinary issue_prefix convention, including its bd
// fallback. The canonical path is fixed for the lifetime of this workspace.
func (s *Store) mergeSlotPath(ctx context.Context) (string, error) {
	var prefix string
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, "SELECT value FROM config WHERE `key` = ?", "issue_prefix").Scan(&prefix)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return "", err
	}
	if prefix == "" {
		prefix = "bd"
	}
	id := strings.TrimSuffix(prefix, "-") + "-merge-slot"
	path := "beads/" + id
	if err := validatePath(path); err != nil {
		return "", fmt.Errorf("%w: invalid merge-slot prefix: %v", storage.ErrValidation, err)
	}
	return path, nil
}

func (s *Store) MergeSlotID(ctx context.Context) (string, error) {
	path, err := s.mergeSlotPath(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(path, "beads/"), nil
}

func parseGraphSlot(issue IssueRecord) (mergeSlotMetadata, error) {
	if issue.Properties.IssueType != types.TypeTask || !hasGraphSlotLabel(issue.Properties.Labels) {
		return mergeSlotMetadata{}, fmt.Errorf("%w: canonical merge-slot ID is occupied by a non-slot Issue", storage.ErrValidation)
	}
	var meta mergeSlotMetadata
	if len(issue.Metadata) != 0 {
		if err := json.Unmarshal(issue.Metadata, &meta); err != nil {
			return meta, fmt.Errorf("%w: invalid merge-slot metadata: %v", storage.ErrValidation, err)
		}
	}
	if issue.Properties.Status != types.StatusOpen && issue.Properties.Status != types.StatusInProgress {
		return meta, fmt.Errorf("%w: merge slot has unsupported status %s", storage.ErrValidation, issue.Properties.Status)
	}
	return meta, nil
}

func hasGraphSlotLabel(labels []string) bool {
	for _, label := range labels {
		if label == "gt:slot" {
			return true
		}
	}
	return false
}

// MergeSlotCreate creates a single durable Issue with its label already set.
// A repeat returns the existing slot without minting History.
func (s *Store) MergeSlotCreate(ctx context.Context, actor string) (IssueRecord, error) {
	path, err := s.mergeSlotPath(ctx)
	if err != nil {
		return IssueRecord{}, err
	}
	if existing, err := s.ShowIssue(ctx, path); err == nil {
		_, err = parseGraphSlot(existing)
		return existing, err
	} else if !errors.Is(err, ErrNotFound) {
		return IssueRecord{}, err
	}
	id := strings.TrimPrefix(path, "beads/")
	issue := &types.Issue{ID: id, Title: "Merge Slot", Description: "Exclusive access slot for serialized conflict resolution in the merge queue.", IssueType: types.TypeTask, Status: types.StatusOpen, Priority: 0, Labels: []string{"gt:slot"}}
	created, err := s.CreateIssue(ctx, path, publicops.CreateRequest{Actor: actor, Issue: issue})
	if errors.Is(err, ErrAlreadyExists) {
		created, err = s.ShowIssue(ctx, path)
		if err == nil {
			_, err = parseGraphSlot(created)
		}
	}
	return created, err
}

func (s *Store) MergeSlotCheck(ctx context.Context) (storage.MergeSlotStatus, error) {
	path, err := s.mergeSlotPath(ctx)
	if err != nil {
		return storage.MergeSlotStatus{}, err
	}
	issue, err := s.ShowIssue(ctx, path)
	if err != nil {
		return storage.MergeSlotStatus{}, err
	}
	meta, err := parseGraphSlot(issue)
	if err != nil {
		return storage.MergeSlotStatus{}, err
	}
	return storage.MergeSlotStatus{SlotID: issue.Properties.ID, Available: issue.Properties.Status == types.StatusOpen, Holder: meta.Holder, Waiters: meta.Waiters}, nil
}

// MergeSlotAcquire keeps the availability check, metadata edit, native Issue
// History and graph version mapping in one transaction.
func (s *Store) MergeSlotAcquire(ctx context.Context, holder, actor string, wait bool) (storage.MergeSlotResult, bool, error) {
	if holder == "" || actor == "" || !utf8.ValidString(holder) || !utf8.ValidString(actor) {
		return storage.MergeSlotResult{}, false, fmt.Errorf("%w: merge-slot holder and actor must be nonempty UTF-8", storage.ErrValidation)
	}
	path, err := s.mergeSlotPath(ctx)
	if err != nil {
		return storage.MergeSlotResult{}, false, err
	}
	var result storage.MergeSlotResult
	changed := false
	err = s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		before, err := s.requireIssueInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		meta, err := parseGraphSlot(before)
		if err != nil {
			return err
		}
		result = storage.MergeSlotResult{SlotID: before.Properties.ID, Holder: meta.Holder}
		patch := publicops.IssuePatch{}
		if before.Properties.Status == types.StatusOpen {
			meta.Holder = holder
			patch.Status = publicops.Field[publicops.Status]{Set: true, Value: types.StatusInProgress}
			result.Acquired, result.Holder = true, holder
		} else if wait {
			for i, w := range meta.Waiters {
				if w == holder {
					result.Waiting, result.Position = true, i+1
					return nil
				}
			}
			meta.Waiters = append(meta.Waiters, holder)
			result.Waiting, result.Position = true, len(meta.Waiters)
		} else {
			return nil
		}
		encoded, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		patch.Metadata.Replace = publicops.Field[json.RawMessage]{Set: true, Value: encoded}
		return s.writeMergeSlotInTx(ctx, tx, path, before, actor, patch, &changed)
	})
	return result, changed, err
}

func (s *Store) MergeSlotRelease(ctx context.Context, holder, actor string) (string, bool, error) {
	if actor == "" || !utf8.ValidString(actor) || !utf8.ValidString(holder) {
		return "", false, fmt.Errorf("%w: merge-slot actor and holder must be UTF-8", storage.ErrValidation)
	}
	path, err := s.mergeSlotPath(ctx)
	if err != nil {
		return "", false, err
	}
	var id string
	changed := false
	err = s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		before, err := s.requireIssueInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		meta, err := parseGraphSlot(before)
		if err != nil {
			return err
		}
		id = before.Properties.ID
		if holder != "" && holder != meta.Holder {
			return fmt.Errorf("slot held by %s, not %s", meta.Holder, holder)
		}
		if before.Properties.Status == types.StatusOpen {
			return nil
		}
		meta.Holder = ""
		encoded, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		patch := publicops.IssuePatch{Status: publicops.Field[publicops.Status]{Set: true, Value: types.StatusOpen}}
		patch.Metadata.Replace = publicops.Field[json.RawMessage]{Set: true, Value: encoded}
		return s.writeMergeSlotInTx(ctx, tx, path, before, actor, patch, &changed)
	})
	return id, changed, err
}

func (s *Store) writeMergeSlotInTx(ctx context.Context, tx *sql.Tx, path string, before IssueRecord, actor string, patch publicops.IssuePatch, changed *bool) error {
	if err := s.touchCoordination(ctx, tx); err != nil {
		return err
	}
	unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
	defer unscope()
	updated, _, err := issueops.ExecuteUpdate(ctx, tx, publicops.UpdateRequest{Actor: actor, IssueID: before.Properties.ID, Patch: patch, IssuePlaneOnly: true})
	if err != nil {
		return err
	}
	if !updated.Changed {
		return fmt.Errorf("%w: merge-slot writer unexpectedly made no change", ErrInvalidStore)
	}
	if err := s.afterStage("issue-update"); err != nil {
		return err
	}
	if err := s.recordIssueMappingInTx(ctx, tx, path, before.Properties.ID, actor); err != nil {
		return err
	}
	if _, err := s.showIssueInTx(ctx, tx, path); err != nil {
		return err
	}
	*changed = true
	return nil
}

package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func validateIssueCreate(request publicops.CreateRequest) error {
	if !utf8.ValidString(request.Actor) {
		return fmt.Errorf("%w: Issue actor must be UTF-8", storage.ErrValidation)
	}
	if err := issueops.ValidatePublicCreateRequest(request); err != nil {
		return err
	}
	if request.ParentID != "" || request.InheritLabelsFromParent || len(request.Dependencies) > 0 || request.WaitsFor != nil || request.IDPrefix != "" {
		return fmt.Errorf("%w: Issue preview does not support composite create options or a custom ID prefix", storage.ErrValidation)
	}
	i := request.Issue
	// CloneCreateRequest materializes empty relation slices. Their lengths
	// were checked above; preserve that empty representation without admitting
	// any dependency or comment values into this bounded adapter.
	allowed := &types.Issue{ID: i.ID, Title: i.Title, Description: i.Description, Notes: i.Notes, Owner: i.Owner, CreatedBy: i.CreatedBy, Design: i.Design, AcceptanceCriteria: i.AcceptanceCriteria, Assignee: i.Assignee, EstimatedMinutes: i.EstimatedMinutes, ExternalRef: i.ExternalRef, SpecID: i.SpecID, IssueType: i.IssueType, Status: i.Status, Priority: i.Priority, DueAt: i.DueAt, Labels: i.Labels, Dependencies: i.Dependencies, Comments: i.Comments, Metadata: i.Metadata}
	if !reflect.DeepEqual(i, allowed) {
		return fmt.Errorf("%w: Issue preview accepts only ID, title, description, design, acceptance, initial notes, owner, creator, assignee, estimate, external/spec references, classification, status, priority, due date, labels and metadata; no relationships, ephemeral or no-history records", storage.ErrValidation)
	}
	if _, err := commonMetadata(i.Metadata); err != nil {
		return err
	}
	return validateIssueCreateFields(i)
}

// CreateIssue applies the existing Issue domain create and Jim Wordelman's
// retained writer in the graph transaction. Specialized tables are the only
// current authority; issue_versions is the only Issue snapshot store.
func (s *Store) CreateIssue(ctx context.Context, path string, request publicops.CreateRequest) (IssueRecord, error) {
	if path != "" {
		if err := validatePath(path); err != nil {
			return IssueRecord{}, err
		}
	}
	request = issueops.CloneCreateRequest(request)
	if err := validateIssueCreate(request); err != nil {
		return IssueRecord{}, err
	}
	due, err := normalizeIssueDue(request.Issue.DueAt)
	if err != nil {
		return IssueRecord{}, err
	}
	request.Issue.DueAt = due
	revision, err := freshToken()
	if err != nil {
		return IssueRecord{}, err
	}
	var result IssueRecord
	err = s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
		if err != nil {
			return err
		}
		if issueops.ResolveInfraTypesInTx(ctx, tx)[string(request.Issue.IssueType.Normalize())] {
			return fmt.Errorf("%w: infrastructure Issue types are not admitted by the graph preview", storage.ErrValidation)
		}
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		// Explicit IDs can remain reserved by a deleted graph allocation even
		// after the native Issue row has been removed. Refuse before the native
		// writer can collide with retained History for that ID.
		if request.Issue.ID != "" {
			if exists, err := publicResourceIDExistsInTx(ctx, tx, request.Issue.ID); err != nil {
				return err
			} else if exists {
				return ErrAlreadyExists
			}
		}
		if path != "" {
			var exists int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM graph_preview_catalog WHERE path=?`, path).Scan(&exists)
			if err == nil {
				return ErrAlreadyExists
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		unScope := issueops.ScopeVersionedHistoryTransaction(tx, true)
		defer unScope()
		created, _, err := issueops.ExecuteCreate(ctx, tx, request)
		if err != nil {
			return err
		}
		if path == "" {
			// The graph path is a separate address from the opaque native ID.
			// A Memory or deleted Bead may reserve the preferred path while
			// the native Issue keeps its own public ID.
			path, err = generatedIssuePath(ctx, tx, created.Issue.ID)
			if err != nil {
				return err
			}
		}
		// ExecuteCreate already owns the initial retained snapshot. Verify its
		// hydrated values before publishing the graph mapping; a coercion rolls
		// back that snapshot and all ordinary create effects in this transaction.
		if !sameIssueCreateFields(request.Issue, created.Issue) {
			return fmt.Errorf("%w: Issue initial fields cannot be represented exactly by storage", storage.ErrValidation)
		}
		if issueops.IsWisp(created.Issue) {
			return fmt.Errorf("%w: Issue preview requires a durable history-bearing Issue", storage.ErrValidation)
		}
		if err := s.afterStage("issue"); err != nil {
			return err
		}
		if err := issueops.RecordVersionForCreateInTx(ctx, tx, created.Issue.ID, request.Actor); err != nil {
			return err
		}
		var ordinal int64
		if err := tx.QueryRowContext(ctx, `SELECT current_revision FROM issues WHERE id=?`, created.Issue.ID).Scan(&ordinal); err != nil {
			return err
		}
		issueType, err = NativeIssueTypeURL(s.ScopeURL(), string(created.Issue.IssueType))
		if err != nil {
			return err
		}
		if err := s.ensureNativeTypeInTx(ctx, tx, issueType); err != nil {
			return err
		}
		// Check after installing the prospective Type as well: this Resource
		// cannot take the URL of its own first-use concrete declaration.
		if exists, err := publicResourceIDExistsInTx(ctx, tx, created.Issue.ID); err != nil {
			return err
		} else if exists {
			return ErrAlreadyExists
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_catalog
            (path,resource_kind,type_url,revision,allocation_state,backing,backing_key)
			VALUES (?,'bead',?,?,'live','issue',?)`, path, issueType, revision, created.Issue.ID); err != nil {
			return err
		}
		if err := s.afterStage("issue-catalog"); err != nil {
			return err
		}
		// This is an identity mapping, not a second snapshot. The local ordinal
		// never escapes as the graph Version or as a portable History address.
		if _, err := tx.ExecContext(ctx, `INSERT INTO graph_preview_issue_versions (path,version,issue_id,issue_revision,owned,revision_context,history_ordinal) VALUES (?,?,?,?,'[]','{"inception":"create"}',?)`, path, revision, created.Issue.ID, ordinal, ordinal); err != nil {
			return err
		}
		if err := s.afterStage("retained"); err != nil {
			return err
		}
		// Initial notes have no admitted replacement/clear inverse. Charge the
		// completed mapping and native retained head before committing.
		if request.Issue.Notes != "" {
			if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
				return err
			}
		}
		result, err = s.showIssueInTx(ctx, tx, path)
		return err
	})
	if err != nil {
		return IssueRecord{}, err
	}
	return result, nil
}

// generatedIssuePath prefers the native Issue ID. A Memory or deleted Bead can
// independently reserve that canonical path, so only that rare collision gets
// a new suffix; the native Issue ID and its counter/hash allocation stay put.
func generatedIssuePath(ctx context.Context, tx *sql.Tx, issueID string) (string, error) {
	base := "beads/" + issueID
	for attempt := 0; attempt < 16; attempt++ {
		path := base
		if attempt != 0 {
			token, err := freshToken()
			if err != nil {
				return "", err
			}
			path += "-" + token
		}
		if err := validatePath(path); err != nil {
			return "", err
		}
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM graph_preview_catalog WHERE path=?`, path).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", ErrAlreadyExists
}

// Read dispatches the immutable backing selection and reads its authoritative
// state in one snapshot. Unsupported resource families remain explicit errors.
func (s *Store) Read(ctx context.Context, path string) (any, error) {
	if err := s.validateResourceReadAddress(path); err != nil {
		return nil, err
	}
	var result any
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		path, err = s.resolveResourceReadAddressInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		result, err = s.readCurrentVerifiedInTx(ctx, tx, path)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ReadBeadSelected uses the shared native Issue selector for bare spelling,
// then reads a non-Issue Bead at that exact path in the same checked snapshot.
// An explicit graph path never resolves through a native ID. Ambiguous bare
// spelling refuses even when a Memory exists at the fallback path.
func (s *Store) ReadBeadSelected(ctx context.Context, selector IssueSelector) (any, error) {
	if err := validateIssueSelector(selector); err != nil {
		return nil, err
	}
	var result any
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := s.checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		result, err = s.readBeadSelectedInTx(ctx, tx, selector)
		return err
	})
	return result, err
}

func (s *Store) readBeadSelectedInTx(ctx context.Context, tx *sql.Tx, selector IssueSelector) (any, error) {
	if selector.NativeID != "" {
		_, issue, err := s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
		switch {
		case err == nil:
			return issue, nil
		case errors.Is(err, ErrNotFound), errors.Is(err, errNonIssueSelector):
			// Bare Memory names retain their existing read spelling.
		default:
			return nil, err
		}
	}
	return s.readCurrentVerifiedInTx(ctx, tx, selector.Path)
}

func (s *Store) readCurrentVerifiedInTx(ctx context.Context, tx *sql.Tx, path string) (any, error) {
	var backing string
	if err := tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=?`, path).Scan(&backing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	switch backing {
	case "generic":
		return s.showMemoryInTx(ctx, tx, path)
	case "issue":
		return s.showIssueInTx(ctx, tx, path)
	case "dependency", "informational":
		return s.showLinkInTx(ctx, tx, path)
	default:
		return nil, fmt.Errorf("%w: unsupported backing", ErrInvalidStore)
	}
}

func (s *Store) ShowIssue(ctx context.Context, path string) (IssueRecord, error) {
	if err := validatePath(path); err != nil {
		return IssueRecord{}, err
	}
	var result IssueRecord
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		result, err = s.showIssueInTx(ctx, tx, path)
		return err
	})
	if err != nil {
		return IssueRecord{}, err
	}
	return result, nil
}

func (s *Store) showIssueInTx(ctx context.Context, tx *sql.Tx, path string) (IssueRecord, error) {
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return IssueRecord{}, err
	}
	var kind, typ, revision, state, backing, issueID string
	err = tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key
        FROM graph_preview_catalog WHERE path=?`, path).Scan(&kind, &typ, &revision, &state, &backing, &issueID)
	if errors.Is(err, sql.ErrNoRows) {
		return IssueRecord{}, ErrNotFound
	}
	if err != nil {
		return IssueRecord{}, err
	}
	if state == "deleted" {
		if err := s.validateDeletedIssueInTx(ctx, tx, path); err != nil {
			return IssueRecord{}, err
		}
		return IssueRecord{}, ErrGone
	}
	if kind != "bead" || !nativeIssueTypeMatches(s.ScopeURL(), typ, issueType) || !authorityID.MatchString(revision) || state != "live" || backing != "issue" || issueID == "" {
		return IssueRecord{}, fmt.Errorf("%w: invalid Issue allocation", ErrInvalidStore)
	}
	if err := s.requireNativeBindingInTx(ctx, tx, typ); err != nil {
		return IssueRecord{}, err
	}
	// Comments are a separate native Issue feed, outside durable_state and its
	// retained graph version. Including them here would make a new comment look
	// like an unversioned mutation of the Issue snapshot.
	issue, err := issueops.HydrateIssueOperationResult(ctx, tx, issueID, false)
	if err != nil {
		return IssueRecord{}, fmt.Errorf("%w: Issue backing: %v", ErrInvalidStore, err)
	}
	actualType, err := NativeIssueTypeURL(s.ScopeURL(), string(issue.IssueType))
	if err != nil || typ != actualType {
		return IssueRecord{}, fmt.Errorf("%w: Issue classification and declared Type differ", ErrInvalidStore)
	}
	var snapshot, retainedOwned, revisionContext []byte
	var actor, status string
	var at time.Time
	var ordinal, current int64
	err = tx.QueryRowContext(ctx, `SELECT m.issue_revision,v.durable_state,v.change_actor,v.attribution_status,v.change_at,i.current_revision,m.owned,m.revision_context
        FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision
        JOIN issues i ON i.id=m.issue_id WHERE m.path=? AND m.version=? AND m.issue_id=?`, path, revision, issueID).Scan(&ordinal, &snapshot, &actor, &status, &at, &current, &retainedOwned, &revisionContext)
	if err != nil {
		return IssueRecord{}, fmt.Errorf("%w: Issue retained mapping: %v", ErrInvalidStore, err)
	}
	// Lease liveness is node-local and deliberately unversioned. Compare the
	// durable Issue against its retained version while preserving the live lease
	// fields in the current projection. A heartbeat must not mint History.
	var retainedIssue types.Issue
	if err := json.Unmarshal(snapshot, &retainedIssue); err != nil {
		return IssueRecord{}, fmt.Errorf("%w: malformed retained Issue", ErrInvalidStore)
	}
	// A claimed Issue can arrive on another replica without its granting node's
	// ignored leases row. Native heartbeat may re-arm that lease locally; neither
	// the missing row nor the later refresh changes durable Issue History.
	comparison := *issue
	comparison.LeaseExpiresAt = retainedIssue.LeaseExpiresAt
	comparison.HeartbeatAt = retainedIssue.HeartbeatAt
	comparison.LeaseGrantedNode = retainedIssue.LeaseGrantedNode
	canonical, err := canonicalJSON(&comparison)
	if err != nil {
		return IssueRecord{}, err
	}
	if current != ordinal || ordinal < 1 || !bytes.Equal(snapshot, canonical) || issueops.IsWisp(issue) || issue.ID != issueID ||
		(actor == "" && status != "unknown") || (actor != "" && status != "claimed") {
		return IssueRecord{}, fmt.Errorf("%w: Issue current/retained state differs or exceeds preview", ErrInvalidStore)
	}
	owned, err := s.ownedLinksInTx(ctx, tx, issueID)
	if err != nil {
		return IssueRecord{}, err
	}
	canonicalOwned, err := canonicalJSON(owned)
	if err != nil {
		return IssueRecord{}, err
	}
	if !bytes.Equal(retainedOwned, canonicalOwned) {
		return IssueRecord{}, fmt.Errorf("%w: Issue owned state differs from retained version", ErrInvalidStore)
	}
	// The graph projection expresses dependencies once, canonically.
	// Jim's authoritative Issue snapshot still retains its original domain shape.
	metadata, err := commonMetadata(issue.Metadata)
	if err != nil {
		return IssueRecord{}, fmt.Errorf("%w: invalid Issue metadata: %v", ErrInvalidStore, err)
	}
	issue.Dependencies = nil
	issue.Metadata = nil
	attribution, err := applyIssueRevisionContext(Attribution{Actor: actor, Status: status, RecordedAt: at.UTC().Format(time.RFC3339Nano)}, revisionContext)
	if err != nil {
		return IssueRecord{}, err
	}
	publicURL, err := s.publicResourceURLInTx(ctx, tx, path)
	if err != nil {
		return IssueRecord{}, err
	}
	return IssueRecord{ID: publicURL, Type: typ, Revision: revision, Version: revision,
		Properties: issue, Metadata: metadata, Owned: owned, Attribution: attribution}, nil
}

// The installed Type set, already fingerprint-checked by checkBinding, uses
// one Issue allocation identity. Older preview stores require a fresh init.
func installedIssueTypeURLInTx(ctx context.Context, tx *sql.Tx, scope string) (string, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_types`).Scan(&count); err != nil {
		return "", err
	}
	if count < len(previewTypeDefinitions()) || count > len(previewTypeDefinitions())+PreviewSnapshotLimit {
		return "", fmt.Errorf("%w: unsupported Type installation", ErrInvalidStore)
	}
	return IssueTypeURL(scope), nil
}

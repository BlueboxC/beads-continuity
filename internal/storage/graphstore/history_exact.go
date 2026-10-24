package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/typecatalog"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// ErrVersionUnknown means this subject has no available retained mapping for the token.
// It does not assert pruning, erasure, replacement, or non-participation.
var ErrVersionUnknown = errors.New("graph preview retained version is unknown")

// PreviewVersionTokenLimit is an input byte budget, not a token spelling rule.
const PreviewVersionTokenLimit = 4096

// ReadVersion resolves an exact retained preview address without joining current
// payloads or current owned Links. It supplies no ordering, lineage, synthesized
// metadata, or complete public History capability. A private deletion token is
// not a retained Link Resource and returns ErrGone. A missing known component
// is ErrInvalidStore; the HTTP History diagnosis/authorization policy is separate.
// Issue fields excluded from Jim's JSON snapshot (including the current row-lock
// token and content hash) remain zero; they cannot be recovered from current state.
func (s *Store) ReadVersion(ctx context.Context, path, version string) (any, error) {
	if err := s.validateResourceReadAddress(path); err != nil {
		return nil, err
	}
	if err := validateVersionToken(version); err != nil {
		return nil, err
	}
	var result any
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		path, err = s.resolveResourceReadAddressInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		result, err = s.readVersionInTx(ctx, tx, path, version)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validateVersionToken(version string) error {
	if version == "" || !utf8.ValidString(version) || len(version) > PreviewVersionTokenLimit {
		return fmt.Errorf("%w: revision must be nonempty UTF-8 within %d bytes", graph.ErrValidation, PreviewVersionTokenLimit)
	}
	return nil
}

// readVersionInTx uses the caller's authority-checked transaction and validated
// path/token. It never joins current payloads into retained state.
func (s *Store) readVersionInTx(ctx context.Context, tx *sql.Tx, path, version string) (any, error) {
	original, originalKind, found, originalErr := s.originalVersionInTx(ctx, tx, path, version)
	if originalErr != nil {
		return nil, originalErr
	}
	if found {
		return decodeOriginalRecord(original.Snapshot, originalKind)
	}

	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return nil, err
	}
	var backing, kind, state, head string
	var typ, key sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT backing,resource_kind,allocation_state,revision,
 CASE WHEN OCTET_LENGTH(type_url)<=? THEN type_url ELSE NULL END,backing_key
 FROM graph_preview_catalog WHERE path=?`, len(s.ScopeURL())+512, path).Scan(&backing, &kind, &state, &head, &typ, &key)
	if errors.Is(err, sql.ErrNoRows) {
		var retained int
		if err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM graph_preview_versions WHERE path=?) +
 (SELECT COUNT(*) FROM graph_preview_issue_versions WHERE path=?)`, path, path).Scan(&retained); err != nil {
			return nil, err
		}
		if retained != 0 {
			return nil, fmt.Errorf("%w: retained subject lacks allocation", ErrInvalidStore)
		}
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !typ.Valid || !authorityID.MatchString(head) || (state != "live" && state != "deleted") ||
		(kind == "bead") != strings.HasPrefix(path, "beads/") || (kind != "bead" && kind != "link") {
		return nil, fmt.Errorf("%w: invalid retained subject allocation", ErrInvalidStore)
	}
	if state == "deleted" {
		if backing == "generic" {
			if err := checkDeletedMemoryReadBytes(ctx, tx, path, head, version); err != nil {
				return nil, err
			}
			memory, terminal, err := s.deletedMemoryStateInTx(ctx, tx, path)
			if err != nil {
				return nil, err
			}
			if version == head {
				if terminal != nil {
					return nil, ErrGone
				}
				return memory, nil
			}
			if version == memory.Version {
				return memory, nil
			}
		} else if backing == "issue" {
			if err := s.validDeletedIssueAllocationInTx(ctx, tx, path, kind, typ.String, head, state, backing, key); err != nil {
				return nil, err
			}
			if err := checkNativeIssueExactReadBytes(ctx, tx, path, head, version); err != nil {
				return nil, err
			}
			terminal, _, err := s.issueTerminalInTx(ctx, tx, path, head)
			if err != nil {
				return nil, err
			}
			if terminal != nil && version == head {
				return nil, ErrGone
			}
		} else if !s.validDeletedLinkAllocation(kind, typ.String, backing, key, issueType) {
			return nil, fmt.Errorf("%w: unsupported deleted subject", ErrInvalidStore)
		}
	}
	if backing == "issue" {
		if kind != "bead" || !nativeIssueTypeMatches(s.ScopeURL(), typ.String, issueType) || !key.Valid || key.String == "" {
			return nil, fmt.Errorf("%w: invalid Issue allocation", ErrInvalidStore)
		}
		return s.readNativeIssueRetainedInTx(ctx, tx, path, version, head, key.String)
	}
	dependencyType := isNativeDependencyLinkType(s.ScopeURL(), issueType, typ.String)
	if !((backing == "generic" && kind == "bead" && s.isMemoryType(typ.String)) ||
		(backing == "informational" && kind == "link" && IsInformationalTypeURL(s.ScopeURL(), typ.String)) ||
		(backing == "dependency" && kind == "link" && dependencyType)) {
		return nil, fmt.Errorf("%w: unsupported retained allocation", ErrInvalidStore)
	}
	if backing == "informational" {
		if err := s.informationalTypeInTx(ctx, tx, typ.String); err != nil {
			return nil, err
		}
	}
	raw, actor, err := readVersionBytes(ctx, tx, path, version, head)
	if err != nil {
		return nil, err
	}
	if kind == "link" {
		var marker struct {
			State string `json:"state"`
		}
		if json.Unmarshal(raw, &marker) == nil && marker.State == "deleted" {
			if _, err := s.decodeLinkTerminal(ctx, tx, path, typ.String, version, raw, actor); err != nil {
				return nil, err
			}
			return nil, ErrGone
		}
	}
	if kind == "link" {
		var link LinkRecord
		expectedID, err := s.publicResourceURLInTx(ctx, tx, path)
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &link) != nil || link.ID != expectedID || link.Type != typ.String ||
			link.Revision != version || link.Version != version {
			return nil, fmt.Errorf("%w: retained Link identity differs", ErrInvalidStore)
		}
		if err := s.validateVersionLink(ctx, tx, link, raw, actor); err != nil {
			return nil, err
		}
		return link, nil
	}
	expectedType := ""
	if version == head {
		expectedType = typ.String
	}
	return s.decodeExactMemoryVersion(ctx, tx, path, expectedType, version, raw, actor)
}

func (s *Store) readMemoryVersionInTx(ctx context.Context, tx *sql.Tx, path, typ, version, head string) (Record, error) {
	if original, kind, found, err := s.originalVersionInTx(ctx, tx, path, version); err != nil {
		return Record{}, err
	} else if found {
		value, err := decodeOriginalRecord(original.Snapshot, kind)
		if err != nil {
			return Record{}, err
		}
		record, ok := value.(Record)
		if !ok {
			return Record{}, ErrCapabilityUnavailable
		}
		return record, nil
	}
	raw, actor, err := readVersionBytes(ctx, tx, path, version, head)
	if err != nil {
		return Record{}, err
	}
	return s.decodeExactMemoryVersion(ctx, tx, path, typ, version, raw, actor)
}

func (s *Store) decodeExactMemoryVersion(ctx context.Context, tx *sql.Tx, path, typ, version string, raw []byte, actor string) (Record, error) {
	// Terminal evidence stays terminal after recovery and is never a source.
	var marker struct {
		State string `json:"state"`
	}
	if json.Unmarshal(raw, &marker) == nil && marker.State != "" {
		if _, err := s.decodeMemoryTerminal(ctx, tx, path, version, raw, actor); err != nil {
			return Record{}, err
		}
		return Record{}, ErrGone
	}
	return s.decodeMemoryVersion(ctx, tx, path, typ, version, raw, actor)
}

func (s *Store) decodeMemoryVersion(ctx context.Context, tx *sql.Tx, path, typ, version string, raw []byte, actor string) (Record, error) {
	expectedID, err := s.publicResourceURLInTx(ctx, tx, path)
	if err != nil {
		return Record{}, err
	}
	var memory Record
	if json.Unmarshal(raw, &memory) != nil || !sameVersionJSON(raw, memory) || memory.ID != expectedID ||
		!s.isMemoryType(memory.Type) || (typ != "" && memory.Type != typ) || memory.Revision != version || memory.Version != version ||
		!utf8.ValidString(memory.Properties.Title) || !utf8.ValidString(memory.Properties.Body) ||
		!validVersionAttribution(memory.Attribution, actor) {
		return Record{}, fmt.Errorf("%w: invalid retained Memory", ErrInvalidStore)
	}
	metadata, err := commonMetadata(memory.Metadata)
	if err != nil || !bytes.Equal(metadata, memory.Metadata) {
		return Record{}, fmt.Errorf("%w: invalid retained Memory metadata", ErrInvalidStore)
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return Record{}, err
	}
	properties, err := canonicalJSON(memory.Properties)
	if err != nil {
		return Record{}, err
	}
	if err := catalog.validateProperties(memory.Type, properties, graph.KindBead); err != nil {
		return Record{}, fmt.Errorf("%w: retained original Type payload: %v", ErrInvalidStore, err)
	}
	if err := s.validateVersionOwned(ctx, tx, memory.ID, memory.Type, memory.Owned); err != nil {
		return Record{}, err
	}
	memory.PublicID, err = memoryPublicIDInTx(ctx, tx, path)
	if err != nil {
		return Record{}, err
	}
	return memory, nil
}

func readVersionBytes(ctx context.Context, tx *sql.Tx, path, version, head string) ([]byte, string, error) {
	var size uint64
	err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor)+COALESCE((SELECT OCTET_LENGTH(public_id) FROM graph_preview_catalog WHERE path=graph_preview_versions.path),0)
 FROM graph_preview_versions WHERE path=? AND version=?`, path, version).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		if version == head {
			return nil, "", fmt.Errorf("%w: current retained version is missing", ErrInvalidStore)
		}
		return nil, "", ErrVersionUnknown
	}
	if err != nil {
		return nil, "", err
	}
	if size > PreviewCurrentReadByteLimit {
		return nil, "", fmt.Errorf("%w: retained version exceeds acquisition budget", ErrLimitExceeded)
	}
	var raw []byte
	var actor string
	err = tx.QueryRowContext(ctx, `SELECT snapshot,actor FROM graph_preview_versions WHERE path=? AND version=?`, path, version).Scan(&raw, &actor)
	return raw, actor, err
}

func sameVersionJSON(raw []byte, value any) bool {
	canonical, err := canonicalJSON(value)
	return err == nil && bytes.Equal(raw, canonical)
}

func validVersionAttribution(value Attribution, actor string) bool {
	if h := value.HistoricalUpdate; h != nil {
		if validateVersionToken(h.FromRevision) != nil || validateVersionToken(h.PreviousRevision) != nil {
			return false
		}
	}
	at, err := time.Parse(time.RFC3339Nano, value.RecordedAt)
	return err == nil && at.UTC().Format(time.RFC3339Nano) == value.RecordedAt && utf8.ValidString(actor) &&
		value.Actor == actor && ((actor == "" && value.Status == "unknown") || (actor != "" && value.Status == "claimed"))
}

func (s *Store) validateVersionLink(ctx context.Context, tx *sql.Tx, link LinkRecord, raw []byte, actor string) error {
	metadata, metadataErr := commonMetadata(link.Metadata)
	if metadataErr != nil || !bytes.Equal(metadata, link.Metadata) {
		return fmt.Errorf("%w: invalid retained Link metadata", ErrInvalidStore)
	}
	_, identityErr := s.publicResourcePathInTx(ctx, tx, link.ID, graph.KindLink)
	if identityErr != nil || !authorityID.MatchString(link.Revision) ||
		link.Version != link.Revision || !sameVersionJSON(raw, link) || !validVersionAttribution(link.Attribution, actor) {
		return fmt.Errorf("%w: invalid retained Link", ErrInvalidStore)
	}
	if IsInformationalTypeURL(s.ScopeURL(), link.Type) {
		if err := s.informationalTypeInTx(ctx, tx, link.Type); err != nil {
			return err
		}
		return s.validateRetainedInformational(ctx, tx, link, raw, actor)
	}
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return err
	}
	if link.SourceRevision != "" || link.TargetRevision != "" {
		return fmt.Errorf("%w: native Dependencies cannot carry endpoint pins", ErrInvalidStore)
	}
	depType, hasKind := link.Properties["dep_type"].(string)
	nativeID, hasID := link.Properties["nativeId"].(string)
	thread, hasThread := link.Properties["threadId"].(string)
	expectedType, typeErr := NativeDependencyTypeURL(s.ScopeURL(), depType)
	if !isNativeDependencyLinkType(s.ScopeURL(), issueType, link.Type) ||
		!hasKind || !hasID || nativeID == "" || !utf8.ValidString(nativeID) || !hasThread || !utf8.ValidString(thread) || len(thread) > 255 || typeErr != nil || link.Type != expectedType || len(link.Properties) != 3 || !types.DependencyType(depType).IsValid() ||
		!utf8.ValidString(depType) {
		return fmt.Errorf("%w: invalid retained Dependency", ErrInvalidStore)
	}
	if err := s.requireNativeBindingInTx(ctx, tx, link.Type); err != nil {
		return err
	}
	for _, endpoint := range []string{link.Source, link.Target} {
		if _, err := s.publicResourcePathInTx(ctx, tx, endpoint, graph.KindBead); err != nil {
			return fmt.Errorf("%w: invalid retained endpoint", ErrInvalidStore)
		}
	}
	return nil
}

func (s *Store) validateVersionOwned(ctx context.Context, tx *sql.Tx, source, ownerType string, owned []json.RawMessage) error {
	if owned == nil || len(owned) > PreviewOwnedLinkLimit {
		return fmt.Errorf("%w: invalid retained owned set", ErrInvalidStore)
	}
	// Retained membership uses the owner's original declared Type, including
	// positive family ownership. Native payload validation stays native.
	memoryOwner := s.isMemoryType(ownerType)
	if !memoryOwner && !isConcreteNativeIssue(s.ScopeURL(), ownerType) {
		return fmt.Errorf("%w: unsupported retained owner Type", ErrInvalidStore)
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return err
	}
	previous := ""
	for _, raw := range owned {
		var link LinkRecord
		if json.Unmarshal(raw, &link) != nil {
			return fmt.Errorf("%w: invalid retained owned membership", ErrInvalidStore)
		}
		owned, err := catalog.types.Owns(typecatalog.Reference{URI: nativeFamilyReference(s.ScopeURL(), ownerType)}, typecatalog.Reference{URI: nativeFamilyReference(s.ScopeURL(), link.Type)})
		if err != nil || !owned || link.Source != source ||
			(previous != "" && graph.CompareCodeUnits(previous, link.ID) >= 0) {
			return fmt.Errorf("%w: invalid retained owned membership", ErrInvalidStore)
		}
		if err := s.validateVersionLink(ctx, tx, link, raw, link.Attribution.Actor); err != nil {
			return err
		}
		previous = link.ID
	}
	return nil
}

func (s *Store) readIssueVersionInTx(ctx context.Context, tx *sql.Tx, path, version, head, backingKey string) (IssueRecord, error) {
	var issueID string
	var ordinal int64
	var ownedSize uint64
	err := tx.QueryRowContext(ctx, `SELECT issue_id,issue_revision,OCTET_LENGTH(owned)+COALESCE(OCTET_LENGTH(revision_context),0)
 FROM graph_preview_issue_versions WHERE path=? AND version=?`, path, version).Scan(&issueID, &ordinal, &ownedSize)
	if errors.Is(err, sql.ErrNoRows) {
		if version == head {
			return IssueRecord{}, fmt.Errorf("%w: current Issue retained mapping is missing", ErrInvalidStore)
		}
		return IssueRecord{}, ErrVersionUnknown
	}
	if err != nil {
		return IssueRecord{}, err
	}
	if ordinal < 1 || issueID != backingKey {
		return IssueRecord{}, fmt.Errorf("%w: invalid Issue retained mapping", ErrInvalidStore)
	}
	var bodySize uint64
	var removedAt sql.NullTime
	var removedReason, removedRestriction sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(durable_state)+COALESCE(OCTET_LENGTH(change_actor),0)+COALESCE(OCTET_LENGTH(attribution_status),0),removed_at,removed_reason,removed_restriction
 FROM issue_versions WHERE issue_id=? AND revision=?`, issueID, ordinal).Scan(&bodySize, &removedAt, &removedReason, &removedRestriction)
	if errors.Is(err, sql.ErrNoRows) {
		return IssueRecord{}, fmt.Errorf("%w: mapped Issue retained body is missing", ErrInvalidStore)
	}
	if err != nil {
		return IssueRecord{}, err
	}
	// A retained graph mapping does not authorize a native row marked removed.
	// Match retained-reference admission and report the selected row's recorded
	// restriction/reason, without hydrating it or falling back to another row.
	if removedAt.Valid {
		return IssueRecord{}, fmt.Errorf("%w: %s: %s", ErrVersionUnknown, issueops.NormalizeRemovedRestriction(removedRestriction.String), removedReason.String)
	}
	if ownedSize > PreviewCurrentReadByteLimit || bodySize > PreviewCurrentReadByteLimit-ownedSize {
		return IssueRecord{}, fmt.Errorf("%w: retained Issue exceeds acquisition budget", ErrLimitExceeded)
	}
	var raw, owned, revisionContext []byte
	var actor, status sql.NullString
	var at time.Time
	err = tx.QueryRowContext(ctx, `SELECT v.durable_state,v.change_actor,v.attribution_status,v.change_at,m.owned,m.revision_context
 FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision
 WHERE m.path=? AND m.version=?`, path, version).Scan(&raw, &actor, &status, &at, &owned, &revisionContext)
	if err != nil {
		return IssueRecord{}, err
	}
	var issue publicops.Issue
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return IssueRecord{}, err
	}
	publicURL, err := s.publicResourceURLInTx(ctx, tx, path)
	if err != nil {
		return IssueRecord{}, err
	}
	result := IssueRecord{ID: publicURL, Type: issueType, Revision: version, Version: version,
		Attribution: Attribution{Actor: actor.String, Status: status.String, RecordedAt: at.UTC().Format(time.RFC3339Nano)}}
	result.Attribution, err = applyIssueRevisionContext(result.Attribution, revisionContext)
	if err != nil {
		return IssueRecord{}, err
	}
	if json.Unmarshal(raw, &issue) != nil || !sameVersionJSON(raw, &issue) || issue.ID != issueID ||
		json.Unmarshal(owned, &result.Owned) != nil || !sameVersionJSON(owned, result.Owned) ||
		!validVersionAttribution(result.Attribution, actor.String) {
		return IssueRecord{}, fmt.Errorf("%w: invalid retained Issue state", ErrInvalidStore)
	}
	result.Type, err = NativeIssueTypeURL(s.ScopeURL(), string(issue.IssueType))
	if err != nil {
		return IssueRecord{}, fmt.Errorf("%w: retained Issue classification: %v", ErrInvalidStore, err)
	}
	if err := s.requireNativeBindingInTx(ctx, tx, result.Type); err != nil {
		return IssueRecord{}, err
	}
	if err := s.validateVersionOwned(ctx, tx, result.ID, result.Type, result.Owned); err != nil {
		return IssueRecord{}, err
	}
	// Match the current graph projection without duplicating Jim's domain body.
	metadata, err := commonMetadata(issue.Metadata)
	if err != nil {
		return IssueRecord{}, fmt.Errorf("%w: invalid retained Issue metadata: %v", ErrInvalidStore, err)
	}
	issue.Dependencies = nil
	issue.Metadata = nil
	result.Properties = &issue
	result.Metadata = metadata
	return result, nil
}

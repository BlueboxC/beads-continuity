package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// RemoveDependency is the Issue pair form of the native dep remove verb. It
// selects only a native blocks-kind Link in the same write transaction as
// the canonical unlink/Issue writer. An absent pair is a true no-op: no event,
// retained version, tombstone, or coordination write is produced. The generic
// unlink command keeps its explicit canonical-Link-ID and guard contract.
func (s *Store) RemoveDependency(ctx context.Context, request DependencyRemoveRequest) (DependencyRemoveResult, error) {
	for _, endpoint := range []struct{ path, id string }{{request.SourcePath, request.SourceNativeID}, {request.TargetPath, request.TargetNativeID}} {
		if err := s.validateBeadEndpointOperand(endpoint.path, endpoint.id); err != nil {
			return DependencyRemoveResult{}, err
		}
	}
	if request.Actor == "" || !utf8.ValidString(request.Actor) {
		return DependencyRemoveResult{}, fmt.Errorf("%w: remove dependency requires a UTF-8 actor", storage.ErrValidation)
	}
	var result DependencyRemoveResult
	err := s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		sourcePath, source, err := s.resolveDependencyRemoveIssueInTx(ctx, tx, request.SourcePath, request.SourceNativeID)
		if err != nil {
			return err
		}
		targetPath, target, err := s.resolveDependencyRemoveIssueInTx(ctx, tx, request.TargetPath, request.TargetNativeID)
		if err != nil {
			return err
		}
		result.Source, result.Target = source, target
		blockingType, ok := nativeDependencyTypeURLForIssueType(s.ScopeURL(), source.Type, types.DepBlocks)
		if !ok || target.Type != source.Type {
			return fmt.Errorf("%w: Issue pair has no shared installed blocking Type", ErrInvalidStore)
		}
		path, err := s.selectUnlinkPathInTx(ctx, tx, LinkDeleteRequest{
			SourcePath: sourcePath, TargetPath: targetPath, TypeURL: blockingType,
		})
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		link, err := s.showLinkInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		if link.Type != blockingType {
			return fmt.Errorf("%w: selected pair is not a Dependency", ErrInvalidStore)
		}
		// The single nominal Dependency Type also includes parent-child and
		// user-defined kinds. This familiar pair verb remains blocks-only;
		// those Links are deleted explicitly by canonical Link ID.
		if link.Properties["dep_type"] != string(types.DepBlocks) {
			return fmt.Errorf("%w: pair-level dep removal supports only blocks; delete this Dependency by canonical Link ID", ErrCapabilityUnavailable)
		}
		removed, err := s.unlinkDependencyInTx(ctx, tx, path, link, LinkDeleteRequest{
			Actor: request.Actor, ExpectedRevision: link.Revision, ExpectedSourceRevision: source.Revision,
		})
		if err != nil {
			return err
		}
		current, ok := removed.Source.(IssueRecord)
		if !ok || !removed.Changed {
			return fmt.Errorf("%w: blocking writer returned no changed Issue", ErrInvalidStore)
		}
		result.Removed, result.Source, result.Link = true, current, removed.Link
		return nil
	})
	if err != nil {
		return DependencyRemoveResult{}, err
	}
	return result, nil
}

// Keep removal's Issue selection in its write transaction. A local spelling
// is a public ID even when a different Bead occupies its candidate path;
// only a canonical Scope URL selects that graph path directly.
func (s *Store) resolveDependencyRemoveIssueInTx(ctx context.Context, tx *sql.Tx, candidatePath, nativeID string) (string, IssueRecord, error) {
	return s.resolveLiveIssueSelectorInTx(ctx, tx, IssueSelector{Path: candidatePath, NativeID: nativeID})
}

// validDeletedLinkAllocation separates reserved canonical identity from current
// authority. Dependency row IDs are deterministic per pair; clearing the deleted
// allocation's backing key permits a later pair assertion under a NEW Link ID.
func (s *Store) validDeletedLinkAllocation(kind, typ, backing string, key sql.NullString, issueType string) bool {
	return kind == "link" && !key.Valid &&
		((backing == "informational" && IsInformationalTypeURL(s.ScopeURL(), typ)) ||
			(backing == "dependency" && isNativeDependencyLinkType(s.ScopeURL(), issueType, typ)))
}

// unlinkDependencyInTx extends the existing guarded unlink transaction only for
// a checked live canonical native Dependency Link. The Issue-domain removal remains the
// sole writer of Dependency rows and derived readiness state.
func (s *Store) unlinkDependencyInTx(ctx context.Context, tx *sql.Tx, path string, link LinkRecord, request LinkDeleteRequest) (LinkDeleteResult, error) {
	if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, link.Revision, true, "Link"); err != nil {
		return LinkDeleteResult{}, err
	}
	sourcePath, err := s.publicResourcePathInTx(ctx, tx, link.Source, graph.KindBead)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	targetPath, err := s.publicResourcePathInTx(ctx, tx, link.Target, graph.KindBead)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	source, err := s.requireIssueInTx(ctx, tx, sourcePath)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	target, err := s.requireIssueInTx(ctx, tx, targetPath)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	// Issue informational Links remain unowned. This blocking Type is owned,
	// so the source guard is required independently of beadEndpointInTx's flag.
	if err := checkRevisionGuard(request.ExpectedSourceRevision, request.UnconditionalSource, source.Revision, true, "source"); err != nil {
		return LinkDeleteResult{}, err
	}
	removal := publicops.RemoveDependencyRequest{Actor: request.Actor, IssueID: source.Properties.ID, DependsOnID: target.Properties.ID}
	if err := issueops.ValidateRemoveDependencyRequest(removal); err != nil {
		return LinkDeleteResult{}, err
	}
	// A specialized Dependency must not also have a generic Bead or Link payload.
	var payloads int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM graph_preview_links WHERE path=?) + (SELECT COUNT(*) FROM graph_preview_payloads WHERE path=?)`, path, path).Scan(&payloads); err != nil {
		return LinkDeleteResult{}, err
	}
	if payloads != 0 {
		return LinkDeleteResult{}, fmt.Errorf("%w: Dependency has a generic payload", ErrInvalidStore)
	}
	if err := s.touchCoordination(ctx, tx); err != nil {
		return LinkDeleteResult{}, err
	}
	unscope := issueops.ScopeVersionedHistoryTransaction(tx, true)
	defer unscope()
	removed, _, err := issueops.ExecuteRemoveDependency(ctx, tx, removal)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	if !removed.Removed {
		return LinkDeleteResult{}, fmt.Errorf("%w: checked live Dependency disappeared", ErrInvalidStore)
	}
	if err := s.afterStage("dependency-remove"); err != nil {
		return LinkDeleteResult{}, err
	}
	revision, err := freshToken()
	if err != nil {
		return LinkDeleteResult{}, err
	}
	attribution := writeAttribution(request.Actor)
	attribution.Inception = link.Attribution.Inception
	tombstone := LinkTombstone{ID: link.ID, Type: link.Type, Revision: revision, Version: revision, State: "deleted", PreviousVersion: link.Version, Attribution: attribution}
	if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET allocation_state='deleted',revision=?,backing_key=NULL WHERE path=?`, revision, path); err != nil {
		return LinkDeleteResult{}, err
	}
	if err := s.afterStage("link-catalog"); err != nil {
		return LinkDeleteResult{}, err
	}
	snapshot, err := canonicalJSON(tombstone)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	if err := insertPreviewVersionInTx(ctx, tx, path, revision, snapshot, request.Actor); err != nil {
		return LinkDeleteResult{}, err
	}
	if err := s.afterStage("link-retained"); err != nil {
		return LinkDeleteResult{}, err
	}
	// The target shared writer owns the one native retained version.
	if err := s.afterStage("issue-retained"); err != nil {
		return LinkDeleteResult{}, err
	}
	if err := s.recordIssueMappingInTx(ctx, tx, sourcePath, source.Properties.ID, request.Actor); err != nil {
		return LinkDeleteResult{}, err
	}
	current, err := s.showIssueInTx(ctx, tx, sourcePath)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	return LinkDeleteResult{Link: tombstone, Source: current, Changed: true}, nil
}

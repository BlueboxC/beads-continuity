package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/workapi"
)

// ShowIssueDetails projects a current graph Issue through the same detail
// assembler as ordinary bd show. The graph revision is the equality token
// accepted by graph --if-revision; native row_lock is never exposed here.
func (s *Store) ShowIssueDetails(ctx context.Context, path string, opts workapi.DetailOptions) (*types.IssueDetails, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	var details *types.IssueDetails
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		record, err := s.showIssueInTx(ctx, tx, path)
		if err != nil {
			return err
		}
		details, err = s.issueDetailsInTx(ctx, tx, record, opts)
		return err
	})
	return details, err
}

// ShowIssueDetailsSelected resolves a bare native Issue ID (including a unique
// abbreviation) and projects its detail view in the same checked snapshot.
// Explicit graph paths remain exact.
func (s *Store) ShowIssueDetailsSelected(ctx context.Context, selector IssueSelector, opts workapi.DetailOptions) (*types.IssueDetails, error) {
	if err := validateIssueSelector(selector); err != nil {
		return nil, err
	}
	var details *types.IssueDetails
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var record IssueRecord
		var err error
		if selector.NativeID == "" {
			_, record, err = s.resolveExactLiveIssueSelectorInTx(ctx, tx, selector)
		} else {
			_, record, err = s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
		}
		if err != nil {
			return err
		}
		details, err = s.issueDetailsInTx(ctx, tx, record, opts)
		return err
	})
	if errors.Is(err, errNonIssueSelector) {
		return nil, ErrCapabilityUnavailable
	}
	return details, err
}

// ReadBeadWithIssueDetailsSelected returns a current Bead and, when it is an
// Issue, its familiar detail projection from one checked read transaction.
// A Memory has no Issue details. The caller can choose a representation
// without re-resolving a bare selector in a second snapshot.
func (s *Store) ReadBeadWithIssueDetailsSelected(ctx context.Context, selector IssueSelector, opts workapi.DetailOptions) (any, *types.IssueDetails, error) {
	if err := validateIssueSelector(selector); err != nil {
		return nil, nil, err
	}
	var record any
	var details *types.IssueDetails
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		selected, err := s.readBeadSelectedInTx(ctx, tx, selector)
		if err != nil {
			return err
		}
		record = selected
		if issue, ok := selected.(IssueRecord); ok {
			details, err = s.issueDetailsInTx(ctx, tx, issue, opts)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return record, details, nil
}

// ReadResourceSelected resolves one current Resource without building the
// familiar Issue detail projection. Transport adapters use this to keep their
// dependency boundary and read only the canonical Resource record.
func (s *Store) ReadResourceSelected(ctx context.Context, path, localID string) (string, any, error) {
	selected, record, _, err := s.readResourceSelected(ctx, path, localID, workapi.DetailOptions{}, false)
	return selected, record, err
}

// ReadResourceWithIssueDetailsSelected resolves a local public ID across the
// currently supported Bead and Link backings in one checked snapshot. A
// canonical Scope URL supplies only an exact path. No local spelling is
// interpreted as a graph path, including beads/ and links/ prefixes.
func (s *Store) ReadResourceWithIssueDetailsSelected(ctx context.Context, path, localID string, opts workapi.DetailOptions) (string, any, *types.IssueDetails, error) {
	return s.readResourceSelected(ctx, path, localID, opts, true)
}

func (s *Store) readResourceSelected(ctx context.Context, path, localID string, opts workapi.DetailOptions, includeDetails bool) (string, any, *types.IssueDetails, error) {
	if localID == "" {
		if err := s.validateResourceReadAddress(path); err != nil {
			return "", nil, nil, err
		}
	} else if path != "beads/"+localID || validatePublicID(localID) != nil {
		return "", nil, nil, fmt.Errorf("%w: invalid local Resource ID", storage.ErrValidation)
	}
	var selectedPath string
	var record any
	var details *types.IssueDetails
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if localID == "" {
			var err error
			selectedPath, err = s.resolveResourceReadAddressInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			record, err = s.readCurrentVerifiedInTx(ctx, tx, selectedPath)
			if err != nil {
				return err
			}
		} else {
			// Memory and Link public IDs come from the immutable catalog. Legacy
			// suffix shorthand is confined to allocations without explicit public IDs;
			// a private path is never a second identity.
			exactNonIssuePath := ""
			rows, err := tx.QueryContext(ctx, `SELECT path FROM graph_preview_catalog WHERE backing IN ('generic','dependency','informational') AND COALESCE(public_id,path)=? AND allocation_state='live' LIMIT 2`, localID)
			if err != nil {
				return err
			}
			for rows.Next() {
				if exactNonIssuePath != "" {
					_ = rows.Close()
					return fmt.Errorf("%w: public ID has multiple live allocations", ErrInvalidStore)
				}
				if err := rows.Scan(&exactNonIssuePath); err != nil {
					_ = rows.Close()
					return err
				}
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			if exactNonIssuePath == "" {
				// Explicit public IDs remain authoritative through deletion;
				// old NULL-ID suffix compatibility retains its former policy.
				var deleted int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_catalog WHERE public_id=? AND allocation_state='deleted'`, localID).Scan(&deleted); err != nil {
					return err
				}
				if deleted > 1 {
					return fmt.Errorf("%w: duplicate deleted public IDs", ErrInvalidStore)
				}
				if deleted == 1 {
					return ErrGone
				}
				if validatePath(path) != nil || validateLinkPath("links/"+localID) != nil {
					return ErrNotFound
				}
			}

			issuePath, issue, issueErr := s.resolveExactLiveIssueSelectorInTx(ctx, tx, IssueSelector{Path: path, NativeID: localID})
			exactIssue := issueErr == nil
			if issueErr != nil && !errors.Is(issueErr, ErrNotFound) && !errors.Is(issueErr, errNonIssueSelector) {
				return issueErr
			}
			if issueErr == nil {
				if exactNonIssuePath != "" {
					return fmt.Errorf("%w: public ID %q identifies multiple Resources", storage.ErrValidation, localID)
				}
				selectedPath, record = issuePath, issue
			} else if exactNonIssuePath != "" {
				selectedPath = exactNonIssuePath
			} else {
				// Only after exact public identity misses may shipping Issue
				// abbreviations and old Bead/Link suffix shorthand participate.
				issuePath, issue, issueErr = s.resolveLiveIssueSelectorInTx(ctx, tx, IssueSelector{Path: path, NativeID: localID})
				if issueErr != nil && !errors.Is(issueErr, ErrNotFound) && !errors.Is(issueErr, errNonIssueSelector) {
					return issueErr
				}
				if issueErr == nil {
					selectedPath, record = issuePath, issue
				}
			}
			if exactNonIssuePath == "" && !exactIssue {
				for _, candidate := range []struct{ path, backing string }{{path, "generic"}, {"links/" + localID, "link"}} {
					var backing string
					err := tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=? AND allocation_state='live' AND public_id IS NULL`, candidate.path).Scan(&backing)
					if errors.Is(err, sql.ErrNoRows) {
						continue
					}
					if err != nil {
						return err
					}
					if candidate.backing == "generic" && backing != "generic" {
						continue
					}
					if candidate.backing == "link" && backing != "dependency" && backing != "informational" {
						continue
					}
					if selectedPath != "" {
						return fmt.Errorf("%w: local shorthand %q identifies multiple Resources; use a public ID or canonical Scope URL", storage.ErrValidation, localID)
					}
					selectedPath = candidate.path
				}
			}
			if selectedPath == "" {
				// A deleted public ID remains reserved. Report its terminal
				// state only after every live exact and compatibility candidate
				// has missed, so an old deleted Link cannot shadow a live Bead.
				var deleted int
				err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_catalog
					WHERE allocation_state='deleted' AND
					((backing='issue' AND backing_key=?) OR
					 (backing IN ('generic','dependency','informational') AND COALESCE(public_id,path)=?))`, localID, localID).Scan(&deleted)
				if err != nil {
					return err
				}
				if deleted > 1 {
					return fmt.Errorf("%w: public ID %q has multiple deleted allocations", ErrInvalidStore, localID)
				}
				if deleted == 1 {
					return ErrGone
				}
				return ErrNotFound
			}
			if record == nil {
				var err error
				record, err = s.readCurrentVerifiedInTx(ctx, tx, selectedPath)
				if err != nil {
					return err
				}
			}
		}
		if issue, ok := record.(IssueRecord); ok && includeDetails {
			var err error
			details, err = s.issueDetailsInTx(ctx, tx, issue, opts)
			return err
		}
		return nil
	})
	if err != nil {
		return "", nil, nil, err
	}
	return selectedPath, record, details, nil
}

func (s *Store) issueDetailsInTx(ctx context.Context, tx *sql.Tx, record IssueRecord, opts workapi.DetailOptions) (*types.IssueDetails, error) {
	if record.Properties == nil {
		return nil, fmt.Errorf("%w: Issue has no properties", ErrInvalidStore)
	}
	// Read the native Issue in this same verified snapshot. Its metadata
	// and labels keep their ordinary JSON representation; the graph
	// record's common metadata and separated owned Links are different
	// projections of the same state.
	issue, err := issueops.GetIssueInTx(ctx, tx, record.Properties.ID)
	if err != nil {
		return nil, err
	}
	details, err := workapi.BuildIssueDetails(ctx, graphIssueDetailSource{tx: tx}, issue, false, opts)
	if err != nil {
		return nil, err
	}
	details.Revision = record.Revision
	// Validate every Issue reference against this snapshot's live catalog.
	// Public Issue IDs stay literal in the familiar detail projection; the
	// graph record above still carries its exact canonical Scope URL.
	if err := s.projectIssueDetailIDsInTx(ctx, tx, details); err != nil {
		return nil, err
	}
	if details.ID != record.Properties.ID {
		return nil, fmt.Errorf("%w: Issue allocation changed during detail projection", ErrInvalidStore)
	}
	return details, nil
}

func (s *Store) projectIssueDetailIDsInTx(ctx context.Context, tx *sql.Tx, details *types.IssueDetails) error {
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return err
	}
	validated := map[string]bool{}
	validateNativeID := func(nativeID string) error {
		if validated[nativeID] {
			return nil
		}
		var path, kind, typ, state string
		err := tx.QueryRowContext(ctx, `SELECT path,resource_kind,type_url,allocation_state FROM graph_preview_catalog WHERE backing='issue' AND backing_key=?`, nativeID).Scan(&path, &kind, &typ, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: unmapped Issue detail ID", ErrInvalidStore)
		}
		if err != nil {
			return err
		}
		if kind != "bead" || !nativeIssueTypeMatches(s.ScopeURL(), typ, issueType) || state != "live" || validatePath(path) != nil {
			return fmt.Errorf("%w: invalid Issue detail allocation", ErrInvalidStore)
		}
		validated[nativeID] = true
		return nil
	}
	projectIssue := func(issue *types.Issue) error {
		return validateNativeID(issue.ID)
	}
	if err := projectIssue(&details.Issue); err != nil {
		return err
	}
	for _, relation := range [][]*types.IssueWithDependencyMetadata{details.Dependencies, details.Dependents} {
		for _, related := range relation {
			if related != nil {
				if err := projectIssue(&related.Issue); err != nil {
					return err
				}
			}
		}
	}
	if details.Parent != nil {
		if err := validateNativeID(*details.Parent); err != nil {
			return err
		}
	}
	for _, comment := range details.Comments {
		if comment != nil {
			if err := validateNativeID(comment.IssueID); err != nil {
				return err
			}
		}
	}
	return nil
}

// graphIssueDetailSource uses the native Issue read primitives in the same
// snapshot that verified the graph catalog and retained Issue head.
type graphIssueDetailSource struct{ tx *sql.Tx }

func (s graphIssueDetailSource) GetIssue(ctx context.Context, id string) (*types.Issue, error) {
	return issueops.GetIssueInTx(ctx, s.tx, id)
}

func (graphIssueDetailSource) GetWisp(_ context.Context, _ string) (*types.Issue, error) {
	return nil, storage.ErrNotFound
}

func (s graphIssueDetailSource) Labels(ctx context.Context, id string, _ bool) ([]string, error) {
	return issueops.GetLabelsInTx(ctx, s.tx, "labels", id)
}

func (s graphIssueDetailSource) Dependencies(ctx context.Context, id string, _ bool) ([]*types.IssueWithDependencyMetadata, error) {
	return issueops.GetDependenciesWithMetadataInTx(ctx, s.tx, id)
}

func (s graphIssueDetailSource) CountDependencies(ctx context.Context, id string, _ bool) (int64, error) {
	return issueops.CountDependencyEdgesInTx(ctx, s.tx, id, domain.DepDirectionOut, nil)
}

func (s graphIssueDetailSource) CountDependents(ctx context.Context, id string, _ bool) (int64, error) {
	return issueops.CountDependencyEdgesInTx(ctx, s.tx, id, domain.DepDirectionIn, nil)
}

func (s graphIssueDetailSource) CountComments(ctx context.Context, id string, _ bool) (int64, error) {
	counts, err := issueops.GetCommentCountsInTx(ctx, s.tx, []string{id})
	return int64(counts[id]), err
}

func (s graphIssueDetailSource) IterDependents(ctx context.Context, id string, _ bool) (storage.Iter[types.IssueWithDependencyMetadata], error) {
	items, err := issueops.GetDependentsWithMetadataInTx(ctx, s.tx, id)
	if err != nil {
		return nil, err
	}
	return storage.NewSliceIter(items), nil
}

func (s graphIssueDetailSource) IterComments(ctx context.Context, id string, _ bool) (storage.Iter[types.Comment], error) {
	items, err := issueops.GetIssueCommentsInTx(ctx, s.tx, id)
	if err != nil {
		return nil, err
	}
	return storage.NewSliceIter(items), nil
}

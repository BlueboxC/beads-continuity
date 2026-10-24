package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
)

// IssueSelector keeps an exact canonical graph path separate from an opaque
// local native Issue ID. NativeID is set for any local spelling, including
// strings that happen to contain beads/; canonical URLs select paths only.
type IssueSelector struct {
	Path     string
	NativeID string
	// Exact is used after a canonical public URL or explicit literal-ID form.
	// It disables shipping Issue abbreviation fallback, not revision guarding.
	Exact bool
}

// A non-Issue selection has a different user-visible state-read refusal from
// a bare name that ambiguously identifies an Issue and another graph Bead.
var errNonIssueSelector = errors.New("selector is not an Issue")

func nonIssueSelectorError(backing string) error {
	return fmt.Errorf("%w: selector names %s, not an Issue: %w", storage.ErrValidation, backing, errNonIssueSelector)
}

func validateIssueSelector(selector IssueSelector) error {
	if err := validatePath(selector.Path); err != nil {
		return err
	}
	if selector.NativeID != "" && selector.Path != "beads/"+selector.NativeID {
		return fmt.Errorf("%w: native Issue fallback requires its matching bare selector", storage.ErrValidation)
	}
	return nil
}

// Resolve against the checked live catalog in the caller's transaction. A
// local spelling names a native Issue ID, never another Issue's graph path.
// Canonical URLs remain exact paths.
// This exact pass is the first stage of native abbreviation resolution.
func (s *Store) resolveExactLiveIssueSelectorInTx(ctx context.Context, tx *sql.Tx, selector IssueSelector) (string, IssueRecord, error) {
	if selector.NativeID == "" {
		var backing string
		err := tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=?`, selector.Path).Scan(&backing)
		if errors.Is(err, sql.ErrNoRows) {
			return "", IssueRecord{}, ErrNotFound
		}
		if err != nil {
			return "", IssueRecord{}, err
		}
		if backing != "issue" {
			return "", IssueRecord{}, nonIssueSelectorError(backing)
		}
		issue, err := s.requireIssueInTx(ctx, tx, selector.Path)
		return selector.Path, issue, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT path FROM graph_preview_catalog WHERE backing='issue' AND backing_key=? AND allocation_state='live' LIMIT 2`, selector.NativeID)
	if err != nil {
		return "", IssueRecord{}, err
	}
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return "", IssueRecord{}, err
		}
		paths = append(paths, path)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", IssueRecord{}, err
	}
	if len(paths) > 1 {
		return "", IssueRecord{}, fmt.Errorf("%w: native Issue ID has multiple live graph allocations", ErrInvalidStore)
	}
	if len(paths) == 1 {
		issue, err := s.requireIssueInTx(ctx, tx, paths[0])
		return paths[0], issue, err
	}
	var backing string
	err = tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog
		WHERE COALESCE(public_id,path)=? AND backing IN ('generic','dependency','informational') AND allocation_state='live'`, selector.NativeID).Scan(&backing)
	if err == nil {
		return "", IssueRecord{}, nonIssueSelectorError(backing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", IssueRecord{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=? AND allocation_state='live'`, selector.Path).Scan(&backing)
	if err == nil && backing == "generic" {
		return "", IssueRecord{}, nonIssueSelectorError(backing)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", IssueRecord{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog
		WHERE backing='issue' AND backing_key=? AND allocation_state='deleted'`, selector.NativeID).Scan(&backing)
	if err == nil {
		return "", IssueRecord{}, ErrGone
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", IssueRecord{}, err
	}
	return "", IssueRecord{}, ErrNotFound
}

// Every bare native Issue selector accepts shipping Issue abbreviations.
// Callers resolve inside their own transaction, so selection and any write
// observe the same live catalog. Canonical URLs stay exact through Path-only
// selectors; local beads/PATH spelling is an opaque public ID.
func (s *Store) resolveLiveIssueSelectorInTx(ctx context.Context, tx *sql.Tx, selector IssueSelector) (string, IssueRecord, error) {
	if selector.Exact {
		return s.resolveExactLiveIssueSelectorInTx(ctx, tx, selector)
	}
	return s.resolveDependencyOperandInTx(ctx, tx, selector.Path, selector.NativeID)
}

// Dependency requests predate IssueSelector as a request field. Keep this
// adapter until those request types are migrated to the common selector.
func (s *Store) resolveDependencyOperandInTx(ctx context.Context, tx *sql.Tx, path, nativeID string) (string, IssueRecord, error) {
	if strings.HasPrefix(path, s.ScopeURL()) {
		if nativeID != "" {
			return "", IssueRecord{}, fmt.Errorf("%w: canonical endpoint cannot also supply a local-ID selector", storage.ErrValidation)
		}
		var err error
		path, err = s.publicResourcePathInTx(ctx, tx, path, graph.KindBead)
		if err != nil {
			return "", IssueRecord{}, err
		}
	}
	if nativeID == "" {
		issue, err := s.requireIssueInTx(ctx, tx, path)
		return path, issue, err
	}
	if path != "beads/"+nativeID {
		return "", IssueRecord{}, fmt.Errorf("%w: local Issue ID does not match its candidate path", storage.ErrValidation)
	}
	// An exact lifetime public identity must not fall through to a different
	// native Issue found by abbreviation, even when the exact allocation is a
	// Memory, Link, or tombstone.
	identity, identityErr := resolveAllocatedResourceIdentityInTx(ctx, tx, nativeID)
	if identityErr == nil {
		if identity.State == "deleted" {
			return "", IssueRecord{}, ErrGone
		}
		if identity.Kind != graph.KindBead {
			return "", IssueRecord{}, nonIssueSelectorError(string(identity.Kind))
		}
		return s.resolveExactLiveIssueSelectorInTx(ctx, tx, IssueSelector{Path: identity.PrivatePath, Exact: true})
	}
	if !errors.Is(identityErr, ErrNotFound) {
		return "", IssueRecord{}, identityErr
	}
	switch strings.ToLower(strings.TrimSpace(nativeID)) {
	case "", "null", "undefined", "none", "nil":
		return "", IssueRecord{}, fmt.Errorf("%w: refusing missing-value token %q as an Issue ID", storage.ErrValidation, nativeID)
	}
	selected, issue, err := s.resolveExactLiveIssueSelectorInTx(ctx, tx, IssueSelector{Path: path, NativeID: nativeID})
	if err == nil && issue.Properties.ID == nativeID {
		return selected, issue, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, errNonIssueSelector) {
		return selected, issue, err
	}
	selectedErr := err
	// Shipping first probes the exact spelling and its configured-prefix form.
	// Querying the live catalog (rather than issues alone) excludes deleted and
	// otherwise unallocated native rows.
	var prefix string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM config WHERE `key`='issue_prefix'").Scan(&prefix); err != nil {
		return "", IssueRecord{}, err
	}
	prefix = strings.TrimSuffix(prefix, "-")
	known := []string{prefix}
	var allowed string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM config WHERE `key`='allowed_prefixes'").Scan(&allowed); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", IssueRecord{}, err
	}
	for _, value := range strings.Split(allowed, ",") {
		if value = strings.TrimSuffix(strings.TrimSpace(value), "-"); value != "" {
			known = append(known, value)
		}
	}
	search := nativeID
	if p := configuredIssuePrefix(nativeID, known); p != "" {
		search = strings.TrimPrefix(nativeID, p+"-")
	}
	if !validPartialIssueHash(search) {
		return selected, issue, selectedErr
	}
	type match struct{ path, id string }
	var normalizedExact []match
	seenPrefix := map[string]bool{}
	for _, candidatePrefix := range known {
		if candidatePrefix == "" || seenPrefix[candidatePrefix] {
			continue
		}
		seenPrefix[candidatePrefix] = true
		candidateID := candidatePrefix + "-" + search
		var candidatePath string
		err := tx.QueryRowContext(ctx, `SELECT path FROM graph_preview_catalog WHERE backing='issue' AND allocation_state='live' AND backing_key=?`, candidateID).Scan(&candidatePath)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", IssueRecord{}, err
		}
		normalizedExact = append(normalizedExact, match{candidatePath, candidateID})
	}
	if len(normalizedExact) > 0 {
		if len(normalizedExact) > 1 {
			return "", IssueRecord{}, fmt.Errorf("%w: Issue hash %q has multiple exact prefix matches; use a full ID", storage.ErrValidation, nativeID)
		}
		candidate := normalizedExact[0]
		if (selectedErr == nil && selected != candidate.path) || errors.Is(selectedErr, errNonIssueSelector) {
			return "", IssueRecord{}, fmt.Errorf("%w: public Issue ID %q is ambiguous; use a canonical Scope URL for an exact graph path", storage.ErrValidation, nativeID)
		}
		resolved, err := s.requireIssueInTx(ctx, tx, candidate.path)
		return candidate.path, resolved, err
	}
	// SQL narrows the candidate set; Go applies the same leading-hash rule as
	// v1.3.1. The limit is a refusal bound, never a truncated answer.
	queryPart := search
	if i := strings.LastIndex(search, "-"); i >= 0 && i+1 < len(search) && validPartialIssueHash(search[i+1:]) {
		queryPart = search[i+1:]
	}
	rows, err := tx.QueryContext(ctx, `SELECT path,backing_key FROM graph_preview_catalog WHERE backing='issue' AND allocation_state='live' AND backing_key LIKE ? LIMIT 1001`, "%"+queryPart+"%")
	if err != nil {
		return "", IssueRecord{}, err
	}
	var exact, partial []match
	seen := 0
	for rows.Next() {
		seen++
		var candidate match
		if err := rows.Scan(&candidate.path, &candidate.id); err != nil {
			_ = rows.Close()
			return "", IssueRecord{}, err
		}
		if seen > 1000 {
			_ = rows.Close()
			return "", IssueRecord{}, fmt.Errorf("%w: too many Issue ID candidates for %q; use a longer ID", storage.ErrValidation, nativeID)
		}
		hash := candidate.id
		if p := configuredIssuePrefix(candidate.id, known); p != "" {
			hash = strings.TrimPrefix(candidate.id, p+"-")
		}
		if candidate.id == nativeID || hash == search {
			exact = append(exact, candidate)
		} else if strings.HasPrefix(hash, search) {
			partial = append(partial, candidate)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return "", IssueRecord{}, err
	}
	matches := partial
	if len(exact) > 0 {
		matches = exact
	}
	if len(matches) == 0 {
		return selected, issue, selectedErr
	}
	if selectedErr == nil && selected != matches[0].path {
		return "", IssueRecord{}, fmt.Errorf("%w: public Issue ID %q is ambiguous; use a canonical Scope URL for an exact graph path", storage.ErrValidation, nativeID)
	}
	if errors.Is(selectedErr, errNonIssueSelector) {
		return "", IssueRecord{}, fmt.Errorf("%w: public Issue ID %q is ambiguous across Resource kinds; use a canonical Scope URL for an exact graph path", storage.ErrValidation, nativeID)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, candidate := range matches {
			ids = append(ids, candidate.id)
		}
		sort.Strings(ids)
		return "", IssueRecord{}, fmt.Errorf("%w: Issue ID %q matches %v; use more characters", storage.ErrValidation, nativeID, ids)
	}
	if err := validatePath(matches[0].path); err != nil {
		return "", IssueRecord{}, fmt.Errorf("%w: invalid native Issue allocation: %v", ErrInvalidStore, err)
	}
	issue, err = s.requireIssueInTx(ctx, tx, matches[0].path)
	return matches[0].path, issue, err
}

func validPartialIssueHash(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// An unknown prefix is part of the opaque ID. The general Issue helper falls
// back to a spelling heuristic, which could turn other-parent into a match
// for compat-parent even though those are different public IDs.
func configuredIssuePrefix(id string, known []string) string {
	longest := ""
	for _, prefix := range known {
		if len(prefix) > len(longest) && strings.HasPrefix(id, prefix+"-") {
			longest = prefix
		}
	}
	return longest
}

func (s *Store) ShowIssueSelected(ctx context.Context, selector IssueSelector) (IssueRecord, error) {
	if err := validateIssueSelector(selector); err != nil {
		return IssueRecord{}, err
	}
	var result IssueRecord
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var issue IssueRecord
		var err error
		if selector.NativeID == "" {
			_, issue, err = s.resolveExactLiveIssueSelectorInTx(ctx, tx, selector)
		} else {
			_, issue, err = s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
		}
		result = issue
		return err
	})
	if err != nil {
		if errors.Is(err, errNonIssueSelector) {
			return IssueRecord{}, ErrCapabilityUnavailable
		}
		return IssueRecord{}, err
	}
	return result, nil
}

// ResolveIssuePaths resolves count filters against the checked Issue catalog.
// Results are public IDs, matching SearchIssueRows. Missing and non-Issue IDs
// retain their exact public spelling so a count returns zero without matching
// an unrelated allocation whose private locator happens to share that spelling.
// Ambiguous bare IDs fail.
func (s *Store) ResolveIssuePaths(ctx context.Context, selectors []IssueSelector) ([]string, error) {
	if len(selectors) == 0 {
		return []string{}, nil
	}
	for _, selector := range selectors {
		if err := validateIssueSelector(selector); err != nil {
			return nil, err
		}
	}
	paths := make([]string, len(selectors))
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		for i, selector := range selectors {
			path, issue, err := s.resolveLiveIssueSelectorInTx(ctx, tx, selector)
			literal := selector.NativeID
			if literal == "" {
				identity, lookupErr := allocatedResourceIdentityForPathInTx(ctx, tx, selector.Path)
				if lookupErr == nil {
					literal = identity.PublicID
				} else if !errors.Is(lookupErr, ErrNotFound) {
					return lookupErr
				} else {
					literal = selector.Path
				}
			}
			switch {
			case errors.Is(err, ErrNotFound):
				paths[i] = literal
			case errors.Is(err, errNonIssueSelector):
				paths[i] = literal
			case err != nil:
				return err
			default:
				paths[i], err = graph.ScopeLocalPublicResourceID(s.ScopeURL(), issue.ID)
				if err != nil {
					return fmt.Errorf("%w: Issue at %q has invalid public identity: %v", ErrInvalidStore, path, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

var (
	ErrCapabilityUnavailable = errors.New("graph preview capability is unavailable")
	ErrLimitExceeded         = errors.New("graph preview result limit exceeded")
)

// CollectLinks returns every matching current Link from one bounded,
// authority-checked snapshot. The bound applies before filtering: a caller
// cannot mistake a truncated workspace scan for a complete answer. Unlike
// ListLinks, this is not a per-Bead incident walk.
func (s *Store) CollectLinks(ctx context.Context, request LinkCollectionRequest) ([]LinkRecord, error) {
	if err := s.validateLinkType(request.TypeURL, true); err != nil {
		return nil, err
	}
	if request.PairDirection != "" && (request.PairDirection != "out" && request.PairDirection != "in" && request.PairDirection != "both" ||
		request.SourcePath == "" || request.TargetPath == "") {
		return nil, fmt.Errorf("%w: pair selection requires two Beads and direction out, in, or both", storage.ErrValidation)
	}
	if request.IncidentDirection != "" && (request.IncidentPath == "" || request.IncidentDirection != "in" && request.IncidentDirection != "out" && request.IncidentDirection != "both") {
		return nil, fmt.Errorf("%w: incident direction requires a Bead and direction in, out, or both", storage.ErrValidation)
	}
	for _, path := range []string{request.SourcePath, request.TargetPath, request.IncidentPath} {
		if path != "" {
			if err := validatePath(path); err != nil {
				return nil, err
			}
		}
	}
	var result []LinkRecord
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if err := s.checkLinkMappingsInTx(ctx, tx); err != nil {
			return err
		}
		if request.TypeURL != "" {
			if err := s.selectedLinkTypeInTx(ctx, tx, request.TypeURL); err != nil {
				return err
			}
		}
		family, err := s.selectedFamilyIDsInTx(ctx, tx, request.ConformsTo, graph.KindLink)
		if err != nil {
			return err
		}
		// Resolve selected public identities and live endpoint revisions in the
		// same read transaction as the Link snapshot. A historical token is
		// stable, but floating-pin applicability depends on current revision.
		currentRevisions := make(map[string]string)
		for _, endpoint := range []struct {
			path string
			url  *string
		}{{request.SourcePath, &request.SourceURL}, {request.TargetPath, &request.TargetURL}, {request.IncidentPath, &request.IncidentURL}} {
			if endpoint.path == "" {
				continue
			}
			publicURL, err := s.publicResourceURLInTx(ctx, tx, endpoint.path)
			if err != nil {
				return err
			}
			*endpoint.url = publicURL
			if _, exists := currentRevisions[endpoint.path]; exists {
				continue
			}
			_, revision, err := s.beadEndpointInTx(ctx, tx, endpoint.path)
			if err != nil {
				return err
			}
			currentRevisions[endpoint.path] = revision
		}
		rows, err := tx.QueryContext(ctx, `SELECT path FROM graph_preview_catalog
 WHERE allocation_state='live' AND resource_kind='link' LIMIT ?`, PreviewSnapshotLimit+1)
		if err != nil {
			return err
		}
		paths := []string{}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				return errors.Join(err, rows.Close())
			}
			paths = append(paths, path)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(paths) > PreviewSnapshotLimit {
			return fmt.Errorf("%w: complete Link collection exceeds %d live Links; pagination is unavailable", ErrLimitExceeded, PreviewSnapshotLimit)
		}
		sort.Slice(paths, func(i, j int) bool { return graph.CompareCodeUnits(paths[i], paths[j]) < 0 })
		result = make([]LinkRecord, 0, len(paths))
		for _, path := range paths {
			link, err := s.showLinkInTx(ctx, tx, path)
			if err != nil {
				return err
			}
			if family != nil && !family[link.Type] || request.TypeURL != "" && link.Type != request.TypeURL ||
				!LinkCollectionMatches(link, request, currentRevisions) {
				continue
			}
			result = append(result, link)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// LinkPinApplies is shared by legacy and canonical CLI collection readers.
// A selection of the current revision includes floating Links; an older exact
// revision admits only an exact stored pin. Bare IDs and --all-revisions leave
// pin applicability unconstrained, without admitting historical Links.
func LinkPinApplies(pin string, selected LinkPinSelection, current string, allRevisions bool) bool {
	if allRevisions || !selected.Explicit {
		return true
	}
	wanted := selected.Revision
	if selected.Current {
		wanted = current
	}
	return pin == wanted || pin == "" && wanted == current
}

// LinkCollectionMatches applies the CLI's endpoint and pin predicates to one
// captured current Link. Callers supply revisions from the same read snapshot.
func LinkCollectionMatches(link LinkRecord, request LinkCollectionRequest, current map[string]string) bool {
	endpoint := func(uri, pin, path, selectedURL string, selected LinkPinSelection) bool {
		return path != "" && selectedURL != "" && uri == selectedURL &&
			LinkPinApplies(pin, selected, current[path], request.AllRevisions)
	}
	if request.PairDirection != "" {
		forward := endpoint(link.Source, link.SourceRevision, request.SourcePath, request.SourceURL, request.SourcePin) &&
			endpoint(link.Target, link.TargetRevision, request.TargetPath, request.TargetURL, request.TargetPin)
		reverse := endpoint(link.Source, link.SourceRevision, request.TargetPath, request.TargetURL, request.TargetPin) &&
			endpoint(link.Target, link.TargetRevision, request.SourcePath, request.SourceURL, request.SourcePin)
		pair := request.PairDirection != "in" && forward || request.PairDirection != "out" && reverse
		return pair && linkIncidentMatches(link, request, endpoint)
	}
	if request.SourcePath != "" && !endpoint(link.Source, link.SourceRevision, request.SourcePath, request.SourceURL, request.SourcePin) ||
		request.TargetPath != "" && !endpoint(link.Target, link.TargetRevision, request.TargetPath, request.TargetURL, request.TargetPin) {
		return false
	}
	return linkIncidentMatches(link, request, endpoint)
}

func linkIncidentMatches(link LinkRecord, request LinkCollectionRequest, endpoint func(string, string, string, string, LinkPinSelection) bool) bool {
	if request.IncidentPath == "" {
		return true
	}
	return request.IncidentDirection != "in" && endpoint(link.Source, link.SourceRevision, request.IncidentPath, request.IncidentURL, request.IncidentPin) ||
		request.IncidentDirection != "out" && endpoint(link.Target, link.TargetRevision, request.IncidentPath, request.IncidentURL, request.IncidentPin)
}

// ListLinks returns complete, canonically ordered live incident state, or an
// explicit limit error. It performs no endpoint resolution outside this Scope.
func (s *Store) ListLinks(ctx context.Context, request LinksRequest) ([]LinkRecord, error) {
	if err := validatePath(request.BeadPath); err != nil {
		return nil, err
	}
	if request.Direction == "" {
		request.Direction = "both"
	}
	if request.Direction != "both" && request.Direction != "in" && request.Direction != "out" {
		return nil, fmt.Errorf("%w: direction must be in, out, or both", storage.ErrValidation)
	}
	if err := s.validateLinkType(request.TypeURL, true); err != nil {
		return nil, err
	}
	var links []LinkRecord
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if request.TypeURL != "" {
			if err := s.selectedLinkTypeInTx(ctx, tx, request.TypeURL); err != nil {
				return err
			}
		}
		if _, _, err := s.beadEndpointInTx(ctx, tx, request.BeadPath); err != nil {
			return err
		}
		var err error
		links, err = s.incidentLinksInTx(ctx, tx, request, "")
		if err != nil {
			return err
		}
		family, err := s.selectedFamilyIDsInTx(ctx, tx, request.ConformsTo, graph.KindLink)
		if err != nil {
			return err
		}
		if family != nil {
			filtered := make([]LinkRecord, 0, len(links))
			for _, link := range links {
				if family[link.Type] {
					filtered = append(filtered, link)
				}
			}
			links = filtered
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return links, nil
}

func (s *Store) validateLinkType(typ string, allowEmpty bool) error {
	if (allowEmpty && typ == "") || IsInformationalTypeURL(s.ScopeURL(), typ) || typ == DependencyTypeURL(s.options.Binding.ScopeURL) {
		return nil
	}
	if isNativeRelationshipDependencyType(s.ScopeURL(), typ) {
		return nil // The transaction also checks the installed Type.
	}
	return fmt.Errorf("%w: Link Type is not installed in this preview", storage.ErrValidation)
}

func (s *Store) incidentLinksInTx(ctx context.Context, tx *sql.Tx, request LinksRequest, targetPath string) ([]LinkRecord, error) {
	if err := s.checkLinkMappingsInTx(ctx, tx); err != nil {
		return nil, err
	}
	// Each authority projection emits a self-link once, without
	// limiting either backing independently or truncating a successful response.
	rows, err := tx.QueryContext(ctx, `SELECT path FROM (
 SELECT c.path, c.type_url, l.source_path, l.target_path
 FROM graph_preview_catalog c JOIN graph_preview_links l ON l.path=c.path
 WHERE c.allocation_state='live' AND c.backing='informational'
 UNION ALL
 SELECT c.path, c.type_url, src.path, dst.path
 FROM graph_preview_catalog c JOIN dependencies d ON d.id=c.backing_key
 JOIN graph_preview_catalog src ON src.backing='issue' AND src.backing_key=d.issue_id
 JOIN graph_preview_catalog dst ON dst.backing='issue' AND dst.backing_key=d.depends_on_issue_id
 WHERE c.allocation_state='live' AND c.backing='dependency'
 ) incident WHERE ((? IN ('out','both') AND source_path=?) OR (? IN ('in','both') AND target_path=?))
 AND (?='' OR type_url=?) AND (?='' OR target_path=?) LIMIT ?`, request.Direction, request.BeadPath, request.Direction, request.BeadPath, request.TypeURL, request.TypeURL, targetPath, targetPath, PreviewIncidentLinkLimit+1)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(paths) > PreviewIncidentLinkLimit {
		return nil, fmt.Errorf("%w: at most %d incident Links can be returned; preview pagination is unavailable", ErrLimitExceeded, PreviewIncidentLinkLimit)
	}
	sort.Slice(paths, func(i, j int) bool { return graph.CompareCodeUnits(paths[i], paths[j]) < 0 })
	result := make([]LinkRecord, 0, len(paths))
	for _, path := range paths {
		link, err := s.showLinkInTx(ctx, tx, path)
		if err != nil {
			return nil, err
		}
		result = append(result, link)
	}
	return result, nil
}

// Unlink selects, checks both guards, tombstones identity and records its owning
// Bead in one transaction. It neither replays deletion nor reuses identities.
// Blocking Dependencies currently require canonical Link ID selection.
func (s *Store) Unlink(ctx context.Context, request LinkDeleteRequest) (LinkDeleteResult, error) {
	if request.DefaultInformationalSource && (request.ExpectedSourceRevision != "" || request.UnconditionalSource) {
		return LinkDeleteResult{}, fmt.Errorf("%w: default informational source acceptance cannot accompany an explicit source guard", storage.ErrValidation)
	}
	if !utf8.ValidString(request.Actor) {
		return LinkDeleteResult{}, fmt.Errorf("%w: actor must be UTF-8", storage.ErrValidation)
	}
	if request.Path != "" {
		if err := validateLinkPath(request.Path); err != nil {
			return LinkDeleteResult{}, err
		}
		if request.SourcePath != "" || request.TargetPath != "" || request.TypeURL != "" || request.SourceLocalID != "" || request.TargetLocalID != "" {
			return LinkDeleteResult{}, fmt.Errorf("%w: select a Link ID or an exact typed endpoint pair", storage.ErrValidation)
		}
	} else {
		for _, path := range []string{request.SourcePath, request.TargetPath} {
			if err := validatePath(path); err != nil {
				return LinkDeleteResult{}, err
			}
		}
		if request.SourceLocalID != "" && request.SourcePath != "beads/"+request.SourceLocalID ||
			request.TargetLocalID != "" && request.TargetPath != "beads/"+request.TargetLocalID {
			return LinkDeleteResult{}, fmt.Errorf("%w: local Bead ID does not match its candidate path", storage.ErrValidation)
		}
		if err := s.validateLinkType(request.TypeURL, false); err != nil {
			return LinkDeleteResult{}, err
		}
		if request.TypeURL == DependencyTypeURL(s.options.Binding.ScopeURL) || !IsInformationalTypeURL(s.ScopeURL(), request.TypeURL) {
			return LinkDeleteResult{}, fmt.Errorf("%w: native Dependency unlink requires its canonical Link ID", ErrCapabilityUnavailable)
		}
	}
	var result LinkDeleteResult
	err := s.withTypeValidationTx(ctx, func(tx *sql.Tx) error {
		var err error
		if request.Path == "" {
			request.SourcePath, err = s.resolveLiveBeadWriteSelectorInTx(ctx, tx, request.SourcePath, request.SourceLocalID)
			if err != nil {
				return err
			}
			request.TargetPath, err = s.resolveLiveBeadWriteSelectorInTx(ctx, tx, request.TargetPath, request.TargetLocalID)
			if err != nil {
				return err
			}
		}
		result, err = s.unlinkInTx(ctx, tx, request)
		return err
	})
	if err != nil {
		return LinkDeleteResult{}, err
	}
	return result, nil
}

// unlinkInTx performs the canonical Link deletion inside its caller's checked
// transaction. Keep the public Unlink and composed Issue mutations on this
// single path so native edges, graph tombstones, owners, and History agree.
func (s *Store) unlinkInTx(ctx context.Context, tx *sql.Tx, request LinkDeleteRequest) (LinkDeleteResult, error) {
	if err := checkBinding(ctx, tx, s.options); err != nil {
		return LinkDeleteResult{}, err
	}
	if request.TypeURL != "" {
		if err := s.selectedLinkTypeInTx(ctx, tx, request.TypeURL); err != nil {
			return LinkDeleteResult{}, err
		}
	}
	path, err := s.selectUnlinkPathInTx(ctx, tx, request)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	link, err := s.showLinkInTx(ctx, tx, path)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return LinkDeleteResult{}, err
	}
	if isNativeDependencyLinkType(s.ScopeURL(), issueType, link.Type) {
		return s.unlinkDependencyInTx(ctx, tx, path, link, request)
	}
	if !IsInformationalTypeURL(s.ScopeURL(), link.Type) {
		return LinkDeleteResult{}, fmt.Errorf("%w: native relationship Link unlink is not admitted in this preview", ErrCapabilityUnavailable)
	}
	// Only informational Links reach this point. Resolve the CLI default
	// here, after transactional selection, without a whole-workspace read.
	if request.DefaultInformationalSource {
		request.UnconditionalSource = true
	}
	if err := checkRevisionGuard(request.ExpectedRevision, request.Unconditional, link.Revision, true, "Link"); err != nil {
		return LinkDeleteResult{}, err
	}
	sourcePath, err := s.publicResourcePathInTx(ctx, tx, link.Source, graph.KindBead)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	source, revision, err := s.beadEndpointInTx(ctx, tx, sourcePath)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	owned, err := s.ownsLink(source, link.Type)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	if err := checkRevisionGuard(request.ExpectedSourceRevision, request.UnconditionalSource, revision, owned, "source"); err != nil {
		return LinkDeleteResult{}, err
	}
	if err := s.touchCoordination(ctx, tx); err != nil {
		return LinkDeleteResult{}, err
	}
	deletionRevision, err := freshToken()
	if err != nil {
		return LinkDeleteResult{}, err
	}
	attribution := writeAttribution(request.Actor)
	attribution.Inception = link.Attribution.Inception
	tombstone := LinkTombstone{ID: link.ID, Type: link.Type, Revision: deletionRevision, Version: deletionRevision, State: "deleted", PreviousVersion: link.Version, Attribution: attribution}
	if _, err := tx.ExecContext(ctx, `UPDATE graph_preview_catalog SET allocation_state='deleted',revision=? WHERE path=?`, deletionRevision, path); err != nil {
		return LinkDeleteResult{}, err
	}
	if err := s.afterStage("link-catalog"); err != nil {
		return LinkDeleteResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_preview_links WHERE path=?`, path); err != nil {
		return LinkDeleteResult{}, err
	}
	if err := s.afterStage("link-payload"); err != nil {
		return LinkDeleteResult{}, err
	}
	snapshot, err := canonicalJSON(tombstone)
	if err != nil {
		return LinkDeleteResult{}, err
	}
	if err := insertPreviewVersionInTx(ctx, tx, path, deletionRevision, snapshot, request.Actor); err != nil {
		return LinkDeleteResult{}, err
	}
	if err := s.afterStage("link-retained"); err != nil {
		return LinkDeleteResult{}, err
	}
	replacedSource := replacedMemory(source, request.UnconditionalSource)
	if owned {
		source, err = s.recordOwnedMemoryInTx(ctx, tx, sourcePath, request.Actor, source)
	} else {
		replacedSource = nil
	}
	if err != nil {
		return LinkDeleteResult{}, err
	}
	return LinkDeleteResult{Link: tombstone, Source: source, Changed: true, ReplacedSource: replacedSource}, nil
}

func (s *Store) selectUnlinkPathInTx(ctx context.Context, tx *sql.Tx, request LinkDeleteRequest) (string, error) {
	if request.Path != "" {
		return request.Path, nil
	}
	for _, path := range []string{request.SourcePath, request.TargetPath} {
		if _, _, err := s.beadEndpointInTx(ctx, tx, path); err != nil {
			return "", err
		}
	}
	links, err := s.incidentLinksInTx(ctx, tx, LinksRequest{BeadPath: request.SourcePath, Direction: "out", TypeURL: request.TypeURL}, request.TargetPath)
	if err != nil {
		return "", err
	}
	targetURL, err := s.publicResourceURLInTx(ctx, tx, request.TargetPath)
	if err != nil {
		return "", err
	}
	candidates := []string{}
	for _, link := range links {
		if link.Target == targetURL {
			candidates = append(candidates, link.ID)
		}
	}
	if len(candidates) == 0 {
		return "", ErrNotFound
	}
	if len(candidates) > 1 {
		return "", &ErrAmbiguousLink{CandidateIDs: candidates}
	}
	return s.publicResourcePathInTx(ctx, tx, candidates[0], graph.KindLink)
}

// A valid tombstone is distinct from missing or corrupt state. Missing retained
// deletion/previous state must not be disguised as ordinary gone.
func (s *Store) deletedLinkErrorInTx(ctx context.Context, tx *sql.Tx, path string) error {
	_, _, err := s.deletedLinkStateInTx(ctx, tx, path)
	if err != nil {
		return err
	}
	return ErrGone
}

// The preview has one controlled graph writer. Refuse incomplete authority
// mappings before a join can hide them as an apparently complete collection.
func (s *Store) checkLinkMappingsInTx(ctx context.Context, tx *sql.Tx) error {
	var invalidTypes int
	issueType, err := installedIssueTypeURLInTx(ctx, tx, s.ScopeURL())
	if err != nil {
		return err
	}
	ids, err := s.installedInformationalTypes(ctx, tx)
	if err != nil {
		return err
	}
	infoPlaceholders, args := informationalTypeSQL(ids)
	dependencyTypes, err := s.nativeDependencyTypeIDsInTx(ctx, tx)
	if err != nil {
		return err
	}
	_ = issueType
	depPlaceholders, depArgs := informationalTypeSQL(dependencyTypes)
	args = append(args, depArgs...)
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_catalog WHERE resource_kind='link' AND allocation_state='live' AND ((backing='informational' AND type_url NOT IN (`+infoPlaceholders+`)) OR (backing='dependency' AND type_url NOT IN (`+depPlaceholders+`)))`, args...).Scan(&invalidTypes); err != nil {
		return err
	}
	if invalidTypes != 0 {
		return fmt.Errorf("%w: Link Type differs from its authority", ErrInvalidStore)
	}
	// The one Link Type cannot identify a native kind. Reject malformed native
	// kinds here; currentLinkInTx also checks this before projecting dep_type.
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_catalog c JOIN dependencies d ON d.id=c.backing_key
 WHERE c.backing='dependency' AND c.allocation_state='live' AND (d.type='' OR OCTET_LENGTH(d.type)>? OR c.type_url<>CONCAT(?,LOWER(HEX(d.type))))`,
		types.MaxDependencyTypeLen, s.ScopeURL()+"types/native-dependency/").Scan(&invalidTypes); err != nil {
		return err
	}
	if invalidTypes != 0 {
		return fmt.Errorf("%w: invalid native Dependency kind", ErrInvalidStore)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM graph_preview_catalog c LEFT JOIN graph_preview_links l ON l.path=c.path LEFT JOIN dependencies d ON d.id=c.backing_key
   WHERE c.resource_kind='link' AND c.allocation_state='live' AND
   ((c.backing='informational' AND l.path IS NULL) OR (c.backing='dependency' AND d.id IS NULL) OR c.backing NOT IN ('informational','dependency'))`,
		`SELECT COUNT(*) FROM graph_preview_links l LEFT JOIN graph_preview_catalog c ON c.path=l.path
   LEFT JOIN graph_preview_catalog src ON src.path=l.source_path LEFT JOIN graph_preview_catalog dst ON dst.path=l.target_path
   WHERE c.path IS NULL OR c.resource_kind<>'link' OR c.allocation_state<>'live' OR c.backing<>'informational'
    OR src.path IS NULL OR dst.path IS NULL OR src.resource_kind<>'bead' OR dst.resource_kind<>'bead' OR src.allocation_state<>'live' OR dst.allocation_state<>'live'`,
		`SELECT COUNT(*) FROM dependencies d LEFT JOIN graph_preview_catalog c ON c.backing='dependency' AND c.backing_key=d.id
   LEFT JOIN graph_preview_catalog src ON src.backing='issue' AND src.backing_key=d.issue_id
   LEFT JOIN graph_preview_catalog dst ON dst.backing='issue' AND dst.backing_key=d.depends_on_issue_id
   WHERE c.path IS NULL OR c.resource_kind<>'link' OR c.allocation_state<>'live' OR src.path IS NULL OR dst.path IS NULL
    OR src.resource_kind<>'bead' OR dst.resource_kind<>'bead' OR src.allocation_state<>'live' OR dst.allocation_state<>'live'`,
	} {
		var count int
		if err := tx.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("%w: incomplete Link authority mapping", ErrInvalidStore)
		}
	}
	return nil
}

// Retained references are validated as stored selectors, never by joining live
// endpoints. A deleted Link's historical evidence must remain independently readable.
func (s *Store) validateRetainedInformational(ctx context.Context, tx *sql.Tx, link LinkRecord, snapshot []byte, actor string) error {
	canonical, err := canonicalJSON(link)
	if err != nil || !bytes.Equal(canonical, snapshot) || link.Properties == nil {
		return fmt.Errorf("%w: noncanonical retained Link", ErrInvalidStore)
	}
	for _, endpoint := range []string{link.Source, link.Target} {
		if _, err := s.publicResourcePathInTx(ctx, tx, endpoint, graph.KindBead); err != nil {
			return fmt.Errorf("%w: invalid retained Link endpoint", ErrInvalidStore)
		}
	}
	properties, err := informationalProperties(link.Properties)
	if err != nil {
		return fmt.Errorf("%w: invalid retained Link properties", ErrInvalidStore)
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return err
	}
	if err := catalog.validateProperties(link.Type, properties, graph.KindLink); err != nil {
		return fmt.Errorf("%w: invalid retained Link Type payload: %v", ErrInvalidStore, err)
	}
	at, err := time.Parse(time.RFC3339Nano, link.Attribution.RecordedAt)
	if err != nil || at.UTC().Format(time.RFC3339Nano) != link.Attribution.RecordedAt || actor != link.Attribution.Actor || !utf8.ValidString(actor) || (actor == "" && link.Attribution.Status != "unknown") || (actor != "" && link.Attribution.Status != "claimed") {
		return fmt.Errorf("%w: invalid retained Link attribution", ErrInvalidStore)
	}
	return nil
}

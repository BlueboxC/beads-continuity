package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memorycatalog"
	"github.com/steveyegge/beads/internal/memoryquery"
	"github.com/steveyegge/beads/internal/typecatalog"
)

// CurrentFamilyRequest selects current local roots with independent hop depths.
// Admitted families are References, Superseded By, Requires and Defines,
// in either direction. It is not a historical selector or a public CLI schema.
// MaxBytes and Limits accumulate across all roots, including validation feeds.
// MaxInspected independently caps accumulated acquisition rows and accumulated
// pure candidate visits; those are two counters, not one combined work budget.
// Fixed workspace/Type control reads are separately checked and may repeat in
// existing helpers; their row/CPU cost is outside these persisted-data budgets.
// No zero limit means unlimited. Exact caps succeed; excess refuses completely.
type CurrentFamilyRequest struct {
	Roots     []memoryquery.CurrentRoot
	Family    typecatalog.Reference
	Direction graph.Direction
	Limits    memoryquery.Limits
	MaxBytes  uint64
}

// CurrentFamilyRead retains canonical Records separately from selected graph
// membership: embedded Owned copies must never be interpreted as selected edges.
// Graph uses exact local public IDs; canonical Records retain their absolute IDs,
// properties, metadata, pins, ownership and attribution. WriterToken and current
// revisions describe this one observed view, not resolution of historical pins.
// Types/OwnedLinkTypes use the existing checked Snapshot context vocabulary.
type CurrentFamilyRead struct {
	WriterToken    string
	Graph          memoryquery.CurrentGraph
	Records        []any
	Types          []graph.TypeDescriptor
	OwnedLinkTypes map[string][]string
}

// ReadCurrentFamily reads all roots in one rollback-only transaction. It does
// not collect the global graph, open a transaction per root, follow historical
// pins, or invoke the legacy query evaluator. Selected old pins fail closed;
// equal-current pins bind to that same checked current version. Errors,
// cancellation and cleanup failures return zero state. Caller checks Store.Close
// before output. No CLI dispatch, output writer or public result is added here.
func (s *Store) ReadCurrentFamily(ctx context.Context, req CurrentFamilyRequest) (CurrentFamilyRead, error) {
	if ctx == nil {
		return CurrentFamilyRead{}, errors.New("graph operation requires a context")
	}
	if err := validateCurrentFamilyRequest(req); err != nil {
		return CurrentFamilyRead{}, err
	}
	var result CurrentFamilyRead
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		r, err := s.newCurrentFamilyReader(ctx, tx, req)
		if err != nil {
			return err
		}
		catalog := r.catalog
		successors := catalog.projectionBinding(memorycatalog.SupersededBy)
		requires, defines := catalog.projectionBinding(memorycatalog.Requires), catalog.projectionBinding(memorycatalog.Defines)
		if req.Family.URI != catalog.projectionBinding(memorycatalog.References) && req.Family.URI != successors && req.Family.URI != requires && req.Family.URI != defines {
			return fmt.Errorf("%w: current family read does not admit selected family", ErrCapabilityUnavailable)
		}
		for _, root := range req.Roots {
			n, err := r.current(ctx, root.Path, true)
			if err != nil {
				return err
			}
			if req.Family.URI == successors && !catalog.memoryType(n.TypeURL) {
				return fmt.Errorf("%w: succession root must be Memory", graph.ErrValidation)
			}
			if (req.Family.URI == requires || req.Family.URI == defines) && !r.isConstraint(n.TypeURL) {
				return fmt.Errorf("%w: Requires/Defines root must be Constraint", graph.ErrValidation)
			}
		}
		g, err := memoryquery.CurrentFamily(ctx, r, r.matches, req.Roots, req.Direction, req.Limits)
		if err != nil {
			return err
		}
		result, err = r.materialize(ctx, g)
		return err
	})
	err = currentReadError(ctx, err)
	if err != nil {
		return CurrentFamilyRead{}, err
	}
	return result, nil
}

func validateCurrentFamilyRequest(req CurrentFamilyRequest) error {
	if len(req.Roots) == 0 || len(req.Roots) > PreviewSnapshotLimit || !req.Direction.Valid() || req.MaxBytes == 0 || req.MaxBytes > PreviewCurrentReadByteLimit || req.Limits.MaxNodes < 1 || req.Limits.MaxNodes > PreviewSnapshotLimit || req.Limits.MaxLinks < 0 || req.Limits.MaxLinks > PreviewSnapshotLimit || req.Limits.MaxInspected < 0 || req.Limits.MaxInspected > 1000000 || req.Family.Revision != "" {
		return fmt.Errorf("%w: invalid current family read", graph.ErrValidation)
	}
	for _, root := range req.Roots {
		if graph.ValidateCanonicalResourceID(root.Path) != nil || root.Depth < 0 || root.Depth > PreviewSnapshotLimit {
			return fmt.Errorf("%w: invalid current family root", graph.ErrValidation)
		}
	}
	return nil
}
func currentReadError(ctx context.Context, err error) error {
	if errors.Is(err, memoryquery.ErrLimitExceeded) {
		err = errors.Join(ErrLimitExceeded, err)
	}
	if errors.Is(err, memoryquery.ErrCurrentContextUnavailable) {
		err = errors.Join(ErrCapabilityUnavailable, err)
	}
	err = errors.Join(err, ctx.Err())
	return err
}
func (s *Store) newCurrentFamilyReader(ctx context.Context, tx *sql.Tx, req CurrentFamilyRequest) (*currentFamilyReader, error) {
	if err := checkBinding(ctx, tx, s.options); err != nil {
		return nil, err
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return nil, err
	}
	r := &currentFamilyReader{store: s, tx: tx, catalog: catalog, request: req, allocations: map[string]currentFamilyAllocation{}, links: map[string]LinkRecord{}, feeds: map[string][]graph.Link{}}
	// Share only the SQL-side byte/row charger, never the historical solver.
	r.budget = &memoryQueryReader{tx: tx, request: MemoryRequiresRequest{MaxBytes: req.MaxBytes, Limits: req.Limits}}
	return r, nil
}
func (r *currentFamilyReader) materialize(ctx context.Context, g memoryquery.CurrentGraph) (CurrentFamilyRead, error) {
	out := CurrentFamilyRead{Graph: g}
	var err error
	out.Records = make([]any, 0, len(g.Nodes)+len(g.Links))
	for _, node := range g.Nodes {
		record, err := r.hydrate(ctx, node.Path)
		if err != nil {
			return CurrentFamilyRead{}, err
		}
		out.Records = append(out.Records, record)
	}
	for _, link := range g.Links {
		record, ok := r.links[link.Path()]
		if !ok {
			return CurrentFamilyRead{}, fmt.Errorf("%w: missing selected current Link", ErrInvalidStore)
		}
		out.Records = append(out.Records, record)
	}
	out.Types, err = r.store.selectedDescriptorsInTx(ctx, r.tx)
	if err != nil {
		return CurrentFamilyRead{}, err
	}
	out.OwnedLinkTypes = map[string][]string{}
	for _, descriptor := range out.Types {
		if descriptor.Describes() == graph.KindBead {
			owned, err := r.store.SelectedOwnedLinkTypes(descriptor.ID())
			if err != nil {
				return CurrentFamilyRead{}, err
			}
			out.OwnedLinkTypes[descriptor.ID()] = owned
		}
	}
	if err := r.tx.QueryRowContext(ctx, `SELECT writer_token FROM graph_preview_scope WHERE singleton=1`).Scan(&out.WriterToken); err != nil {
		return CurrentFamilyRead{}, err
	}
	return out, nil
}

type currentFamilyAllocation struct {
	node         memoryquery.CurrentNode
	backing, key string
	privatePath  string
}
type currentFamilyReader struct {
	store       *Store
	tx          *sql.Tx
	catalog     *selectedTypeCatalog
	request     CurrentFamilyRequest
	budget      *memoryQueryReader
	allocations map[string]currentFamilyAllocation
	links       map[string]LinkRecord
	feeds       map[string][]graph.Link
}

func (r *currentFamilyReader) matches(ctx context.Context, id string) (bool, error) {
	return r.family(r.request.Family)(ctx, id)
}
func (r *currentFamilyReader) family(family typecatalog.Reference) func(context.Context, string) (bool, error) {
	return func(_ context.Context, id string) (bool, error) {
		d, ok := r.catalog.types.Definition(nativeFamilyReference(r.store.ScopeURL(), id))
		if !ok || d.Kind != graph.KindLink {
			return false, fmt.Errorf("%w: unknown current Link Type", ErrInvalidStore)
		}
		return r.catalog.types.Conforms(d.Identity, family), nil
	}
}
func (r *currentFamilyReader) isConstraint(id string) bool {
	d, ok := r.catalog.types.Definition(nativeFamilyReference(r.store.ScopeURL(), id))
	return ok && d.Kind == graph.KindBead && r.catalog.types.Conforms(d.Identity, typecatalog.Reference{URI: r.catalog.projectionBinding(memorycatalog.Constraint)})
}

func (r *currentFamilyReader) Current(ctx context.Context, path string) (memoryquery.CurrentNode, error) {
	return r.current(ctx, path, false)
}

func (r *currentFamilyReader) current(ctx context.Context, path string, selectedRoot bool) (memoryquery.CurrentNode, error) {
	if err := ctx.Err(); err != nil {
		return memoryquery.CurrentNode{}, err
	}
	if a, ok := r.allocations[path]; ok {
		return a.node, nil
	}
	if err := graph.ValidateCanonicalResourceID(path); err != nil {
		return memoryquery.CurrentNode{}, err
	}
	count, err := r.chargeRows(ctx, "graph_preview_catalog WHERE (backing='issue' AND backing_key=?) OR (backing IN ('generic','dependency','informational') AND COALESCE(public_id,path)=?)", "path,resource_kind,type_url,revision,allocation_state,backing,backing_key,public_id", 4, path, path)
	if err != nil {
		return memoryquery.CurrentNode{}, err
	}
	if count != 1 {
		return memoryquery.CurrentNode{}, ErrNotFound
	}
	identity, err := resolveAllocatedResourceIdentityInTx(ctx, r.tx, path)
	if err != nil {
		return memoryquery.CurrentNode{}, err
	}
	privatePath := identity.PrivatePath
	var kind, typ, version, state, backing string
	var key sql.NullString
	if err := r.tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key FROM graph_preview_catalog WHERE path=?`, privatePath).Scan(&kind, &typ, &version, &state, &backing, &key); err != nil {
		return memoryquery.CurrentNode{}, err
	}
	if state == "deleted" {
		return memoryquery.CurrentNode{}, ErrGone
	}
	if state != "live" || !authorityID.MatchString(version) {
		return memoryquery.CurrentNode{}, fmt.Errorf("%w: unsupported current allocation", ErrInvalidStore)
	}
	// The identity resolver already validated kind/backing/state agreement.
	// A caller may select a valid Link, but graph incidence must resolve Beads.
	if kind != "bead" {
		if selectedRoot {
			return memoryquery.CurrentNode{}, fmt.Errorf("%w: current family root must be a Bead", graph.ErrValidation)
		}
		return memoryquery.CurrentNode{}, fmt.Errorf("%w: unsupported current allocation", ErrInvalidStore)
	}
	if !((backing == "generic" && !key.Valid && r.catalog.memoryType(typ)) || (backing == "issue" && key.Valid && key.String != "" && isConcreteNativeIssue(r.store.ScopeURL(), typ))) {
		return memoryquery.CurrentNode{}, fmt.Errorf("%w: current read supports live Memory/native Issue", ErrCapabilityUnavailable)
	}
	n := memoryquery.CurrentNode{Path: path, TypeURL: typ, Version: version}
	r.allocations[path] = currentFamilyAllocation{node: n, backing: backing, key: key.String, privatePath: privatePath}
	return n, nil
}

func (r *currentFamilyReader) Incident(ctx context.Context, path string, allowance int) ([]graph.Link, error) {
	if v, ok := r.feeds[path]; ok {
		return v, nil
	}
	if _, err := r.Current(ctx, path); err != nil {
		return nil, err
	}
	privatePath := r.allocations[path].privatePath
	// Both directions are necessary even for an outward walk: the induced graph
	// includes reverse/cross Links between reached nodes. This does not expand
	// reachability or frontier in the opposite direction.
	if err := r.chargeInformational(ctx, "(l.source_path=? OR l.target_path=?)", privatePath, privatePath); err != nil {
		return nil, err
	}
	if err := r.chargeNative(ctx, "(src.path=? OR dst.path=?)", privatePath, privatePath); err != nil {
		return nil, err
	}
	records, err := r.store.incidentLinksInTx(ctx, r.tx, LinksRequest{BeadPath: privatePath, Direction: "both"}, "")
	if err != nil {
		return nil, err
	}
	if len(records) > allowance {
		return nil, fmt.Errorf("%w: current incidence allowance", ErrLimitExceeded)
	}
	out := make([]graph.Link, 0, len(records))
	for _, record := range records {
		src, err := graph.ParsePublicRef(r.store.ScopeURL(), record.Source, record.SourceRevision)
		if err != nil {
			return nil, err
		}
		dst, err := graph.ParsePublicRef(r.store.ScopeURL(), record.Target, record.TargetRevision)
		if err != nil {
			return nil, err
		}
		revision, err := graph.NewRevision(record.Revision)
		if err != nil {
			return nil, err
		}
		raw, err := canonicalJSON(record.Properties)
		if err != nil {
			return nil, err
		}
		properties, err := graph.NewProperties(raw)
		if err != nil {
			return nil, err
		}
		linkPath, err := graph.ScopeLocalPublicResourceID(r.store.ScopeURL(), record.ID)
		if err != nil {
			return nil, err
		}
		l, err := graph.NewPublicLink(graph.LinkSpec{Path: linkPath, TypeURL: record.Type, Revision: revision, Source: src, Target: dst, Properties: properties})
		if err != nil {
			return nil, err
		}
		r.links[linkPath] = record
		out = append(out, l)
	}
	r.feeds[path] = out
	return out, nil
}

func (r *currentFamilyReader) hydrate(ctx context.Context, path string) (any, error) {
	a, ok := r.allocations[path]
	if !ok || (a.backing != "generic" && a.backing != "issue") {
		return nil, fmt.Errorf("%w: unselected hydration target", ErrInvalidStore)
	}
	privatePath := a.privatePath
	if err := r.chargeInformational(ctx, "l.source_path=?", privatePath); err != nil {
		return nil, err
	}
	if a.backing == "generic" {
		if _, err := r.chargeRows(ctx, "graph_preview_payloads p WHERE p.path=?", "p.properties,p.metadata", 3, privatePath); err != nil {
			return nil, err
		}
		if _, err := r.chargeRows(ctx, "graph_preview_versions v WHERE v.path=? AND v.version=?", "v.snapshot,v.actor", 3, privatePath, a.node.Version); err != nil {
			return nil, err
		}
		record, err := r.store.showMemoryInTx(ctx, r.tx, privatePath)
		if err != nil {
			return nil, err
		}
		if record.Version != a.node.Version {
			return nil, ErrInvalidStore
		}
		return record, nil
	}
	if err := r.chargeIssue(ctx, privatePath, a.key, a.node.Version); err != nil {
		return nil, err
	}
	record, err := r.store.showIssueInTx(ctx, r.tx, privatePath)
	if err != nil {
		return nil, err
	}
	if record.Version != a.node.Version {
		return nil, ErrInvalidStore
	}
	return record, nil
}

// Compile-time guarantee: this is current incidence, never Requires Outgoing.
var _ memoryquery.CurrentContext = (*currentFamilyReader)(nil)

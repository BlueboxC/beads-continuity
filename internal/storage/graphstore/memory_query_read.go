package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memorycatalog"
	"github.com/steveyegge/beads/internal/memoryquery"
	"github.com/steveyegge/beads/internal/typecatalog"
)

// MemoryRequiresRequest selects one local Constraint and its Requires-family
// closure. Root may float or select an exact retained revision. Limits are
// explicit; MaxBytes bounds accumulated persisted data acquisition, excluding
// the fixed, separately checked workspace/Type binding. This first slice reads
// live generic Memory identities, including their retained revisions.
type MemoryRequiresRequest struct {
	Root     graph.Ref
	Limits   memoryquery.Limits
	MaxBytes uint64
}

// MemoryRequiresRead is an internal result, not a CLI/BDP wire schema. Graph
// carries resolved witness endpoints separately from original authored pins.
// Beads and Links preserve complete records, including metadata/attribution.
// Links has one entry per Graph witness, in the same order. WriterToken names
// controlled writer state, not a public revision or an out-of-band write proof.
type MemoryRequiresRead struct {
	Root        graph.Ref
	WriterToken string
	Graph       memoryquery.Graph
	Beads       []Record
	Links       []LinkRecord
}

// ReadMemoryRequires acquires only the selected question, in one rollback-only
// transaction. It never invokes CurrentSnapshot or legacy Issue query/storage
// operations. Any read, bound, cancellation or cleanup failure returns zero
// state; Store.Close remains the caller's responsibility before output.
func (s *Store) ReadMemoryRequires(ctx context.Context, request MemoryRequiresRequest) (MemoryRequiresRead, error) {
	if ctx == nil {
		return MemoryRequiresRead{}, errors.New("graph operation requires a context")
	}
	if request.Root.IsZero() || !request.Root.InScope() || request.MaxBytes == 0 || request.MaxBytes > PreviewCurrentReadByteLimit || request.Limits.MaxNodes < 1 || request.Limits.MaxNodes > PreviewSnapshotLimit || request.Limits.MaxLinks < 0 || request.Limits.MaxLinks > PreviewSnapshotLimit || request.Limits.MaxInspected < 0 || request.Limits.MaxInspected > PreviewSnapshotLimit {
		return MemoryRequiresRead{}, fmt.Errorf("%w: invalid scoped Memory read request", graph.ErrValidation)
	}
	var result MemoryRequiresRead
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		catalog, err := s.selectedTypes()
		if err != nil {
			return err
		}
		reader, err := newMemoryQueryReader(s, tx, catalog, request)
		if err != nil {
			return err
		}
		root, err := reader.Resolve(ctx, request.Root)
		if err != nil {
			return err
		}
		exact, err := graph.NewPublicInScopeRef(root.Path(), root.Revision().String())
		if err != nil {
			return err
		}
		walk, err := memoryquery.Requires(ctx, reader, reader.requiresFamily, exact, request.Limits)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT writer_token FROM graph_preview_scope WHERE singleton=1`).Scan(&result.WriterToken); err != nil {
			return err
		}
		result.Root, result.Graph = exact, walk
		result.Beads = make([]Record, 0, len(walk.Nodes))
		result.Links = make([]LinkRecord, 0, len(walk.Links))
		for _, node := range walk.Nodes {
			ref, err := graph.NewPublicInScopeRef(node.Path(), node.Revision().String())
			if err != nil {
				return err
			}
			record, ok := reader.records[ref]
			if !ok {
				return fmt.Errorf("%w: resolved Bead missing from context", memoryquery.ErrReadContext)
			}
			result.Beads = append(result.Beads, record)
		}
		for _, witness := range walk.Links {
			link, ok := reader.links[memoryLinkKey{witness.Link.Path(), witness.Link.Revision().String()}]
			if !ok {
				return fmt.Errorf("%w: resolved Link missing from context", memoryquery.ErrReadContext)
			}
			result.Links = append(result.Links, link)
		}
		return nil
	})
	if errors.Is(err, memoryquery.ErrLimitExceeded) {
		err = errors.Join(ErrLimitExceeded, err)
	}
	err = errors.Join(err, ctx.Err())
	if err != nil {
		return MemoryRequiresRead{}, err
	}
	return result, nil
}

type memoryLinkKey struct{ path, revision string }
type memoryQueryReader struct {
	store                *Store
	tx                   *sql.Tx
	catalog              *selectedTypeCatalog
	constraint, requires typecatalog.Reference
	request              MemoryRequiresRequest
	used                 uint64
	inspected            int
	records              map[graph.Ref]Record
	current              map[string]graph.Ref
	links                map[memoryLinkKey]LinkRecord
}

func newMemoryQueryReader(store *Store, tx *sql.Tx, catalog *selectedTypeCatalog, request MemoryRequiresRequest) (*memoryQueryReader, error) {
	constraint, requires, err := memoryQueryTypeRefs(catalog)
	if err != nil {
		return nil, err
	}
	return &memoryQueryReader{store: store, tx: tx, catalog: catalog, request: request, constraint: constraint, requires: requires,
		records: map[graph.Ref]Record{}, current: map[string]graph.Ref{}, links: map[memoryLinkKey]LinkRecord{}}, nil
}

// Use the selected projection's bindings and fail closed if its family/ownership
// facts cannot support a complete retained-owner traversal.
func memoryQueryTypeRefs(catalog *selectedTypeCatalog) (constraint, requires typecatalog.Reference, err error) {
	if catalog == nil || catalog.projection == nil || catalog.types == nil {
		return constraint, requires, fmt.Errorf("%w: absent selected Memory catalog", ErrInvalidStore)
	}
	constraint = typecatalog.Reference{URI: catalog.projectionBinding(memorycatalog.Constraint)}
	requires = typecatalog.Reference{URI: catalog.projectionBinding(memorycatalog.Requires)}
	for _, item := range []struct {
		ref  typecatalog.Reference
		kind graph.ResourceKind
	}{{constraint, graph.KindBead}, {requires, graph.KindLink}} {
		definition, ok := catalog.types.Definition(item.ref.URI)
		if !ok || definition.Kind != item.kind {
			return constraint, requires, fmt.Errorf("%w: missing selected Memory query Type binding", ErrInvalidStore)
		}
	}
	for _, link := range catalog.types.Public(graph.KindLink) {
		if link.Abstract || !catalog.types.Conforms(link.Identity, requires) {
			continue
		}
		owned, e := catalog.types.Owns(constraint, link.Identity)
		if e != nil || !owned {
			return constraint, requires, fmt.Errorf("%w: Requires family must be Constraint-owned", ErrInvalidStore)
		}
	}
	return constraint, requires, nil
}

func (r *memoryQueryReader) requiresFamily(_ context.Context, id string) (bool, error) {
	if _, ok := r.catalog.types.Definition(id); !ok {
		return false, fmt.Errorf("%w: unknown selected Link Type", ErrInvalidStore)
	}
	return r.catalog.types.Conforms(typecatalog.Reference{URI: id}, r.requires), nil
}

func (r *memoryQueryReader) Resolve(ctx context.Context, ref graph.Ref) (graph.Bead, error) {
	if err := ctx.Err(); err != nil {
		return graph.Bead{}, err
	}
	if !ref.InScope() || ref.IsZero() {
		return graph.Bead{}, fmt.Errorf("%w: Memory read needs a local Bead", graph.ErrValidation)
	}
	if ref.Pinned() {
		if err := validateVersionToken(ref.Pin()); err != nil {
			return graph.Bead{}, err
		}
		if record, ok := r.records[ref]; ok {
			return memoryQueryBead(record, ref.Path())
		}
	}
	if !ref.Pinned() {
		if exact, ok := r.current[ref.Path()]; ok {
			return memoryQueryBead(r.records[exact], ref.Path())
		}
	}
	if len(r.records) >= r.request.Limits.MaxNodes {
		return graph.Bead{}, fmt.Errorf("%w: scoped Memory node acquisition", memoryquery.ErrLimitExceeded)
	}
	count, err := r.charge(ctx, `SELECT CAST(LEAST(COALESCE(SUM(4*(256+OCTET_LENGTH(path)+OCTET_LENGTH(type_url)+OCTET_LENGTH(revision)+OCTET_LENGTH(resource_kind)+OCTET_LENGTH(allocation_state)+OCTET_LENGTH(backing)+COALESCE(OCTET_LENGTH(backing_key),0)+COALESCE(OCTET_LENGTH(public_id),0))),0),?) AS UNSIGNED),COUNT(*) FROM graph_preview_catalog WHERE (backing='issue' AND backing_key=?) OR (backing IN ('generic','dependency','informational') AND COALESCE(public_id,path)=?)`, ref.Path(), ref.Path())
	if err != nil {
		return graph.Bead{}, err
	}
	if count != 1 {
		return graph.Bead{}, ErrNotFound
	}
	identity, err := resolveAllocatedResourceIdentityInTx(ctx, r.tx, ref.Path())
	if err != nil {
		return graph.Bead{}, err
	}
	privatePath := identity.PrivatePath
	var kind, typ, head, state, backing string
	var key sql.NullString
	if err := r.tx.QueryRowContext(ctx, `SELECT resource_kind,type_url,revision,allocation_state,backing,backing_key FROM graph_preview_catalog WHERE path=?`, privatePath).Scan(&kind, &typ, &head, &state, &backing, &key); err != nil {
		return graph.Bead{}, err
	}
	if state == "deleted" {
		return graph.Bead{}, ErrGone
	}
	if kind != "bead" || state != "live" || backing != "generic" || key.Valid || !authorityID.MatchString(head) {
		return graph.Bead{}, fmt.Errorf("%w: scoped Requires read supports live generic Constraints", ErrCapabilityUnavailable)
	}
	selected := ref.Pin()
	if selected == "" {
		selected = head
	}
	exact, err := graph.NewPublicInScopeRef(ref.Path(), selected)
	if err != nil {
		return graph.Bead{}, err
	}
	// Charge the retained object before any JSON payload is acquired or decoded.
	count, err = r.charge(ctx, `SELECT CAST(LEAST(COALESCE(SUM(2*(256+OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor))),0),?) AS UNSIGNED),COUNT(*) FROM graph_preview_versions WHERE path=? AND version=?`, privatePath, selected)
	if err != nil {
		return graph.Bead{}, err
	}
	if count != 1 {
		if selected == head {
			return graph.Bead{}, fmt.Errorf("%w: missing current retained Memory", ErrInvalidStore)
		}
		return graph.Bead{}, ErrVersionUnknown
	}
	var owned sql.NullInt64
	if err := r.tx.QueryRowContext(ctx, `SELECT CASE WHEN JSON_VALID(CAST(snapshot AS CHAR)) THEN JSON_LENGTH(JSON_EXTRACT(CAST(snapshot AS CHAR),'$.owned')) ELSE NULL END FROM graph_preview_versions WHERE path=? AND version=?`, privatePath, selected).Scan(&owned); err != nil {
		return graph.Bead{}, err
	}
	if !owned.Valid || owned.Int64 < 0 {
		return graph.Bead{}, fmt.Errorf("%w: malformed retained owned set", ErrInvalidStore)
	}
	if owned.Int64 > int64(r.request.Limits.MaxInspected-r.inspected) {
		return graph.Bead{}, fmt.Errorf("%w: scoped Memory owned-Link acquisition", memoryquery.ErrLimitExceeded)
	}
	var record Record
	if selected == head {
		if _, err := r.charge(ctx, `SELECT CAST(LEAST(COALESCE(SUM(2*(256+OCTET_LENGTH(properties)+COALESCE(OCTET_LENGTH(metadata),0))),0),?) AS UNSIGNED),COUNT(*) FROM graph_preview_payloads WHERE path=?`, privatePath); err != nil {
			return graph.Bead{}, err
		}
		if err := r.chargeCurrentLinks(ctx, privatePath); err != nil {
			return graph.Bead{}, err
		}
		record, err = r.store.showMemoryInTx(ctx, r.tx, privatePath)
	} else {
		var value any
		value, err = r.store.readVersionInTx(ctx, r.tx, privatePath, selected)
		if err == nil {
			var ok bool
			record, ok = value.(Record)
			if !ok {
				return graph.Bead{}, fmt.Errorf("%w: retained Requires endpoint is not Memory", ErrInvalidStore)
			}
		}
	}
	if err != nil {
		return graph.Bead{}, err
	}
	if !r.catalog.types.Conforms(typecatalog.Reference{URI: record.Type}, r.constraint) {
		return graph.Bead{}, fmt.Errorf("%w: Requires root/target must affiliate with Constraint", ErrCapabilityUnavailable)
	}
	if selected != head {
		r.inspected += len(record.Owned)
	}
	r.records[exact] = record
	if selected == head {
		r.current[ref.Path()] = exact
	}
	return memoryQueryBead(record, ref.Path())
}

func (r *memoryQueryReader) Outgoing(ctx context.Context, source graph.Ref, allowance int) ([]graph.Link, error) {
	if _, err := r.Resolve(ctx, source); err != nil {
		return nil, err
	}
	record, ok := r.records[source]
	if !ok {
		return nil, fmt.Errorf("%w: outgoing needs an exact selected source", memoryquery.ErrReadContext)
	}
	// Every Requires refinement is owned under the selected catalog. Thus the
	// retained owner's owned set is complete for this traversal, without joining
	// current Links onto a historical owner or acquiring unowned References.
	if len(record.Owned) > allowance {
		return nil, fmt.Errorf("%w: inspected owned Links", memoryquery.ErrLimitExceeded)
	}
	out := make([]graph.Link, 0, len(record.Owned))
	for _, raw := range record.Owned {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var link LinkRecord
		if err := json.Unmarshal(raw, &link); err != nil {
			return nil, err
		}
		yes, err := r.requiresFamily(ctx, link.Type)
		if err != nil {
			return nil, err
		}
		if !yes {
			continue
		}
		src, err := graph.ParsePublicRef(r.store.ScopeURL(), link.Source, link.SourceRevision)
		if err != nil {
			return nil, err
		}
		target, err := graph.ParsePublicRef(r.store.ScopeURL(), link.Target, link.TargetRevision)
		if err != nil {
			return nil, err
		}
		revision, err := graph.NewRevision(link.Revision)
		if err != nil {
			return nil, err
		}
		rawProperties, err := canonicalJSON(link.Properties)
		if err != nil {
			return nil, err
		}
		properties, err := graph.NewProperties(rawProperties)
		if err != nil {
			return nil, err
		}
		path, err := graph.ScopeLocalPublicResourceID(r.store.ScopeURL(), link.ID)
		if err != nil {
			return nil, err
		}
		value, err := graph.NewPublicLink(graph.LinkSpec{Path: path, TypeURL: link.Type, Revision: revision, Source: src, Target: target, Properties: properties})
		if err != nil {
			return nil, err
		}
		r.links[memoryLinkKey{path, link.Revision}] = link
		out = append(out, value)
	}
	return out, nil
}

func memoryQueryBead(record Record, path string) (graph.Bead, error) {
	revision, err := graph.NewRevision(record.Revision)
	if err != nil {
		return graph.Bead{}, err
	}
	raw, err := canonicalJSON(record.Properties)
	if err != nil {
		return graph.Bead{}, err
	}
	properties, err := graph.NewProperties(raw)
	if err != nil {
		return graph.Bead{}, err
	}
	return graph.NewPublicBead(graph.BeadSpec{Path: path, TypeURL: record.Type, Revision: revision, Properties: properties})
}

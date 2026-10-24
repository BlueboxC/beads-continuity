// Package memoryquery provides language-independent Memory graph operations.
// It consumes an authorized, coherent read context; it does not open storage,
// resolve Type catalogs, interpret prose, or define a query wire format.
package memoryquery

import (
	"context"
	"errors"
	"fmt"
	"slices"

	graph "github.com/steveyegge/beads/graphops"
)

var (
	// ErrLimitExceeded means the complete answer exceeds the supplied bounds.
	ErrLimitExceeded = errors.New("memory query work limit exceeded")
	// ErrReadContext means a reader returned inconsistent or incomplete data.
	ErrReadContext = errors.New("memory query read context is inconsistent")
)

// ReadContext reads one authorized, coherent graph view. Implementations own
// permissions, revision retention, reference resolution, Type/endpoint/payload
// validation and acquisition budgets. An unavailable read is an error, never an
// empty set. The caller selects a valid Constraint root; this operation does not
// replace Type validation or accept a new Type declaration.
//
// Resolve must honor a Reference's pin exactly. For an unpinned Reference it
// selects a revision within this same context. No external dereference is
// implied; unsupported references must fail.
//
// Outgoing returns a complete candidate set for the selected exact Bead revision,
// once per Link identity. It must contain every Requires-family Link. A reader
// using the same selected Type context may omit proven nonmembers (for example,
// unowned References); this is not a generic incident-Link API.
// It must refuse rather than truncate if
// its returned candidate set would exceed allowance. The allowance counts
// returned Links, including any nonmembers the reader returns. Total storage
// acquisition (including proven nonmembers omitted from the candidate set) is
// separately bounded by the provider, which may impose a stricter budget. Endpoint pins do not change
// incidence/ownership: graph.Ref.SameURI remains the existing identity rule.
// The provider must preserve retained owned-Link context rather than joining
// current Links to an older selected Bead.
type ReadContext interface {
	Resolve(context.Context, graph.Ref) (graph.Bead, error)
	Outgoing(ctx context.Context, source graph.Ref, allowance int) ([]graph.Link, error)
}

// RequiresFamily classifies one installed Link Type against the caller's
// selected Type context. It includes Requires itself and all its refinements.
// Errors are distinct from nonmembership; this package builds no Type catalog.
type RequiresFamily func(context.Context, string) (bool, error)

// Limits bound the complete result and returned candidate work. MaxNodes includes
// the root, MaxLinks counts selected Link witnesses (including distinct exact
// source contexts for one retained Link), and MaxInspected
// counts every Link returned by Outgoing, whether selected or not. A provider
// separately bounds its storage acquisition, including omitted candidates. Exact caps
// succeed; exceeding one refuses with no partial Graph. No zero means unlimited.
type Limits struct {
	MaxNodes     int
	MaxLinks     int
	MaxInspected int
}

// Witness retains the original Link and its resolved exact source/target
// References. The resolved References do not rewrite the Link's authored pins.
type Witness struct {
	Link   graph.Link
	Source graph.Ref
	Target graph.Ref
}

// Graph is the complete finite Requires closure, including the selected root.
// Nodes and witness Links have deterministic identity/revision order. A node
// appears once per exact Reference; parallel Link identities remain distinct.
// This is internal operation data, not a BDP Resource or CLI output schema.
type Graph struct {
	Nodes []graph.Bead
	Links []Witness
}

// Requires returns the complete outward Requires-family witness graph for an
// already selected exact root. It makes no satisfaction/violation inference.
// Any validation, provider, family, cancellation or limit error returns zero
// Graph. Success means closure only within the reader's authorized context.
//
// The breadth-first traversal adapts the separate visited-node/emitted-Link
// approach of donnabox/beads PR58, source 9c86d6d1559ffcfcd9770b64e17a7f1f654690b3,
// cmd/bd/graph_preview_generic_projection.go. Its direction/diamond/parallel
// regression intent is preserved; the existing preview and tests remain intact.
func Requires(ctx context.Context, reader ReadContext, family RequiresFamily, root graph.Ref, limits Limits) (Graph, error) {
	if err := ctx.Err(); err != nil {
		return Graph{}, err
	}
	if reader == nil || family == nil || root.IsZero() || !root.InScope() || !root.Pinned() {
		return Graph{}, fmt.Errorf("%w: Requires needs a reader, family and exact local root", graph.ErrValidation)
	}
	if limits.MaxNodes < 1 || limits.MaxLinks < 0 || limits.MaxInspected < 0 {
		return Graph{}, fmt.Errorf("%w: invalid Requires limits", graph.ErrValidation)
	}
	start, err := resolve(ctx, reader, root)
	if err != nil {
		return Graph{}, err
	}
	nodes := map[graph.Ref]graph.Bead{root: start}
	queue := []graph.Ref{root}
	links := []Witness{}
	inspected := 0
	for cursor := 0; cursor < len(queue); cursor++ {
		if err := ctx.Err(); err != nil {
			return Graph{}, err
		}
		source := queue[cursor]
		outgoing, err := reader.Outgoing(ctx, source, limits.MaxInspected-inspected)
		if err != nil {
			return Graph{}, err
		}
		if err := ctx.Err(); err != nil {
			return Graph{}, err
		}
		if len(outgoing) > limits.MaxInspected-inspected {
			return Graph{}, fmt.Errorf("%w: inspected Links exceed %d", ErrLimitExceeded, limits.MaxInspected)
		}
		inspected += len(outgoing)
		seenLinks := map[string]bool{}
		for _, link := range outgoing {
			if err := ctx.Err(); err != nil {
				return Graph{}, err
			}
			if link.IsZero() || !link.Source().SameURI(source) {
				return Graph{}, fmt.Errorf("%w: outgoing Link has a different source", ErrReadContext)
			}
			if seenLinks[link.Path()] {
				return Graph{}, fmt.Errorf("%w: duplicate outgoing Link %s", ErrReadContext, link.Path())
			}
			seenLinks[link.Path()] = true
			selected, err := family(ctx, link.TypeURL())
			if err != nil {
				return Graph{}, err
			}
			if err := ctx.Err(); err != nil {
				return Graph{}, err
			}
			if !selected {
				continue
			}
			if len(links) >= limits.MaxLinks {
				return Graph{}, fmt.Errorf("%w: selected Links exceed %d", ErrLimitExceeded, limits.MaxLinks)
			}
			targetBead, err := resolve(ctx, reader, link.Target())
			if err != nil {
				return Graph{}, err
			}
			target, err := graph.NewPublicInScopeRef(targetBead.Path(), targetBead.Revision().String())
			if err != nil {
				return Graph{}, err
			}
			if _, seen := nodes[target]; !seen {
				if len(nodes) >= limits.MaxNodes {
					return Graph{}, fmt.Errorf("%w: selected nodes exceed %d", ErrLimitExceeded, limits.MaxNodes)
				}
				nodes[target] = targetBead
				queue = append(queue, target)
			}
			// Keep the Link independently from target-node visitation. The same
			// retained Link may participate at different selected source revisions;
			// its witness carries that exact source context instead of coalescing it.
			links = append(links, Witness{Link: link, Source: source, Target: target})
		}
	}
	result := Graph{Nodes: make([]graph.Bead, 0, len(nodes)), Links: make([]Witness, 0, len(links))}
	for _, bead := range nodes {
		result.Nodes = append(result.Nodes, bead)
	}
	for _, witness := range links {
		result.Links = append(result.Links, witness)
	}
	slices.SortFunc(result.Nodes, func(a, b graph.Bead) int {
		if cmp := graph.CompareCodeUnits(a.Path(), b.Path()); cmp != 0 {
			return cmp
		}
		return graph.CompareCodeUnits(a.Revision().String(), b.Revision().String())
	})
	slices.SortFunc(result.Links, func(a, b Witness) int {
		if cmp := graph.CompareCodeUnits(a.Link.Path(), b.Link.Path()); cmp != 0 {
			return cmp
		}
		if cmp := graph.CompareCodeUnits(a.Link.Revision().String(), b.Link.Revision().String()); cmp != 0 {
			return cmp
		}
		return graph.CompareCodeUnits(a.Source.Pin(), b.Source.Pin())
	})
	if err := ctx.Err(); err != nil {
		return Graph{}, err
	}
	return result, nil
}

func resolve(ctx context.Context, reader ReadContext, ref graph.Ref) (graph.Bead, error) {
	if err := ctx.Err(); err != nil {
		return graph.Bead{}, err
	}
	if !ref.InScope() {
		return graph.Bead{}, fmt.Errorf("%w: Requires cannot resolve an external target", graph.ErrValidation)
	}
	bead, err := reader.Resolve(ctx, ref)
	if err != nil {
		return graph.Bead{}, err
	}
	if err := ctx.Err(); err != nil {
		return graph.Bead{}, err
	}
	if bead.IsZero() || bead.Path() != ref.Path() || (ref.Pinned() && bead.Revision().String() != ref.Pin()) {
		return graph.Bead{}, fmt.Errorf("%w: resolved Bead differs from requested reference", ErrReadContext)
	}
	return bead, nil
}

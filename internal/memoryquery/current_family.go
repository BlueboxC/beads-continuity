package memoryquery

import (
	"context"
	"fmt"
	"slices"

	graph "github.com/steveyegge/beads/graphops"
)

// currentFamilyBound is this internal preview operation's root/depth cap.
// It matches graphstore's preview limits without importing storage into this module.
const currentFamilyBound = 1000

// CurrentNode binds an identity to its checked current Type and revision. It
// deliberately contains no payload: frontier checks need not hydrate neighbors.
// Canonical records are acquired by the storage adapter after node selection.
type CurrentNode struct{ Path, TypeURL, Version string }

// CurrentContext supplies one coherent, authorized current view. Incident is
// complete BOTH-direction incidence, once per Link identity, including unowned
// Links. It refuses rather than truncates above allowance. Current acquires only
// identity/Type/version facts, not historical payload. Storage separately bounds
// all acquisition, including proven nonmembers and canonical owner validation.
// Pins are preserved; CurrentFamily checks selected references against current
// versions and never silently resolves an old pin to a current record.
type CurrentContext interface {
	Current(context.Context, string) (CurrentNode, error)
	Incident(context.Context, string, int) ([]graph.Link, error)
}

// CurrentRoot is one independent traversal slot. Repeated identities with
// different depths remain distinct slots in the answer.
type CurrentRoot struct {
	Path  string
	Depth int
}

// CurrentView is root-local membership, in code-unit order. Depth chooses nodes;
// For CurrentFamily, Links is the induced selected-family graph on those nodes;
// Frontier contains reached nodes with eligible directional neighbors outside
// that root's set. Fixed direct/staged operations document their narrower edge
// scopes and operation-specific depth/frontier meaning separately.
type CurrentView struct {
	Root                   CurrentRoot
	Nodes, Links, Frontier []string
}

// CurrentGraph unions completed root views, without re-inducing over their union.
// These are internal operation values, not a wire or CLI result schema.
type CurrentGraph struct {
	Nodes []CurrentNode
	Links []graph.Link
	Views []CurrentView
}

// ErrCurrentContextUnavailable refuses selected pins that this current-only
// operation cannot bind. It is not an empty answer, historical lookup or frontier.
var ErrCurrentContextUnavailable = fmt.Errorf("selected reference is outside the current read context")

// CurrentFamily mechanically adapts PR176 8dbb393c's separate BFS and induced
// edge/frontier passes (cmd/bd/graph_preview_generic_projection.go). It preserves
// authored direction, parallel edges and cycle closure. Each root has its own
// distance map, while output and candidate-work limits accumulate across roots.
// MaxInspected counts each candidate processing visit, including nonmembers and
// repeated visits in different root views, not merely distinct output Links.
// Every error returns zero state. It performs no satisfaction inference.
func CurrentFamily(ctx context.Context, reader CurrentContext, family func(context.Context, string) (bool, error), roots []CurrentRoot, direction graph.Direction, limits Limits) (CurrentGraph, error) {
	if err := validateCurrentWalk(ctx, reader, family, roots, direction, limits); err != nil {
		return CurrentGraph{}, err
	}
	w := newCurrentWalk(ctx, reader, limits)
	views := make([]CurrentView, 0, len(roots))
	matcher := newCurrentMatcher(family)
	for _, root := range roots {
		view, err := w.walk(root, []string{root.Path}, matcher, direction)
		if err != nil {
			return CurrentGraph{}, err
		}
		views = append(views, view)
	}
	return w.finish(views)
}

func validateCurrentWalk(ctx context.Context, reader CurrentContext, family func(context.Context, string) (bool, error), roots []CurrentRoot, direction graph.Direction, limits Limits) error {
	if err := validateCurrentRootCount(ctx, len(roots)); err != nil {
		return err
	}
	if reader == nil || family == nil || !direction.Valid() || limits.MaxNodes < 1 || limits.MaxLinks < 0 || limits.MaxInspected < 0 {
		return fmt.Errorf("%w: invalid current family request", graph.ErrValidation)
	}
	for _, root := range roots {
		if graph.ValidateCanonicalResourceID(root.Path) != nil || root.Depth < 0 || root.Depth > currentFamilyBound {
			return fmt.Errorf("%w: invalid current root", graph.ErrValidation)
		}
	}
	return nil
}

type currentWalk struct {
	ctx    context.Context
	reader CurrentContext
	limits Limits
	nodes  map[string]CurrentNode
	links  map[string]graph.Link
	feeds  map[string][]graph.Link
	facts  map[string]CurrentNode
	work   int
}

func newCurrentWalk(ctx context.Context, reader CurrentContext, limits Limits) *currentWalk {
	return &currentWalk{ctx: ctx, reader: reader, limits: limits, nodes: map[string]CurrentNode{}, links: map[string]graph.Link{}, feeds: map[string][]graph.Link{}, facts: map[string]CurrentNode{}}
}
func (w *currentWalk) current(path string) (CurrentNode, error) {
	if n, ok := w.facts[path]; ok {
		return n, nil
	}
	n, err := w.reader.Current(w.ctx, path)
	if err != nil {
		return CurrentNode{}, err
	}
	if n.Path != path || n.TypeURL == "" || n.Version == "" {
		return CurrentNode{}, fmt.Errorf("%w: invalid current identity", ErrReadContext)
	}
	w.facts[path] = n
	return n, nil
}

func (w *currentWalk) admit(path string) error {
	if _, ok := w.nodes[path]; ok {
		return nil
	}
	if len(w.nodes) >= w.limits.MaxNodes {
		return fmt.Errorf("%w: current node union", ErrLimitExceeded)
	}
	n, err := w.current(path)
	if err != nil {
		return err
	}
	w.nodes[path] = n
	return nil
}

func (w *currentWalk) incident(path string) ([]graph.Link, error) {
	if v, ok := w.feeds[path]; ok {
		return v, nil
	}
	v, err := w.reader.Incident(w.ctx, path, w.limits.MaxInspected-w.work)
	if err != nil {
		return nil, err
	}
	if len(v) > w.limits.MaxInspected-w.work {
		return nil, fmt.Errorf("%w: incidence acquisition", ErrLimitExceeded)
	}
	seen := map[string]bool{}
	for _, l := range v {
		if l.IsZero() || !l.Source().InScope() || !l.Target().InScope() || (l.Source().Path() != path && l.Target().Path() != path) || seen[l.Path()] {
			return nil, fmt.Errorf("%w: invalid/duplicate current incidence", ErrReadContext)
		}
		seen[l.Path()] = true
	}
	w.feeds[path] = v
	return v, nil
}

func (w *currentWalk) visit() error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.work >= w.limits.MaxInspected {
		return fmt.Errorf("%w: current traversal candidate work", ErrLimitExceeded)
	}
	w.work++
	return nil
}

func (w *currentWalk) bind(l graph.Link) error {
	for _, ref := range []graph.Ref{l.Source(), l.Target()} {
		if !ref.Pinned() {
			continue
		}
		n, err := w.current(ref.Path())
		if err != nil {
			return err
		}
		if ref.Pin() != n.Version {
			return fmt.Errorf("%w: %s", ErrCurrentContextUnavailable, ref.Path())
		}
	}
	return nil
}

func currentNeighbor(direction graph.Direction, id string, l graph.Link) (string, bool) {
	switch direction {
	case graph.DirectionOut:
		return l.Target().Path(), l.Source().Path() == id
	case graph.DirectionIn:
		return l.Source().Path(), l.Target().Path() == id
	default:
		if l.Source().Path() == id {
			return l.Target().Path(), true
		}
		return l.Source().Path(), true
	}
}

// walk admits one phase's seed set. Distances/induction/frontiers are local to
// this phase, while acquisition, node/Link union and work are shared across phases.
func (w *currentWalk) walk(root CurrentRoot, seeds []string, family *currentMatcher, direction graph.Direction) (CurrentView, error) {
	distance := map[string]int{}
	queue := []string{}
	for _, seed := range seeds {
		if _, ok := distance[seed]; ok {
			continue
		}
		if err := w.admit(seed); err != nil {
			return CurrentView{}, err
		}
		distance[seed] = 0
		queue = append(queue, seed)
	}
	for cursor := 0; cursor < len(queue); cursor++ {
		if err := w.ctx.Err(); err != nil {
			return CurrentView{}, err
		}
		id := queue[cursor]
		if distance[id] >= root.Depth {
			continue
		}
		feed, err := w.incident(id)
		if err != nil {
			return CurrentView{}, err
		}
		for _, l := range feed {
			if err := w.visit(); err != nil {
				return CurrentView{}, err
			}
			yes, err := family.matches(w.ctx, l.TypeURL())
			if err != nil {
				return CurrentView{}, err
			}
			neighbor, eligible := currentNeighbor(direction, id, l)
			if !yes || !eligible {
				continue
			}
			if err := w.bind(l); err != nil {
				return CurrentView{}, err
			}
			if _, seen := distance[neighbor]; !seen {
				if err := w.admit(neighbor); err != nil {
					return CurrentView{}, err
				}
				distance[neighbor] = distance[id] + 1
				queue = append(queue, neighbor)
			}
		}
	}
	view := CurrentView{Root: root, Nodes: []string{}, Links: []string{}, Frontier: []string{}}
	emitted := map[string]graph.Link{}
	for _, id := range queue {
		if err := w.ctx.Err(); err != nil {
			return CurrentView{}, err
		}
		view.Nodes = append(view.Nodes, id)
		feed, err := w.incident(id)
		if err != nil {
			return CurrentView{}, err
		}
		frontier := false
		for _, l := range feed {
			if err := w.visit(); err != nil {
				return CurrentView{}, err
			}
			yes, err := family.matches(w.ctx, l.TypeURL())
			if err != nil {
				return CurrentView{}, err
			}
			if !yes {
				continue
			}
			_, src := distance[l.Source().Path()]
			_, dst := distance[l.Target().Path()]
			neighbor, eligible := currentNeighbor(direction, id, l)
			_, reached := distance[neighbor]
			if (src && dst) || (eligible && !reached) {
				if err := w.bind(l); err != nil {
					return CurrentView{}, err
				}
			}
			if src && dst {
				emitted[l.Path()] = l
			}
			if eligible && !reached {
				frontier = true
			}
		}
		if frontier {
			view.Frontier = append(view.Frontier, id)
		}
	}
	for path, l := range emitted {
		if err := w.selectLink(l); err != nil {
			return CurrentView{}, err
		}
		view.Links = append(view.Links, path)
	}
	sortCurrentPaths(view.Nodes)
	sortCurrentPaths(view.Links)
	sortCurrentPaths(view.Frontier)
	return view, nil
}

func (w *currentWalk) finish(views []CurrentView) (CurrentGraph, error) {
	result := CurrentGraph{Views: views, Nodes: []CurrentNode{}, Links: []graph.Link{}}
	for _, n := range w.nodes {
		result.Nodes = append(result.Nodes, n)
	}
	for _, l := range w.links {
		result.Links = append(result.Links, l)
	}
	slices.SortFunc(result.Nodes, func(a, b CurrentNode) int { return graph.CompareCodeUnits(a.Path, b.Path) })
	slices.SortFunc(result.Links, func(a, b graph.Link) int { return graph.CompareCodeUnits(a.Path(), b.Path()) })
	if err := w.ctx.Err(); err != nil {
		return CurrentGraph{}, err
	}
	return result, nil
}

func sortCurrentPaths(v []string) { slices.SortFunc(v, graph.CompareCodeUnits) }

func (w *currentWalk) selectLink(l graph.Link) error {
	if _, ok := w.links[l.Path()]; !ok && len(w.links) >= w.limits.MaxLinks {
		return fmt.Errorf("%w: current Link union", ErrLimitExceeded)
	}
	w.links[l.Path()] = l
	return nil
}

func validateCurrentRootCount(ctx context.Context, count int) error {
	if ctx == nil {
		return fmt.Errorf("%w: missing context", graph.ErrValidation)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if count == 0 || count > currentFamilyBound {
		return fmt.Errorf("%w: invalid current root count", graph.ErrValidation)
	}
	return nil
}

// A separate matcher per selected family preserves per-read memoization without
// allowing one phase's false classification to hide another phase's members.
type currentMatcher struct {
	family     func(context.Context, string) (bool, error)
	classified map[string]bool
}

func newCurrentMatcher(family func(context.Context, string) (bool, error)) *currentMatcher {
	return &currentMatcher{family: family, classified: map[string]bool{}}
}
func (m *currentMatcher) matches(ctx context.Context, typ string) (bool, error) {
	if yes, ok := m.classified[typ]; ok {
		return yes, nil
	}
	yes, err := m.family(ctx, typ)
	if err == nil {
		m.classified[typ] = yes
	}
	return yes, err
}

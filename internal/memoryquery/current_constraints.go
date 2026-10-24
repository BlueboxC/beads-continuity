package memoryquery

import (
	"context"
	"fmt"
	graph "github.com/steveyegge/beads/graphops"
	"slices"
)

// ConstraintRoot is an original subject with an independent Requires-stage depth.
// RequiresDepth is an internal bound; it does not define a public CLI flag.
type ConstraintRoot struct {
	Path          string
	RequiresDepth int
}

// ConstraintSeed preserves each original Subject To declaration, including
// parallel declarations. Requires.Seeds deduplicates their targets separately.
type ConstraintSeed struct{ Link, Target string }

// CurrentStage is membership in one original root's Requires phase. Its node and
// frontier sets do not include the original subject merely because it is a root.
// Frontier describes only this Requires question. It says nothing about further
// Subject To declarations or Requires edges outside this phase's reached set.
type CurrentStage struct{ Seeds, Nodes, Links, Frontier []string }

// ConstraintStage keeps phase witnesses separate from the combined graph. Human
// tree occurrences use (root slot, phase, identity): an original Constraint may
// reappear in Requires and expand there, but not at its original Subject To
// occurrence. Remaining selected links are references, not new expansions.
type ConstraintStage struct {
	Root      ConstraintRoot
	SubjectTo []ConstraintSeed
	Requires  CurrentStage
}

// CurrentConstraintGraph combines the two declared phases per root. A combined
// view's Root.Depth is its Requires depth, not the Subject To hop count. Its
// Frontier is exclusively the Requires-stage frontier; Stages is authoritative.
type CurrentConstraintGraph struct {
	Graph  CurrentGraph
	Stages []ConstraintStage
}

// CurrentSubjects selects only direct incoming Subject To declarations. It does
// not induce secondary links between subjects or traverse their requirements.
// Views use Root.Depth=1 and have no transitive frontier: all direct declarations
// to the root are complete, and declarations to its subjects are not this question.
// The adapter admits Constraint roots using the canonical Type engine.
func CurrentSubjects(ctx context.Context, reader CurrentContext, subjectTo func(context.Context, string) (bool, error), roots []string, limits Limits) (CurrentGraph, error) {
	if err := validateCurrentRootCount(ctx, len(roots)); err != nil {
		return CurrentGraph{}, err
	}
	slots := make([]CurrentRoot, len(roots))
	for i, root := range roots {
		slots[i] = CurrentRoot{Path: root}
	}
	if err := validateCurrentWalk(ctx, reader, subjectTo, slots, graph.DirectionIn, limits); err != nil {
		return CurrentGraph{}, err
	}
	w := newCurrentWalk(ctx, reader, limits)
	views := make([]CurrentView, 0, len(roots))
	matcher := newCurrentMatcher(subjectTo)
	for _, root := range slots {
		view, _, err := w.declarations(root.Path, matcher, graph.DirectionIn)
		if err != nil {
			return CurrentGraph{}, err
		}
		views = append(views, view)
	}
	return w.finish(views)
}

// CurrentConstraints follows Subject To once from each original subject, then
// Requires (including Defines) from that root's complete seed set. It never
// replays Subject To at reached Constraints, nor induces over phases/root unions.
// Acquisition caches and all counters are shared across roots and phases. Every
// error returns zero state; selected old pins are refused, never unpinned.
func CurrentConstraints(ctx context.Context, reader CurrentContext, subjectTo, requires func(context.Context, string) (bool, error), roots []ConstraintRoot, limits Limits) (CurrentConstraintGraph, error) {
	if err := validateCurrentRootCount(ctx, len(roots)); err != nil {
		return CurrentConstraintGraph{}, err
	}
	slots := make([]CurrentRoot, len(roots))
	for i, root := range roots {
		slots[i] = CurrentRoot{Path: root.Path, Depth: root.RequiresDepth}
	}
	if err := validateCurrentWalk(ctx, reader, subjectTo, slots, graph.DirectionOut, limits); err != nil {
		return CurrentConstraintGraph{}, err
	}
	if requires == nil {
		return CurrentConstraintGraph{}, fmt.Errorf("%w: missing Requires classifier", graph.ErrValidation)
	}
	w := newCurrentWalk(ctx, reader, limits)
	result := CurrentConstraintGraph{Stages: make([]ConstraintStage, 0, len(roots))}
	views := make([]CurrentView, 0, len(roots))
	subjectMatcher, requiresMatcher := newCurrentMatcher(subjectTo), newCurrentMatcher(requires)
	for i, root := range roots {
		direct, declarations, err := w.declarations(root.Path, subjectMatcher, graph.DirectionOut)
		if err != nil {
			return CurrentConstraintGraph{}, err
		}
		seeds := []string{}
		for _, seed := range declarations {
			seeds = append(seeds, seed.Target)
		}
		sortCurrentPaths(seeds)
		seeds = slices.Compact(seeds)
		phase, err := w.walk(slots[i], seeds, requiresMatcher, graph.DirectionOut)
		if err != nil {
			return CurrentConstraintGraph{}, err
		}
		result.Stages = append(result.Stages, ConstraintStage{Root: root, SubjectTo: declarations, Requires: CurrentStage{Seeds: seeds, Nodes: phase.Nodes, Links: phase.Links, Frontier: phase.Frontier}})
		views = append(views, CurrentView{Root: slots[i], Nodes: unionCurrentPaths(direct.Nodes, phase.Nodes), Links: unionCurrentPaths(direct.Links, phase.Links), Frontier: slices.Clone(phase.Frontier)})
	}
	var err error
	result.Graph, err = w.finish(views)
	if err != nil {
		return CurrentConstraintGraph{}, err
	}
	return result, nil
}

// declarations deliberately emits only the selected direct incidence, not the
// induced graph on the root and its direct neighbors. The two callers supply
// fixed opposite directions, not an arbitrary stage or query language.
func (w *currentWalk) declarations(root string, family *currentMatcher, direction graph.Direction) (CurrentView, []ConstraintSeed, error) {
	view := CurrentView{Root: CurrentRoot{Path: root, Depth: 1}, Nodes: []string{root}, Links: []string{}, Frontier: []string{}}
	seeds := []ConstraintSeed{}
	if err := w.admit(root); err != nil {
		return CurrentView{}, nil, err
	}
	feed, err := w.incident(root)
	if err != nil {
		return CurrentView{}, nil, err
	}
	for _, l := range feed {
		if err := w.visit(); err != nil {
			return CurrentView{}, nil, err
		}
		yes, err := family.matches(w.ctx, l.TypeURL())
		if err != nil {
			return CurrentView{}, nil, err
		}
		neighbor, eligible := currentNeighbor(direction, root, l)
		if !yes || !eligible {
			continue
		}
		if err := w.bind(l); err != nil {
			return CurrentView{}, nil, err
		}
		if err := w.admit(neighbor); err != nil {
			return CurrentView{}, nil, err
		}
		if err := w.selectLink(l); err != nil {
			return CurrentView{}, nil, err
		}
		view.Nodes = append(view.Nodes, neighbor)
		view.Links = append(view.Links, l.Path())
		seeds = append(seeds, ConstraintSeed{Link: l.Path(), Target: neighbor})
	}
	sortCurrentPaths(view.Nodes)
	view.Nodes = slices.Compact(view.Nodes)
	sortCurrentPaths(view.Links)
	slices.SortFunc(seeds, func(a, b ConstraintSeed) int { return graph.CompareCodeUnits(a.Link, b.Link) })
	return view, seeds, nil
}
func unionCurrentPaths(a, b []string) []string {
	out := append(slices.Clone(a), b...)
	sortCurrentPaths(out)
	return slices.Compact(out)
}

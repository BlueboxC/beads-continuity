package memoryquery_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memoryquery"
)

type currentReads struct {
	badCurrent *memoryquery.CurrentNode
	badFeed    []graph.Link

	nodes map[string]memoryquery.CurrentNode
	links []graph.Link
	hook  func()
	calls map[string]int
}

func (r *currentReads) Current(_ context.Context, path string) (memoryquery.CurrentNode, error) {
	if r.badCurrent != nil {
		return *r.badCurrent, nil
	}
	if r.hook != nil {
		r.hook()
	}
	n, ok := r.nodes[path]
	if !ok {
		return memoryquery.CurrentNode{}, errors.New("missing current node")
	}
	return n, nil
}
func (r *currentReads) Incident(_ context.Context, path string, _ int) ([]graph.Link, error) {
	r.calls[path]++
	if r.badFeed != nil {
		return r.badFeed, nil
	}
	out := []graph.Link{}
	for _, l := range r.links {
		if l.Source().Path() == path || l.Target().Path() == path {
			out = append(out, l)
		}
	}
	return out, nil
}
func currentFixture(t *testing.T, edges [][3]string) *currentReads {
	t.Helper()
	r := &currentReads{nodes: map[string]memoryquery.CurrentNode{}, calls: map[string]int{}}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		r.nodes["beads/"+name] = memoryquery.CurrentNode{Path: "beads/" + name, TypeURL: scope + "types/memory", Version: "current"}
	}
	for _, e := range edges {
		r.links = append(r.links, link(t, e[0], "references", reference(t, e[1], ""), reference(t, e[2], "")))
	}
	return r
}
func allFamily(context.Context, string) (bool, error) { return true, nil }

var currentLimits = memoryquery.Limits{MaxNodes: 20, MaxLinks: 20, MaxInspected: 1000}

func currentRun(t *testing.T, r *currentReads, roots []memoryquery.CurrentRoot, d graph.Direction) memoryquery.CurrentGraph {
	t.Helper()
	g, err := memoryquery.CurrentFamily(context.Background(), r, allFamily, roots, d, currentLimits)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func currentPaths(g memoryquery.CurrentGraph) []string {
	out := []string{}
	for _, l := range g.Links {
		out = append(out, l.Path())
	}
	return out
}
func TestCurrentFamilyInductionAndIndependentRoots(t *testing.T) {
	edges := [][3]string{{"ab", "a", "b"}, {"ac", "a", "c"}, {"bd", "b", "d"}, {"cd", "c", "d"}, {"bc", "b", "c"}, {"reverse", "b", "a"}, {"parallel", "a", "b"}}
	r := currentFixture(t, edges)
	roots := []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 1}, {Path: "beads/b", Depth: 0}}
	got := currentRun(t, r, roots, graph.DirectionOut)
	if !reflect.DeepEqual(got.Views[0].Links, []string{"links/ab", "links/ac", "links/bc", "links/parallel", "links/reverse"}) || !reflect.DeepEqual(got.Views[0].Frontier, []string{"beads/b", "beads/c"}) {
		t.Fatal(got.Views)
	}
	if !reflect.DeepEqual(got.Views[1].Nodes, []string{"beads/b"}) || len(got.Views[1].Links) != 0 || !reflect.DeepEqual(got.Views[1].Frontier, []string{"beads/b"}) {
		t.Fatal(got.Views[1])
	}
	for path, count := range r.calls {
		if count != 1 {
			t.Fatalf("duplicate acquisition %s %d", path, count)
		}
	}
	// Removing BC makes the aggregate diamond case distinguish a per-root union
	// from re-induction over the union: CD must remain absent even though C and D
	// each appear in different depth-one root views.
	r = currentFixture(t, edges[:4])
	roots[1].Depth = 1
	got = currentRun(t, r, roots, graph.DirectionOut)
	if !reflect.DeepEqual(currentPaths(got), []string{"links/ab", "links/ac", "links/bd"}) {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(got.Views[0].Frontier, []string{"beads/b", "beads/c"}) || !reflect.DeepEqual(got.Views[1].Nodes, []string{"beads/b", "beads/d"}) {
		t.Fatal(got.Views)
	}
	roots = append(roots, memoryquery.CurrentRoot{Path: "beads/a", Depth: 0})
	got = currentRun(t, currentFixture(t, edges[:4]), roots, graph.DirectionOut)
	if len(got.Views) != 3 || len(got.Views[2].Nodes) != 1 {
		t.Fatal(got.Views)
	}
}
func TestCurrentFamilyDirectionsCyclesAndPermutation(t *testing.T) {
	edges := [][3]string{{"ab", "a", "b"}, {"ba", "b", "a"}, {"bc", "b", "c"}, {"cb", "c", "b"}, {"parallel", "a", "b"}}
	r := currentFixture(t, edges)
	roots := []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 10}}
	expected := currentRun(t, r, roots, graph.DirectionOut)
	if len(expected.Nodes) != 3 || len(expected.Links) != 5 || len(expected.Views[0].Frontier) != 0 {
		t.Fatal(expected)
	}
	slices.Reverse(r.links)
	if got := currentRun(t, r, roots, graph.DirectionOut); !reflect.DeepEqual(got, expected) {
		t.Fatal("order changed graph")
	}
	got := currentRun(t, r, []memoryquery.CurrentRoot{{Path: "beads/c", Depth: 1}}, graph.DirectionIn)
	if !reflect.DeepEqual(got.Views[0].Nodes, []string{"beads/b", "beads/c"}) || !reflect.DeepEqual(got.Views[0].Links, []string{"links/bc", "links/cb"}) || !reflect.DeepEqual(got.Views[0].Frontier, []string{"beads/b"}) {
		t.Fatal(got)
	}
	got = currentRun(t, r, []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 0}}, graph.DirectionBoth)
	if len(got.Nodes) != 1 || len(got.Links) != 0 || len(got.Views[0].Frontier) != 1 {
		t.Fatal(got)
	}
}
func TestCurrentFamilyPinsAndNonmembers(t *testing.T) {
	r := currentFixture(t, nil)
	r.links = []graph.Link{link(t, "pin", "references", reference(t, "a", ""), reference(t, "b", "current"))}
	roots := []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 1}}
	got := currentRun(t, r, roots, graph.DirectionOut)
	if got.Links[0].Target().Pin() != "current" {
		t.Fatal("pin rewritten")
	}
	r.nodes["beads/b"] = memoryquery.CurrentNode{Path: "beads/b", TypeURL: scope + "types/memory", Version: "new"}
	empty, err := memoryquery.CurrentFamily(context.Background(), r, allFamily, roots, graph.DirectionOut, currentLimits)
	if !errors.Is(err, memoryquery.ErrCurrentContextUnavailable) || !reflect.DeepEqual(empty, memoryquery.CurrentGraph{}) {
		t.Fatal(empty, err)
	}
	// An unselected family is not a request to bind its historical pins.
	no := func(context.Context, string) (bool, error) { return false, nil }
	empty, err = memoryquery.CurrentFamily(context.Background(), r, no, roots, graph.DirectionOut, currentLimits)
	if err != nil || len(empty.Nodes) != 1 || len(empty.Links) != 0 {
		t.Fatal(empty, err)
	}
	// Likewise, an incoming outside neighbor cannot enlarge an outward question.
	r.links = []graph.Link{link(t, "incoming", "references", reference(t, "b", "old"), reference(t, "a", ""))}
	got = currentRun(t, r, roots, graph.DirectionOut)
	if len(got.Nodes) != 1 || len(got.Views[0].Frontier) != 0 {
		t.Fatal(got)
	}
	// Boundary pins still matter to a truthful directional frontier.
	roots[0].Depth = 0
	r.links = []graph.Link{link(t, "frontier", "references", reference(t, "a", ""), reference(t, "b", "old"))}
	empty, err = memoryquery.CurrentFamily(context.Background(), r, allFamily, roots, graph.DirectionOut, currentLimits)
	if !errors.Is(err, memoryquery.ErrCurrentContextUnavailable) || !reflect.DeepEqual(empty, memoryquery.CurrentGraph{}) {
		t.Fatal(empty, err)
	}
}
func TestCurrentFamilyLimitsErrorsAndCancellation(t *testing.T) {
	for _, name := range []string{"nodes", "links", "work", "cancel", "family"} {
		t.Run(name, func(t *testing.T) {
			r := currentFixture(t, [][3]string{{"ab", "a", "b"}})
			limits := currentLimits
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := allFamily
			want := memoryquery.ErrLimitExceeded
			switch name {
			case "nodes":
				limits.MaxNodes = 1
			case "links":
				limits.MaxLinks = 0
			case "work":
				limits.MaxInspected = 1
			case "cancel":
				r.hook = cancel
				want = context.Canceled
			case "family":
				want = errors.New("classifier failed")
				f = func(context.Context, string) (bool, error) { return false, want }
			}
			got, err := memoryquery.CurrentFamily(ctx, r, f, []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 1}}, graph.DirectionOut, limits)
			if !errors.Is(err, want) || !reflect.DeepEqual(got, memoryquery.CurrentGraph{}) {
				t.Fatal(got, err)
			}
		})
	}
	// One candidate occurs in BFS once and in the two induced incidence feeds.
	r := currentFixture(t, [][3]string{{"ab", "a", "b"}})
	limits := memoryquery.Limits{MaxNodes: 2, MaxLinks: 1, MaxInspected: 3}
	if _, err := memoryquery.CurrentFamily(context.Background(), r, allFamily, []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 1}}, graph.DirectionOut, limits); err != nil {
		t.Fatal(err)
	}
	limits.MaxInspected = 2
	if got, err := memoryquery.CurrentFamily(context.Background(), r, allFamily, []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 1}}, graph.DirectionOut, limits); !errors.Is(err, memoryquery.ErrLimitExceeded) || !reflect.DeepEqual(got, memoryquery.CurrentGraph{}) {
		t.Fatal(got, err)
	}
}

func TestCurrentFamilyRepeatedRootSlotsUseUniqueNodeBudget(t *testing.T) {
	r := currentFixture(t, nil)
	g, err := memoryquery.CurrentFamily(context.Background(), r, allFamily, []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 0}, {Path: "beads/a", Depth: 1}}, graph.DirectionOut, memoryquery.Limits{MaxNodes: 1, MaxLinks: 0, MaxInspected: 0})
	if err != nil || len(g.Nodes) != 1 || len(g.Views) != 2 || g.Views[0].Root.Depth == g.Views[1].Root.Depth {
		t.Fatal(g, err)
	}
}

func TestCurrentFamilyBothExpansion(t *testing.T) {
	r := currentFixture(t, [][3]string{{"ab", "a", "b"}, {"bc", "b", "c"}, {"dc", "d", "c"}})
	got := currentRun(t, r, []memoryquery.CurrentRoot{{Path: "beads/b", Depth: 1}}, graph.DirectionBoth)
	if !reflect.DeepEqual(got.Views[0].Nodes, []string{"beads/a", "beads/b", "beads/c"}) || !reflect.DeepEqual(got.Views[0].Links, []string{"links/ab", "links/bc"}) || !reflect.DeepEqual(got.Views[0].Frontier, []string{"beads/c"}) {
		t.Fatal(got)
	}
}
func TestCurrentFamilySourcePin(t *testing.T) {
	r := currentFixture(t, nil)
	r.links = []graph.Link{link(t, "source-pin", "references", reference(t, "a", "current"), reference(t, "b", ""))}
	roots := []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 1}}
	got := currentRun(t, r, roots, graph.DirectionOut)
	if got.Links[0].Source().Pin() != "current" {
		t.Fatal("source pin erased")
	}
	n := r.nodes["beads/a"]
	n.Version = "new"
	r.nodes[n.Path] = n
	read, err := memoryquery.CurrentFamily(context.Background(), r, allFamily, roots, graph.DirectionOut, currentLimits)
	if !errors.Is(err, memoryquery.ErrCurrentContextUnavailable) || !reflect.DeepEqual(read, memoryquery.CurrentGraph{}) {
		t.Fatal(read, err)
	}
}
func TestCurrentFamilyReaderDefenses(t *testing.T) {
	for _, which := range []string{"identity", "duplicate", "nonincident", "zero", "over_allowance"} {
		t.Run(which, func(t *testing.T) {
			r := currentFixture(t, [][3]string{{"ab", "a", "b"}})
			limits := currentLimits
			want := memoryquery.ErrReadContext
			switch which {
			case "identity":
				r.badCurrent = &memoryquery.CurrentNode{Path: "beads/b", TypeURL: scope + "types/memory", Version: "current"}
			case "duplicate":
				r.badFeed = []graph.Link{r.links[0], r.links[0]}
			case "nonincident":
				r.badFeed = []graph.Link{link(t, "bc", "references", reference(t, "b", ""), reference(t, "c", ""))}
			case "zero":
				r.badFeed = []graph.Link{{}}
			case "over_allowance":
				limits.MaxInspected = 1
				r.badFeed = []graph.Link{r.links[0], link(t, "ac", "references", reference(t, "a", ""), reference(t, "c", ""))}
				want = memoryquery.ErrLimitExceeded
			}
			got, err := memoryquery.CurrentFamily(context.Background(), r, allFamily, []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 1}}, graph.DirectionOut, limits)
			if !errors.Is(err, want) || !reflect.DeepEqual(got, memoryquery.CurrentGraph{}) {
				t.Fatal(got, err)
			}
		})
	}
}

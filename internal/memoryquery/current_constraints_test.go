package memoryquery_test

import (
	"context"
	"errors"
	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memoryquery"
	"reflect"
	"slices"
	"testing"
)

func constraintFixture(t *testing.T, edges [][4]string) *currentReads {
	t.Helper()
	r := currentFixture(t, nil)
	for _, e := range edges {
		r.links = append(r.links, link(t, e[0], e[1], reference(t, e[2], ""), reference(t, e[3], "")))
	}
	return r
}
func subjectFamily(_ context.Context, typ string) (bool, error) {
	return typ == scope+"types/subject-to", nil
}
func requiresFamily(_ context.Context, typ string) (bool, error) {
	return typ == scope+"types/requires" || typ == scope+"types/defines", nil
}
func TestCurrentConstraintsPhaseMembershipAndRoots(t *testing.T) {
	edges := [][4]string{{"sab", "subject-to", "a", "b"}, {"parallel", "subject-to", "a", "b"}, {"sac", "subject-to", "a", "c"}, {"ba", "requires", "b", "a"}, {"ad", "requires", "a", "d"}, {"cb", "defines", "c", "b"}, {"sde", "subject-to", "d", "e"}}
	r := constraintFixture(t, edges)
	roots := []memoryquery.ConstraintRoot{{Path: "beads/a", RequiresDepth: 0}, {Path: "beads/a", RequiresDepth: 1}, {Path: "beads/e", RequiresDepth: 3}}
	got, err := memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, requiresFamily, roots, currentLimits)
	if err != nil {
		t.Fatal(err)
	}
	first := got.Stages[0]
	if !reflect.DeepEqual(first.Requires.Seeds, []string{"beads/b", "beads/c"}) || !reflect.DeepEqual(first.SubjectTo, []memoryquery.ConstraintSeed{{Link: "links/parallel", Target: "beads/b"}, {Link: "links/sab", Target: "beads/b"}, {Link: "links/sac", Target: "beads/c"}}) || !reflect.DeepEqual(first.Requires.Nodes, []string{"beads/b", "beads/c"}) || !reflect.DeepEqual(first.Requires.Links, []string{"links/cb"}) || !reflect.DeepEqual(first.Requires.Frontier, []string{"beads/b"}) {
		t.Fatal(first)
	}
	if slices.Contains(got.Graph.Views[0].Links, "links/ba") || slices.Contains(got.Graph.Views[0].Links, "links/ad") {
		t.Fatal("induced across phases", got.Graph.Views[0])
	}
	second := got.Stages[1]
	if !reflect.DeepEqual(second.Requires.Nodes, []string{"beads/a", "beads/b", "beads/c"}) || !reflect.DeepEqual(second.Requires.Links, []string{"links/ba", "links/cb"}) || !reflect.DeepEqual(second.Requires.Frontier, []string{"beads/a"}) {
		t.Fatal(second)
	}
	if !reflect.DeepEqual(got.Graph.Views[2].Nodes, []string{"beads/e"}) || len(got.Stages[2].Requires.Nodes) != 0 || len(got.Graph.Nodes) != 4 {
		t.Fatal(got)
	}
	for id, n := range r.calls {
		if n != 1 {
			t.Fatalf("duplicate acquisition %s %d", id, n)
		}
	}
	slices.Reverse(r.links)
	again, err := memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, requiresFamily, roots, currentLimits)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("unstable phases", again, err)
	}
	// Reaching D does not replay its Subject To edge to E.
	r = constraintFixture(t, edges)
	got, err = memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, requiresFamily, []memoryquery.ConstraintRoot{{Path: "beads/a", RequiresDepth: 4}}, currentLimits)
	if err != nil || len(got.Graph.Nodes) != 4 || slices.Contains(currentPaths(got.Graph), "links/sde") {
		t.Fatal(got, err)
	}
}
func TestCurrentSubjectsDirectScope(t *testing.T) {
	r := constraintFixture(t, [][4]string{{"ab", "subject-to", "a", "b"}, {"ab2", "subject-to", "a", "b"}, {"cb", "subject-to", "c", "b"}, {"ca", "subject-to", "c", "a"}, {"bd", "subject-to", "b", "d"}, {"ac", "requires", "a", "c"}})
	got, err := memoryquery.CurrentSubjects(context.Background(), r, subjectFamily, []string{"beads/b", "beads/d"}, currentLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got.Views[0].Root.Depth != 1 || !reflect.DeepEqual(got.Views[0].Nodes, []string{"beads/a", "beads/b", "beads/c"}) || !reflect.DeepEqual(got.Views[0].Links, []string{"links/ab", "links/ab2", "links/cb"}) || len(got.Views[0].Frontier) != 0 || !reflect.DeepEqual(got.Views[1].Links, []string{"links/bd"}) {
		t.Fatal(got)
	}
	if slices.Contains(currentPaths(got), "links/ca") || slices.Contains(currentPaths(got), "links/ac") {
		t.Fatal("direct result reinduced", got)
	}
}
func TestCurrentConstraintsCumulativeLimitsAndPins(t *testing.T) {
	roots := []memoryquery.ConstraintRoot{{Path: "beads/a", RequiresDepth: 1}, {Path: "beads/a", RequiresDepth: 1}}
	r := constraintFixture(t, [][4]string{{"ab", "subject-to", "a", "b"}, {"bc", "requires", "b", "c"}})
	exact := memoryquery.Limits{MaxNodes: 3, MaxLinks: 2, MaxInspected: 12}
	if _, err := memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, requiresFamily, roots, exact); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"nodes", "links", "work"} {
		t.Run(field, func(t *testing.T) {
			limits := exact
			switch field {
			case "nodes":
				limits.MaxNodes--
			case "links":
				limits.MaxLinks--
			case "work":
				limits.MaxInspected--
			}
			got, err := memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, requiresFamily, roots, limits)
			if !errors.Is(err, memoryquery.ErrLimitExceeded) || !reflect.DeepEqual(got, memoryquery.CurrentConstraintGraph{}) {
				t.Fatal(got, err)
			}
		})
	}
	for _, typ := range []string{"subject-to", "requires"} {
		t.Run(typ, func(t *testing.T) {
			r := constraintFixture(t, [][4]string{{"ab", "subject-to", "a", "b"}, {"bc", "requires", "b", "c"}})
			index := 0
			src, dst := "a", "b"
			if typ == "requires" {
				index = 1
				src, dst = "b", "c"
			}
			r.links[index] = link(t, "pin", typ, reference(t, src, ""), reference(t, dst, "old"))
			got, err := memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, requiresFamily, roots, currentLimits)
			if !errors.Is(err, memoryquery.ErrCurrentContextUnavailable) || !reflect.DeepEqual(got, memoryquery.CurrentConstraintGraph{}) {
				t.Fatal(got, err)
			}
			r.links[index] = link(t, "pin", typ, reference(t, src, "current"), reference(t, dst, "current"))
			got, err = memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, requiresFamily, roots, currentLimits)
			if err != nil || len(got.Graph.Links) != 2 {
				t.Fatal(got, err)
			}
		})
	}
}
func TestCurrentConstraintsCancellationAndInvalid(t *testing.T) {
	roots := []memoryquery.ConstraintRoot{{Path: "beads/a", RequiresDepth: 1}}
	r := currentFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		got, err := memoryquery.CurrentConstraints(ctx, r, subjectFamily, requiresFamily, roots, currentLimits)
		if err == nil || !reflect.DeepEqual(got, memoryquery.CurrentConstraintGraph{}) {
			t.Fatal(got, err)
		}
	}
	got, err := memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, nil, roots, currentLimits)
	if !errors.Is(err, graph.ErrValidation) || !reflect.DeepEqual(got, memoryquery.CurrentConstraintGraph{}) {
		t.Fatal(got, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	r = constraintFixture(t, [][4]string{{"ab", "subject-to", "a", "b"}})
	f := func(ctx context.Context, typ string) (bool, error) { cancel(); return subjectFamily(ctx, typ) }
	got, err = memoryquery.CurrentConstraints(ctx, r, f, requiresFamily, roots, currentLimits)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, memoryquery.CurrentConstraintGraph{}) {
		t.Fatal(got, err)
	}
	subjects, err := memoryquery.CurrentSubjects(context.Background(), r, subjectFamily, []string{"a//b"}, currentLimits)
	if !errors.Is(err, graph.ErrValidation) || !reflect.DeepEqual(subjects, memoryquery.CurrentGraph{}) {
		t.Fatal(subjects, err)
	}
}

func TestCurrentConstraintReadRootCaps(t *testing.T) {
	r := currentFixture(t, nil)
	roots := make([]memoryquery.ConstraintRoot, 1001)
	subjects := make([]string, 1001)
	for i := range roots {
		roots[i] = memoryquery.ConstraintRoot{Path: "beads/a"}
		subjects[i] = "beads/a"
	}
	got, err := memoryquery.CurrentConstraints(context.Background(), r, subjectFamily, requiresFamily, roots, currentLimits)
	if !errors.Is(err, graph.ErrValidation) || !reflect.DeepEqual(got, memoryquery.CurrentConstraintGraph{}) {
		t.Fatal(got, err)
	}
	direct, err := memoryquery.CurrentSubjects(context.Background(), r, subjectFamily, subjects, currentLimits)
	if !errors.Is(err, graph.ErrValidation) || !reflect.DeepEqual(direct, memoryquery.CurrentGraph{}) || len(r.calls) != 0 {
		t.Fatal(direct, err, r.calls)
	}
}

func TestCurrentReadClassificationCacheIsPerFamilyPerRead(t *testing.T) {
	r := constraintFixture(t, [][4]string{{"ab", "subject-to", "a", "b"}, {"bc", "requires", "b", "c"}})
	counts := map[string]int{}
	classifier := func(label string, family func(context.Context, string) (bool, error)) func(context.Context, string) (bool, error) {
		return func(ctx context.Context, typ string) (bool, error) {
			key := label + typ
			counts[key]++
			if counts[key] > 1 {
				return false, errors.New("repeated classification within read family")
			}
			return family(ctx, typ)
		}
	}
	roots := []memoryquery.CurrentRoot{{Path: "beads/a", Depth: 2}, {Path: "beads/a", Depth: 2}}
	if _, err := memoryquery.CurrentFamily(context.Background(), r, classifier("generic", allFamily), roots, graph.DirectionOut, currentLimits); err != nil {
		t.Fatal(err)
	}
	staged := []memoryquery.ConstraintRoot{{Path: "beads/a", RequiresDepth: 1}, {Path: "beads/a", RequiresDepth: 1}}
	if _, err := memoryquery.CurrentConstraints(context.Background(), r, classifier("subject", subjectFamily), classifier("requires", requiresFamily), staged, currentLimits); err != nil {
		t.Fatal(err)
	}
	if _, err := memoryquery.CurrentSubjects(context.Background(), r, classifier("direct", subjectFamily), []string{"beads/b", "beads/b"}, currentLimits); err != nil {
		t.Fatal(err)
	}
}

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

const scope = "https://memory.example/"

type reads struct {
	beads    map[graph.Ref]graph.Bead
	outgoing map[graph.Ref][]graph.Link
	current  map[string]graph.Ref
	readErr  error
	readHook func(graph.Ref)
	outErrAt map[graph.Ref]error
}

func (r reads) Resolve(_ context.Context, ref graph.Ref) (graph.Bead, error) {
	if r.readErr != nil {
		return graph.Bead{}, r.readErr
	}
	if !ref.Pinned() {
		ref = r.current[ref.Path()]
	}
	bead, ok := r.beads[ref]
	if !ok {
		return graph.Bead{}, errors.New("requested revision unavailable")
	}
	return bead, nil
}

func (r reads) Outgoing(_ context.Context, ref graph.Ref, _ int) ([]graph.Link, error) {
	if r.readHook != nil {
		r.readHook(ref)
	}
	if err := r.outErrAt[ref]; err != nil {
		return nil, err
	}
	return slices.Clone(r.outgoing[ref]), r.readErr
}

func reference(t *testing.T, name, revision string) graph.Ref {
	t.Helper()
	ref, err := graph.NewPublicInScopeRef("beads/"+name, revision)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func node(t *testing.T, ref graph.Ref) graph.Bead {
	t.Helper()
	revision, err := graph.NewRevision(ref.Pin())
	if err != nil {
		t.Fatal(err)
	}
	bead, err := graph.NewPublicBead(graph.BeadSpec{Path: ref.Path(), TypeURL: scope + "types/requirement", Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	return bead
}

func link(t *testing.T, name, typ string, source, target graph.Ref) graph.Link {
	t.Helper()
	revision, err := graph.NewRevision("revision-" + name)
	if err != nil {
		t.Fatal(err)
	}
	edge, err := graph.NewPublicLink(graph.LinkSpec{Path: "links/" + name, TypeURL: scope + "types/" + typ, Revision: revision, Source: source, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	return edge
}

func family(_ context.Context, typ string) (bool, error) {
	return typ == scope+"types/requires" || typ == scope+"types/defines", nil
}

func fixture(t *testing.T) (reads, graph.Ref) {
	t.Helper()
	a, b, c, d := reference(t, "a", "1"), reference(t, "b", "1"), reference(t, "c", "1"), reference(t, "d", "1")
	r := reads{beads: map[graph.Ref]graph.Bead{}, outgoing: map[graph.Ref][]graph.Link{}, current: map[string]graph.Ref{}}
	for _, ref := range []graph.Ref{a, b, c, d} {
		r.beads[ref] = node(t, ref)
		r.current[ref.Path()] = ref
	}
	// The graph, diamond/parallel/cycle and input permutation cases adapt PR58's
	// generic traversal regression intent, without changing its existing tests.
	r.outgoing[a] = []graph.Link{
		link(t, "ab", "defines", a, b),
		link(t, "ac", "requires", a, c),
		link(t, "aa", "requires", a, a),
		link(t, "unrelated", "subject-to", a, reference(t, "unread", "1")),
	}
	r.outgoing[b] = []graph.Link{link(t, "bd", "requires", b, d)}
	r.outgoing[c] = []graph.Link{link(t, "cd", "requires", c, d), link(t, "cd-parallel", "requires", c, d)}
	r.outgoing[d] = []graph.Link{link(t, "db", "requires", d, b)}
	return r, a
}

func assertEmpty(t *testing.T, result memoryquery.Graph, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || !reflect.DeepEqual(result, memoryquery.Graph{}) {
		t.Fatalf("want empty result and %v; got %+v, %v", want, result, err)
	}
}

func TestRequiresCompleteWitnessGraph(t *testing.T) {
	r, root := fixture(t)
	limits := memoryquery.Limits{MaxNodes: 4, MaxLinks: 7, MaxInspected: 8}
	got, err := memoryquery.Requires(context.Background(), r, family, root, limits)
	if err != nil {
		t.Fatal(err)
	}
	var nodePaths, linkPaths []string
	for _, bead := range got.Nodes {
		nodePaths = append(nodePaths, bead.Path())
	}
	for _, witness := range got.Links {
		linkPaths = append(linkPaths, witness.Link.Path())
		if !witness.Source.Pinned() || !witness.Target.Pinned() || !witness.Source.SameURI(witness.Link.Source()) || !witness.Target.SameURI(witness.Link.Target()) {
			t.Fatalf("witness lost exact context or original Link: %+v", witness)
		}
	}
	if !reflect.DeepEqual(nodePaths, []string{"beads/a", "beads/b", "beads/c", "beads/d"}) || !reflect.DeepEqual(linkPaths, []string{"links/aa", "links/ab", "links/ac", "links/bd", "links/cd", "links/cd-parallel", "links/db"}) {
		t.Fatalf("wrong closure nodes=%v links=%v", nodePaths, linkPaths)
	}
	// Exact caps succeed. Distinct parallel and back edges survive node visitation;
	// an unrelated Subject To edge is neither followed nor treated as Requires.
	for ref := range r.outgoing {
		slices.Reverse(r.outgoing[ref])
	}
	again, err := memoryquery.Requires(context.Background(), r, family, root, limits)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatalf("input permutation changed result: %+v %v", again, err)
	}
}

func TestRequiresLimitsRefusePartialAnswers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits memoryquery.Limits
	}{
		{"nodes", memoryquery.Limits{MaxNodes: 3, MaxLinks: 7, MaxInspected: 8}},
		{"links", memoryquery.Limits{MaxNodes: 4, MaxLinks: 6, MaxInspected: 8}},
		{"inspected-includes-unselected", memoryquery.Limits{MaxNodes: 4, MaxLinks: 7, MaxInspected: 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, root := fixture(t)
			got, err := memoryquery.Requires(context.Background(), r, family, root, tc.limits)
			assertEmpty(t, got, err, memoryquery.ErrLimitExceeded)
		})
	}
	t.Run("isolated-root-with-zero-links", func(t *testing.T) {
		r, root := fixture(t)
		r.outgoing = nil
		got, err := memoryquery.Requires(context.Background(), r, family, root, memoryquery.Limits{MaxNodes: 1})
		if err != nil || len(got.Nodes) != 1 || got.Nodes[0].Revision().String() != "1" || got.Links == nil || len(got.Links) != 0 {
			t.Fatalf("isolated exact-cap closure: %+v %v", got, err)
		}
	})
}

func TestRequiresExactRevisionContext(t *testing.T) {
	t.Run("floating-target-resolves-without-rewriting-Link", func(t *testing.T) {
		r, root := fixture(t)
		floating := reference(t, "b", "")
		r.outgoing = map[graph.Ref][]graph.Link{root: {link(t, "ab", "requires", root, floating)}}
		got, err := memoryquery.Requires(context.Background(), r, family, root, memoryquery.Limits{MaxNodes: 2, MaxLinks: 1, MaxInspected: 1})
		if err != nil || len(got.Links) != 1 || got.Links[0].Link.Target().Pinned() || got.Links[0].Target.Pin() != "1" {
			t.Fatalf("resolution changed authored reference or lost exact result: %+v %v", got, err)
		}
	})
	t.Run("multiple-revisions-remain-distinct", func(t *testing.T) {
		r, root := fixture(t)
		b1, b2 := reference(t, "b", "1"), reference(t, "b", "2")
		r.beads[b2] = node(t, b2)
		r.outgoing = map[graph.Ref][]graph.Link{root: {link(t, "ab1", "requires", root, b1), link(t, "ab2", "requires", root, b2)}}
		got, err := memoryquery.Requires(context.Background(), r, family, root, memoryquery.Limits{MaxNodes: 3, MaxLinks: 2, MaxInspected: 2})
		if err != nil || len(got.Nodes) != 3 || got.Nodes[1].Revision().String() != "1" || got.Nodes[2].Revision().String() != "2" {
			t.Fatalf("revision nodes collapsed: %+v %v", got, err)
		}
	})
	t.Run("retained-Link-keeps-source-revision-context", func(t *testing.T) {
		r, root := fixture(t)
		a2, b := reference(t, "a", "2"), reference(t, "b", "1")
		r.beads[a2] = node(t, a2)
		// A retained Link can be unchanged across selected source revisions.
		// Its source pin remains authored data, not an incidence filter.
		shared := link(t, "ab", "requires", root, b)
		r.outgoing = map[graph.Ref][]graph.Link{root: {shared}, b: {link(t, "ba2", "requires", b, a2)}, a2: {shared}}
		got, err := memoryquery.Requires(context.Background(), r, family, root, memoryquery.Limits{MaxNodes: 3, MaxLinks: 3, MaxInspected: 3})
		if err != nil || len(got.Links) != 3 || got.Links[0].Source.Pin() != "1" || got.Links[1].Source.Pin() != "2" || got.Links[1].Link.Source().Pin() != "1" {
			t.Fatalf("retained source contexts lost: %+v %v", got, err)
		}
	})
}

type incorrectResolver struct{ reads }

func (r incorrectResolver) Resolve(ctx context.Context, ref graph.Ref) (graph.Bead, error) {
	if ref.Path() == "beads/b" {
		ref, _ = graph.NewPublicInScopeRef(ref.Path(), "1")
	}
	return r.reads.Resolve(ctx, ref)
}

func TestRequiresRejectsRevisionFallback(t *testing.T) {
	r, root := fixture(t)
	r.outgoing = map[graph.Ref][]graph.Link{root: {link(t, "ab", "requires", root, reference(t, "b", "2"))}}
	got, err := memoryquery.Requires(context.Background(), incorrectResolver{r}, family, root, memoryquery.Limits{MaxNodes: 2, MaxLinks: 1, MaxInspected: 1})
	assertEmpty(t, got, err, memoryquery.ErrReadContext)
}

func TestRequiresReadFailuresAndCancellation(t *testing.T) {
	boom := errors.New("required read unavailable")
	t.Run("resolve-error", func(t *testing.T) {
		r, root := fixture(t)
		r.readErr = boom
		got, err := memoryquery.Requires(context.Background(), r, family, root, memoryquery.Limits{MaxNodes: 4, MaxLinks: 7, MaxInspected: 8})
		assertEmpty(t, got, err, boom)
	})
	t.Run("family-error", func(t *testing.T) {
		r, root := fixture(t)
		got, err := memoryquery.Requires(context.Background(), r, func(context.Context, string) (bool, error) { return false, boom }, root, memoryquery.Limits{MaxNodes: 4, MaxLinks: 7, MaxInspected: 8})
		assertEmpty(t, got, err, boom)
	})
	t.Run("outgoing-error-after-progress", func(t *testing.T) {
		r, root := fixture(t)
		r.outErrAt = map[graph.Ref]error{reference(t, "b", "1"): boom}
		got, err := memoryquery.Requires(context.Background(), r, family, root, memoryquery.Limits{MaxNodes: 4, MaxLinks: 7, MaxInspected: 8})
		assertEmpty(t, got, err, boom)
	})
	for _, when := range []string{"before", "during-outgoing", "during-family"} {
		t.Run(when, func(t *testing.T) {
			r, root := fixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			classify := family
			switch when {
			case "before":
				cancel()
			case "during-outgoing":
				r.readHook = func(graph.Ref) { cancel() }
			case "during-family":
				classify = func(context.Context, string) (bool, error) { cancel(); return true, nil }
			}
			got, err := memoryquery.Requires(ctx, r, classify, root, memoryquery.Limits{MaxNodes: 4, MaxLinks: 7, MaxInspected: 8})
			assertEmpty(t, got, err, context.Canceled)
		})
	}
}

func TestRequiresInvalidInputsAndReadContext(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*reads, *graph.Ref, *memoryquery.Limits)
		want   error
	}{
		{"floating-root", func(_ *reads, root *graph.Ref, _ *memoryquery.Limits) { *root = reference(t, "a", "") }, graph.ErrValidation},
		{"invalid-nodes", func(_ *reads, _ *graph.Ref, limits *memoryquery.Limits) { limits.MaxNodes = 0 }, graph.ErrValidation},
		{"negative-work", func(_ *reads, _ *graph.Ref, limits *memoryquery.Limits) { limits.MaxInspected = -1 }, graph.ErrValidation},
		{"duplicate-Link", func(r *reads, root *graph.Ref, _ *memoryquery.Limits) {
			r.outgoing[*root] = append(r.outgoing[*root], r.outgoing[*root][0])
		}, memoryquery.ErrReadContext},
		{"wrong-source", func(r *reads, root *graph.Ref, _ *memoryquery.Limits) {
			r.outgoing[*root] = []graph.Link{link(t, "wrong", "requires", reference(t, "b", "1"), *root)}
		}, memoryquery.ErrReadContext},
		{"zero-Link", func(r *reads, root *graph.Ref, _ *memoryquery.Limits) { r.outgoing[*root] = []graph.Link{{}} }, memoryquery.ErrReadContext},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, root := fixture(t)
			limits := memoryquery.Limits{MaxNodes: 10, MaxLinks: 10, MaxInspected: 20}
			tc.mutate(&r, &root, &limits)
			got, err := memoryquery.Requires(context.Background(), r, family, root, limits)
			assertEmpty(t, got, err, tc.want)
		})
	}
}

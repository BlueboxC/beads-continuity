//go:build cgo

package graphstore

import (
	"context"
	"errors"
	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memoryquery"
	"reflect"
	"slices"
	"testing"
)

func currentConstraintsRequest(roots ...CurrentConstraintRoot) CurrentConstraintsRequest {
	return CurrentConstraintsRequest{Roots: roots, Limits: memoryquery.Limits{MaxNodes: 100, MaxLinks: 100, MaxInspected: 10000}, MaxBytes: PreviewCurrentReadByteLimit}
}
func assertConstraintsError(t *testing.T, read CurrentConstraintsRead, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || !reflect.DeepEqual(read, CurrentConstraintsRead{}) {
		t.Fatalf("partial/unexpected %+v %v; want %v", read, err, want)
	}
}
func TestMemoryCurrentConstraintReads(t *testing.T) {
	s := currentFamilyStore(t)
	exerciseCurrentConstraintReads(t, s)
}
func exerciseCurrentConstraintReads(t *testing.T, s *Store) {
	t.Helper()
	// Different declared Types must be admitted through their Constraint
	// affiliation, while canonical records keep those actual Types.
	for i, typ := range []string{"specification", "requirement", "policy", "decision", "procedure"} {
		currentFamilyCreate(t, s, []string{"a", "b", "c", "d", "e"}[i], typ)
	}
	currentFamilyCreate(t, s, "m", "memory")
	if _, err := s.CreateIssue(currentFamilyContext(t), "beads/i", plainIssueAtPublicID("native subject", "beads/i")); err != nil {
		t.Fatal(err)
	}
	for _, e := range [][4]string{{"ab", "subject-to", "a", "b"}, {"ab2", "subject-to", "a", "b"}, {"ac", "subject-to", "a", "c"}, {"ba", "requires", "b", "a"}, {"cb", "requires", "c", "b"}, {"defab", "defines", "a", "b"}, {"ad", "requires", "a", "d"}, {"de", "subject-to", "d", "e"}, {"ia", "subject-to", "i", "a"}, {"mc", "subject-to", "m", "c"}} {
		currentFamilyAdd(t, s, e[0], e[1], e[2], e[3], EndpointPin{})
	}
	before := workflowState(t, currentFamilyContext(t), s)
	req := currentConstraintsRequest(CurrentConstraintRoot{Path: "beads/a", RequiresDepth: 0}, CurrentConstraintRoot{Path: "beads/a", RequiresDepth: 1}, CurrentConstraintRoot{Path: "beads/e", RequiresDepth: 3}, CurrentConstraintRoot{Path: "beads/i", RequiresDepth: 1})
	got, err := s.ReadCurrentConstraints(currentFamilyContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	if got.WriterToken == "" || len(got.Types) != len(FreshTypeNames()) || len(got.OwnedLinkTypes) == 0 || len(got.Graph.Views) != 4 || len(got.Stages) != 4 {
		t.Fatal("lost checked context", got)
	}
	first, second := got.Stages[0], got.Stages[1]
	if !reflect.DeepEqual(first.SubjectTo, []memoryquery.ConstraintSeed{{Link: "links/ab", Target: "beads/b"}, {Link: "links/ab2", Target: "beads/b"}, {Link: "links/ac", Target: "beads/c"}}) || !reflect.DeepEqual(first.Requires.Seeds, []string{"beads/b", "beads/c"}) || !reflect.DeepEqual(first.Requires.Nodes, []string{"beads/b", "beads/c"}) || !reflect.DeepEqual(first.Requires.Links, []string{"links/cb"}) || !reflect.DeepEqual(first.Requires.Frontier, []string{"beads/b"}) {
		t.Fatal(first)
	}
	if slices.Contains(got.Graph.Views[0].Links, "links/ba") || slices.Contains(got.Graph.Views[0].Links, "links/defab") {
		t.Fatal("cross-phase induction", got.Graph.Views[0])
	}
	if !reflect.DeepEqual(second.Requires.Nodes, []string{"beads/a", "beads/b", "beads/c"}) || !reflect.DeepEqual(second.Requires.Links, []string{"links/ba", "links/cb", "links/defab"}) || !reflect.DeepEqual(second.Requires.Frontier, []string{"beads/a"}) {
		t.Fatal(second)
	}
	if !reflect.DeepEqual(got.Graph.Views[2].Nodes, []string{"beads/e"}) || len(got.Stages[2].Requires.Nodes) != 0 {
		t.Fatal("root automatically required itself", got.Stages[2])
	}
	if !reflect.DeepEqual(got.Graph.Views[3].Nodes, []string{"beads/a", "beads/b", "beads/d", "beads/i"}) || slices.Contains(got.Graph.Views[3].Links, "links/ab") || slices.Contains(got.Graph.Views[3].Links, "links/de") {
		t.Fatal("Subject To replayed in closure", got.Graph.Views[3])
	}
	// Canonical actual Type and authored fields remain distinct from family membership.
	wantNativeType, err := NativeIssueTypeURL(s.ScopeURL(), "task")
	if err != nil {
		t.Fatal(err)
	}
	issues := 0
	for _, record := range got.Records {
		switch r := record.(type) {
		case IssueRecord:
			issues++
			assertCurrentOwned(t, r.Owned, []string{s.ScopeURL() + "links/ia"}, nil)
			if r.Attribution.Actor != "family-author" {
				t.Fatal("native attribution lost", r.Attribution)
			}
			if r.Type != wantNativeType || r.Properties.Title != "native subject" {
				t.Fatal(r)
			}
		case Record:
			if r.ID == s.ScopeURL()+"beads/a" {
				assertCurrentOwned(t, r.Owned, []string{s.ScopeURL() + "links/ab", s.ScopeURL() + "links/defab"}, []string{s.ScopeURL() + "links/ia", s.ScopeURL() + "links/ba"})
			}
			if string(r.Metadata) != `{"keep":"bead"}` || r.Attribution.Actor != "family-author" {
				t.Fatal("Memory fields lost", r)
			}
		case LinkRecord:
			if string(r.Metadata) != `{"keep":"link"}` || r.Attribution.Actor != "family-author" || r.Properties["body"] != "evidence" {
				t.Fatal("Link fields lost", r)
			}
			if r.ID == s.ScopeURL()+"links/defab" && r.Type != s.ScopeURL()+"types/defines" {
				t.Fatal("declared Type erased", r)
			}
		}
	}
	if issues != 1 {
		t.Fatal("native subject lost", issues)
	}
	subjects, err := s.ReadCurrentSubjects(currentFamilyContext(t), CurrentSubjectsRequest{Roots: []string{"beads/b", "beads/a"}, Limits: req.Limits, MaxBytes: req.MaxBytes})
	if err != nil || !reflect.DeepEqual(subjects.Graph.Views[0].Links, []string{"links/ab", "links/ab2"}) || !reflect.DeepEqual(subjects.Graph.Views[1].Links, []string{"links/ia"}) || len(subjects.Graph.Views[0].Frontier) != 0 {
		t.Fatal(subjects, err)
	}
	// Requires includes Defines, retaining the valid mixed Requires/Defines cycle.
	for _, family := range []string{"requires", "defines"} {
		familyReq := currentFamilyRequest(s, family, memoryquery.CurrentRoot{Path: "beads/a", Depth: 1})
		read, e := s.ReadCurrentFamily(currentFamilyContext(t), familyReq)
		if e != nil {
			t.Fatal(e)
		}
		want := []string{"links/defab"}
		if family == "requires" {
			want = []string{"links/ad", "links/ba", "links/defab"}
		}
		if !reflect.DeepEqual(currentFamilyIDs(read), want) {
			t.Fatal(family, read.Graph)
		}
		familyReq.Roots[0] = memoryquery.CurrentRoot{Path: "beads/b", Depth: 1}
		familyReq.Direction = graph.DirectionIn
		read, e = s.ReadCurrentFamily(currentFamilyContext(t), familyReq)
		want = []string{"links/defab"}
		if family == "requires" {
			want = []string{"links/ba", "links/cb", "links/defab"}
		}
		if e != nil || !reflect.DeepEqual(currentFamilyIDs(read), want) {
			t.Fatal(family, read.Graph, e)
		}
		for _, path := range []string{"beads/i", "beads/m"} {
			familyReq.Roots[0].Path = path
			read, e = s.ReadCurrentFamily(currentFamilyContext(t), familyReq)
			assertCurrentFamilyError(t, read, e, graph.ErrValidation)
		}
	}
	for _, path := range []string{"beads/i", "beads/m"} {
		read, e := s.ReadCurrentSubjects(currentFamilyContext(t), CurrentSubjectsRequest{Roots: []string{path}, Limits: req.Limits, MaxBytes: req.MaxBytes})
		assertCurrentFamilyError(t, read, e, graph.ErrValidation)
	}
	if !reflect.DeepEqual(before, workflowState(t, currentFamilyContext(t), s)) {
		t.Fatal("read changed persistence")
	}
	t.Run("cumulative_and_errors", func(t *testing.T) {
		for _, which := range []string{"nodes", "links", "rows", "bytes", "cancel", "nil"} {
			t.Run(which, func(t *testing.T) {
				r := req
				ctx := currentFamilyContext(t)
				want := ErrLimitExceeded
				switch which {
				case "nodes":
					r.Limits.MaxNodes = 3
				case "links":
					r.Limits.MaxLinks = 3
				case "rows":
					r.Limits.MaxInspected = 4
				case "bytes":
					r.MaxBytes = 256
				case "cancel":
					c, stop := context.WithCancel(ctx)
					stop()
					ctx = c
					want = context.Canceled
				case "nil":
					ctx = nil
				}
				read, e := s.ReadCurrentConstraints(ctx, r)
				if which == "nil" {
					if e == nil || !reflect.DeepEqual(read, CurrentConstraintsRead{}) {
						t.Fatal(read, e)
					}
				} else {
					assertConstraintsError(t, read, e, want)
				}
			})
		}
		r := req
		r.Roots = []CurrentConstraintRoot{{Path: "beads/a", RequiresDepth: -1}}
		read, e := s.ReadCurrentConstraints(currentFamilyContext(t), r)
		assertConstraintsError(t, read, e, graph.ErrValidation)
		direct, e := s.ReadCurrentSubjects(nil, CurrentSubjectsRequest{})
		if e == nil || !reflect.DeepEqual(direct, CurrentFamilyRead{}) {
			t.Fatal(direct, e)
		}
	})
}
func TestMemoryCurrentConstraintsPinsAndCumulativeRoots(t *testing.T) {
	s := currentFamilyStore(t)
	// Two disconnected roots individually fit; their aggregate must use one bound.
	for _, name := range []string{"s1", "s2"} {
		currentFamilyCreate(t, s, name, "memory")
	}
	for _, name := range []string{"a", "b"} {
		currentFamilyCreate(t, s, name, "constraint")
	}
	currentFamilyAdd(t, s, "s1a", "subject-to", "s1", "a", EndpointPin{})
	currentFamilyAdd(t, s, "s2b", "subject-to", "s2", "b", EndpointPin{})
	req := currentConstraintsRequest(CurrentConstraintRoot{Path: "beads/s1"}, CurrentConstraintRoot{Path: "beads/s2"})
	req.Limits.MaxNodes = 2
	for _, root := range req.Roots {
		r := req
		r.Roots = []CurrentConstraintRoot{root}
		if _, err := s.ReadCurrentConstraints(currentFamilyContext(t), r); err != nil {
			t.Fatal(err)
		}
	}
	read, err := s.ReadCurrentConstraints(currentFamilyContext(t), req)
	assertConstraintsError(t, read, err, ErrLimitExceeded)
	for _, which := range []string{"subject-to", "requires"} {
		t.Run(which, func(t *testing.T) {
			currentFamilyCreate(t, s, "pin-source-"+which, "constraint")
			target := currentFamilyCreate(t, s, "pin-target-"+which, "constraint")
			currentFamilyAdd(t, s, "pin-"+which, which, "pin-source-"+which, "pin-target-"+which, EndpointPin{Revision: target.Revision})
			root := CurrentConstraintRoot{Path: "beads/pin-source-" + which}
			if which == "requires" {
				currentFamilyCreate(t, s, "seed-source", "memory")
				currentFamilyAdd(t, s, "seed", "subject-to", "seed-source", "pin-source-"+which, EndpointPin{})
				root = CurrentConstraintRoot{Path: "beads/seed-source", RequiresDepth: 1}
			}
			req := currentConstraintsRequest(root)
			got, e := s.ReadCurrentConstraints(currentFamilyContext(t), req)
			if e != nil {
				t.Fatal(e)
			}
			pinKept := false
			for _, l := range got.Graph.Links {
				if l.Target().Path() == "beads/pin-target-"+which {
					pinKept = l.Target().Pin() == target.Revision
				}
			}
			if !pinKept {
				t.Fatal("pin lost", got)
			}
			body := "changed"
			if _, e := s.PatchMemory(currentFamilyContext(t), MemoryPatchRequest{Path: "beads/pin-target-" + which, Body: &body, ExpectedRevision: target.Revision}); e != nil {
				t.Fatal(e)
			}
			got, e = s.ReadCurrentConstraints(currentFamilyContext(t), req)
			assertConstraintsError(t, got, e, ErrCapabilityUnavailable)
			if which == "subject-to" {
				direct, e := s.ReadCurrentSubjects(currentFamilyContext(t), CurrentSubjectsRequest{Roots: []string{"beads/pin-target-" + which}, Limits: req.Limits, MaxBytes: req.MaxBytes})
				assertCurrentFamilyError(t, direct, e, ErrCapabilityUnavailable)
			}
		})
	}
}
func TestMemoryCurrentConstraintReadsServer(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "server")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	exerciseCurrentConstraintReads(t, s)
}

func TestMemoryCurrentConstraintReadRootCaps(t *testing.T) {
	// Invalid shape and canceled requests must refuse before needing a Store/DB.
	var s *Store
	roots := make([]CurrentConstraintRoot, PreviewSnapshotLimit+1)
	subjects := make([]string, len(roots))
	got, err := s.ReadCurrentConstraints(context.Background(), CurrentConstraintsRequest{Roots: roots})
	assertConstraintsError(t, got, err, graph.ErrValidation)
	direct, err := s.ReadCurrentSubjects(context.Background(), CurrentSubjectsRequest{Roots: subjects})
	assertCurrentFamilyError(t, direct, err, graph.ErrValidation)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = s.ReadCurrentConstraints(ctx, currentConstraintsRequest(CurrentConstraintRoot{Path: "beads/a"}))
	assertConstraintsError(t, got, err, context.Canceled)
	direct, err = s.ReadCurrentSubjects(ctx, CurrentSubjectsRequest{Roots: []string{"beads/a"}})
	assertCurrentFamilyError(t, direct, err, context.Canceled)
}

func TestMemoryCurrentConstraintsAcquisitionAccumulatesAcrossPhases(t *testing.T) {
	s := currentFamilyStore(t)
	currentFamilyCreate(t, s, "subject", "memory")
	currentFamilyCreate(t, s, "a", "constraint")
	currentFamilyCreate(t, s, "b", "constraint")
	currentFamilyAdd(t, s, "subject-a", "subject-to", "subject", "a", EndpointPin{})
	currentFamilyAdd(t, s, "ab", "requires", "a", "b", EndpointPin{})
	for _, metric := range []string{"bytes", "rows"} {
		t.Run(metric, func(t *testing.T) {
			base := currentConstraintsRequest(CurrentConstraintRoot{Path: "beads/subject", RequiresDepth: 1})
			runners := []func(uint64) error{
				func(cap uint64) error {
					req := CurrentSubjectsRequest{Roots: []string{"beads/a"}, Limits: base.Limits, MaxBytes: base.MaxBytes}
					if metric == "bytes" {
						req.MaxBytes = cap
					} else {
						req.Limits.MaxInspected = int(cap)
					}
					got, err := s.ReadCurrentSubjects(currentFamilyContext(t), req)
					if err != nil && !reflect.DeepEqual(got, CurrentFamilyRead{}) {
						t.Fatal("partial direct budget result", got)
					}
					return err
				},
				func(cap uint64) error {
					req := currentFamilyRequest(s, "requires", memoryquery.CurrentRoot{Path: "beads/a", Depth: 1})
					if metric == "bytes" {
						req.MaxBytes = cap
					} else {
						req.Limits.MaxInspected = int(cap)
					}
					got, err := s.ReadCurrentFamily(currentFamilyContext(t), req)
					if err != nil && !reflect.DeepEqual(got, CurrentFamilyRead{}) {
						t.Fatal("partial family budget result", got)
					}
					return err
				},
				func(cap uint64) error {
					req := base
					if metric == "bytes" {
						req.MaxBytes = cap
					} else {
						req.Limits.MaxInspected = int(cap)
					}
					got, err := s.ReadCurrentConstraints(currentFamilyContext(t), req)
					if err != nil && !reflect.DeepEqual(got, CurrentConstraintsRead{}) {
						t.Fatal("partial staged budget result", got)
					}
					return err
				},
			}
			minimum := func(run func(uint64) error) uint64 {
				lo, hi := uint64(0), uint64(PreviewCurrentReadByteLimit)
				if metric == "rows" {
					hi = 1000000
				}
				if err := run(hi); err != nil {
					t.Fatal(err)
				}
				for hi-lo > 1 {
					mid := lo + (hi-lo)/2
					err := run(mid)
					if err == nil {
						hi = mid
					} else if errors.Is(err, ErrLimitExceeded) {
						lo = mid
					} else {
						t.Fatal(err)
					}
				}
				if err := run(hi); err != nil {
					t.Fatal(err)
				}
				if err := run(hi - 1); !errors.Is(err, ErrLimitExceeded) {
					t.Fatalf("cap-minus-one admitted: %d %v", hi, err)
				}
				return hi
			}
			first, second, total := minimum(runners[0]), minimum(runners[1]), minimum(runners[2])
			cap := max(first, second)
			if total <= cap {
				t.Fatalf("fixture does not distinguish cumulative %s: %d %d %d", metric, first, second, total)
			}
			for _, run := range runners[:2] {
				if err := run(cap); err != nil {
					t.Fatal(err)
				}
			}
			if err := runners[2](cap); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("phase counters did not accumulate %s: %v", metric, err)
			}
			t.Logf("%s exact thresholds direct=%d Requires=%d staged=%d", metric, first, second, total)
		})
	}
}

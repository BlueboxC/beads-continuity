//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memoryquery"
	"github.com/steveyegge/beads/internal/typecatalog"
	"github.com/steveyegge/beads/internal/types"
)

func currentFamilyStore(t *testing.T) *Store {
	t.Helper()
	setup, stop := context.WithTimeout(context.Background(), 3*time.Minute)
	defer stop()
	o := testOptions(t)
	o.IssuePrefix = "fam"
	if err := Init(setup, o); err != nil {
		t.Fatal(err)
	}
	s, err := OpenExisting(setup, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func currentFamilyRequest(s *Store, family string, roots ...memoryquery.CurrentRoot) CurrentFamilyRequest {
	return CurrentFamilyRequest{Roots: roots, Family: typecatalog.Reference{URI: s.ScopeURL() + "types/" + family}, Direction: graph.DirectionOut, Limits: memoryquery.Limits{MaxNodes: 100, MaxLinks: 100, MaxInspected: 10000}, MaxBytes: PreviewCurrentReadByteLimit}
}
func currentFamilyContext(t *testing.T) context.Context {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(stop)
	return ctx
}
func currentFamilyAdd(t *testing.T, s *Store, name, typ, src, dst string, pin EndpointPin) LinkRecord {
	t.Helper()
	r, err := s.AddInformationalLink(currentFamilyContext(t), LinkCreateRequest{Path: "links/" + name, TypeURL: s.ScopeURL() + "types/" + typ, SourcePath: "beads/" + src, TargetPath: "beads/" + dst, TargetPin: pin, UnconditionalSource: true, Properties: map[string]any{"body": "evidence"}, Metadata: json.RawMessage(`{"keep":"link"}`), Actor: "family-author"})
	if err != nil {
		t.Fatal(err)
	}
	return r.Link
}
func currentFamilyCreate(t *testing.T, s *Store, name, typ string) Record {
	t.Helper()
	r, err := s.Create(currentFamilyContext(t), CreateRequest{Path: "beads/" + name, TypeURL: s.ScopeURL() + "types/" + typ, Title: name, Metadata: json.RawMessage(`{"keep":"bead"}`), Actor: "family-author"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func currentFamilyIDs(read CurrentFamilyRead) []string {
	out := []string{}
	for _, l := range read.Graph.Links {
		out = append(out, l.Path())
	}
	return out
}
func assertCurrentFamilyError(t *testing.T, r CurrentFamilyRead, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || !reflect.DeepEqual(r, CurrentFamilyRead{}) {
		t.Fatalf("partial/unexpected %+v %v; want %v", r, err, want)
	}
}

func assertCurrentOwned(t *testing.T, owned []json.RawMessage, include, exclude []string) {
	t.Helper()
	ids := map[string]bool{}
	for _, raw := range owned {
		var l LinkRecord
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatal(err)
		}
		ids[l.ID] = true
	}
	for _, id := range include {
		if !ids[id] {
			t.Fatalf("missing owned Link %s: %v", id, ids)
		}
	}
	for _, id := range exclude {
		if ids[id] {
			t.Fatalf("non-owned incidence became ownership: %s", id)
		}
	}
}

func TestMemoryCurrentFamilyMixedAndRoots(t *testing.T) {
	s := currentFamilyStore(t)
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		currentFamilyCreate(t, s, name, "constraint")
	}
	for _, name := range []string{"i1", "i2"} {
		if _, err := s.CreateIssue(currentFamilyContext(t), "beads/"+name, plainIssueAtPublicID(name, "beads/"+name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range [][3]string{{"ab", "a", "b"}, {"ac", "a", "c"}, {"bd", "b", "d"}, {"cd", "c", "d"}, {"bc", "b", "c"}, {"ba", "b", "a"}, {"parallel", "b", "d"}, {"i1a", "i1", "a"}, {"ai2", "a", "i2"}, {"i2b", "i2", "b"}} {
		currentFamilyAdd(t, s, e[0], "references", e[1], e[2], EndpointPin{})
	}
	currentFamilyAdd(t, s, "defines", "defines", "e", "f", EndpointPin{})
	currentFamilyAdd(t, s, "requires-cycle", "requires", "f", "e", EndpointPin{})
	currentFamilyAdd(t, s, "subject", "subject-to", "i2", "e", EndpointPin{})
	if _, err := s.AddDependency(currentFamilyContext(t), DependencyRequest{Actor: "family-author", Path: "links/native", SourcePath: "beads/i1", TargetPath: "beads/i2", Type: types.DepBlocks}); err != nil {
		t.Fatal(err)
	}
	ctx := currentFamilyContext(t)
	before := workflowState(t, ctx, s)
	req := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/a", Depth: 1}, memoryquery.CurrentRoot{Path: "beads/b", Depth: 1}, memoryquery.CurrentRoot{Path: "beads/a", Depth: 0})
	got, err := s.ReadCurrentFamily(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Graph.Views[0].Links, []string{"links/ab", "links/ac", "links/ai2", "links/ba", "links/bc", "links/i2b"}) {
		t.Fatal(got.Graph.Views[0])
	}
	if slices.Contains(got.Graph.Views[0].Links, "links/cd") || len(got.Graph.Views) != 3 || len(got.Graph.Views[2].Nodes) != 1 {
		t.Fatal(got.Graph)
	}
	if !reflect.DeepEqual(got.Graph.Views[0].Frontier, []string{"beads/b", "beads/c", "beads/i2"}) {
		t.Fatal(got.Graph.Views[0])
	}
	if got.WriterToken == "" || len(got.Types) != len(FreshTypeNames()) || len(got.OwnedLinkTypes) == 0 {
		t.Fatal("lost checked context")
	}
	both := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/c", Depth: 1})
	both.Direction = graph.DirectionBoth
	bothRead, e := s.ReadCurrentFamily(currentFamilyContext(t), both)
	if e != nil || !reflect.DeepEqual(bothRead.Graph.Views[0].Nodes, []string{"beads/a", "beads/b", "beads/c", "beads/d"}) || !reflect.DeepEqual(bothRead.Graph.Views[0].Links, []string{"links/ab", "links/ac", "links/ba", "links/bc", "links/bd", "links/cd", "links/parallel"}) || !reflect.DeepEqual(bothRead.Graph.Views[0].Frontier, []string{"beads/a", "beads/b"}) {
		t.Fatal(bothRead, e)
	}
	req = currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/i1", Depth: 4})
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), req)
	if err != nil {
		t.Fatal(err)
	}
	wantLinks := []string{"links/i1a", "links/ai2", "links/i2b", "links/subject", "links/defines", "links/requires-cycle"}
	for _, id := range wantLinks {
		if !slices.Contains(currentFamilyIDs(got), id) {
			t.Fatalf("missing mixed/refined path %s: %+v", id, got.Graph)
		}
	}
	if slices.Contains(currentFamilyIDs(got), "links/native") {
		t.Fatal("Dependency selected as References")
	}
	wantNativeType, err := NativeIssueTypeURL(s.ScopeURL(), "task")
	if err != nil {
		t.Fatal(err)
	}
	issues := 0
	for _, raw := range got.Records {
		switch r := raw.(type) {
		case IssueRecord:
			if r.Attribution.Actor != "family-author" || r.Attribution.Status != "claimed" || r.Attribution.RecordedAt == "" {
				t.Fatal("lost native attribution", r.Attribution)
			}
			if r.ID == s.ScopeURL()+"beads/i1" {
				assertCurrentOwned(t, r.Owned, []string{s.ScopeURL() + "links/native"}, []string{s.ScopeURL() + "links/i1a"})
			}
			if r.ID == s.ScopeURL()+"beads/i2" {
				assertCurrentOwned(t, r.Owned, []string{s.ScopeURL() + "links/subject"}, []string{s.ScopeURL() + "links/ai2", s.ScopeURL() + "links/i2b"})
			}
			issues++
			if r.Type != wantNativeType || len(r.Properties.Labels) != 2 {
				t.Fatal("lost native Issue shape", r)
			}
		case Record:
			if r.Attribution.Actor != "family-author" || r.Attribution.Status != "claimed" || r.Attribution.RecordedAt == "" {
				t.Fatal("lost Memory attribution", r.Attribution)
			}
			if r.ID == s.ScopeURL()+"beads/e" {
				assertCurrentOwned(t, r.Owned, []string{s.ScopeURL() + "links/defines"}, []string{s.ScopeURL() + "links/subject"})
			}
			if r.ID == s.ScopeURL()+"beads/a" {
				assertCurrentOwned(t, r.Owned, nil, []string{s.ScopeURL() + "links/i1a", s.ScopeURL() + "links/ab"})
			}
			if string(r.Metadata) != `{"keep":"bead"}` {
				t.Fatal(r)
			}
		case LinkRecord:
			if r.Attribution.Actor != "family-author" || r.Attribution.Status != "claimed" || r.Attribution.RecordedAt == "" {
				t.Fatal("lost Link attribution", r.Attribution)
			}
			if r.ID == s.ScopeURL()+"links/defines" && r.Type != s.ScopeURL()+"types/defines" {
				t.Fatal("family erased declared Type")
			}
			if string(r.Metadata) != `{"keep":"link"}` {
				t.Fatal(r)
			}
		}
	}
	if issues != 2 {
		t.Fatal("mixed Issue roots not hydrated", issues)
	}
	req.Direction = graph.DirectionIn
	req.Roots = []memoryquery.CurrentRoot{{Path: "beads/f", Depth: 4}}
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), req)
	if err != nil || !slices.Contains(currentFamilyIDs(got), "links/i1a") {
		t.Fatal(got.Graph, err)
	}
	if !reflect.DeepEqual(before, workflowState(t, currentFamilyContext(t), s)) {
		t.Fatal("read changed persistence")
	}
	t.Run("bounds_and_cancel", func(t *testing.T) {
		for _, which := range []string{"nodes", "links", "rows", "bytes", "canceled", "nil"} {
			t.Run(which, func(t *testing.T) {
				r := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/i1", Depth: 4})
				ctx := currentFamilyContext(t)
				want := ErrLimitExceeded
				switch which {
				case "nodes":
					r.Limits.MaxNodes = 2
				case "links":
					r.Limits.MaxLinks = 1
				case "rows":
					r.Limits.MaxInspected = 2
				case "bytes":
					r.MaxBytes = 256
				case "canceled":
					c, stop := context.WithCancel(ctx)
					stop()
					ctx = c
					want = context.Canceled
				case "nil":
					ctx = nil
				}
				read, e := s.ReadCurrentFamily(ctx, r)
				if which == "nil" {
					if e == nil || !reflect.DeepEqual(read, CurrentFamilyRead{}) {
						t.Fatal(read, e)
					}
				} else {
					assertCurrentFamilyError(t, read, e, want)
				}
			})
		}
	})
	t.Run("native_preflight_before_decode", func(t *testing.T) {
		var key string
		if err := s.db.QueryRowContext(currentFamilyContext(t), `SELECT backing_key FROM graph_preview_catalog WHERE path='beads/i1'`).Scan(&key); err != nil {
			t.Fatal(err)
		}
		oversized := strings.Repeat("{", 128<<10)
		for _, which := range []string{"current_issue", "retained_issue", "incoming_nonmember", "native_link_retained"} {
			t.Run(which, func(t *testing.T) {
				var old []byte
				var readSQL, writeSQL string
				var args []any
				r := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/i1", Depth: 0})
				r.MaxBytes = 64 << 10
				switch which {
				case "current_issue":
					readSQL = `SELECT description FROM issues WHERE id=?`
					writeSQL = `UPDATE issues SET description=? WHERE id=?`
					args = []any{key}
				case "retained_issue":
					readSQL = `SELECT durable_state FROM issue_versions WHERE issue_id=? AND revision=(SELECT current_revision FROM issues WHERE id=?)`
					writeSQL = `UPDATE issue_versions SET durable_state=? WHERE issue_id=? AND revision=(SELECT current_revision FROM issues WHERE id=?)`
					args = []any{key, key}
				case "incoming_nonmember":
					readSQL = `SELECT properties FROM graph_preview_links WHERE path=?`
					writeSQL = `UPDATE graph_preview_links SET properties=? WHERE path=?`
					args = []any{"links/i1a"}
					r = currentFamilyRequest(s, "superseded-by", memoryquery.CurrentRoot{Path: "beads/a", Depth: 0})
					r.Direction = graph.DirectionIn
					r.MaxBytes = 64 << 10
				case "native_link_retained":
					readSQL = `SELECT snapshot FROM graph_preview_versions WHERE path=? AND version=(SELECT revision FROM graph_preview_catalog WHERE path=?)`
					writeSQL = `UPDATE graph_preview_versions SET snapshot=? WHERE path=? AND version=(SELECT revision FROM graph_preview_catalog WHERE path=?)`
					args = []any{"links/native", "links/native"}
				}
				if err := s.db.QueryRowContext(currentFamilyContext(t), readSQL, args...).Scan(&old); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.ExecContext(currentFamilyContext(t), writeSQL, append([]any{oversized}, args...)...); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := s.db.ExecContext(currentFamilyContext(t), writeSQL, append([]any{old}, args...)...); err != nil {
						t.Error(err)
					}
				}()
				read, e := s.ReadCurrentFamily(currentFamilyContext(t), r)
				assertCurrentFamilyError(t, read, e, ErrLimitExceeded)
			})
		}
		t.Run("labels_before_hydration", func(t *testing.T) {
			values := make([]string, 1000)
			args := make([]any, 0, 2000)
			for i := range values {
				values[i] = "(?,?)"
				args = append(args, key, "bounded-"+fmt.Sprint(i)+strings.Repeat("l", 180))
			}
			if _, err := s.db.ExecContext(currentFamilyContext(t), "INSERT INTO labels (issue_id,label) VALUES "+strings.Join(values, ","), args...); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := s.db.ExecContext(currentFamilyContext(t), `DELETE FROM labels WHERE issue_id=? AND label LIKE 'bounded-%'`, key); err != nil {
					t.Error(err)
				}
			}()
			r := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/i1", Depth: 0})
			r.MaxBytes = 64 << 10
			read, e := s.ReadCurrentFamily(currentFamilyContext(t), r)
			assertCurrentFamilyError(t, read, e, ErrLimitExceeded)
		})
	})

	t.Run("inverse_corruption", func(t *testing.T) {
		var old []byte
		if err := s.db.QueryRowContext(currentFamilyContext(t), `SELECT properties FROM graph_preview_links WHERE path='links/i1a'`).Scan(&old); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(currentFamilyContext(t), `UPDATE graph_preview_links SET properties='{"body":"tampered"}' WHERE path='links/i1a'`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := s.db.ExecContext(currentFamilyContext(t), `UPDATE graph_preview_links SET properties=? WHERE path='links/i1a'`, old); err != nil {
				t.Error(err)
			}
		}()
		r := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/a", Depth: 1})
		read, e := s.ReadCurrentFamily(currentFamilyContext(t), r)
		assertCurrentFamilyError(t, read, e, ErrInvalidStore)
	})
	t.Run("native_wisp_refused_before_hydration", func(t *testing.T) {
		var key string
		if err := s.db.QueryRowContext(currentFamilyContext(t), `SELECT backing_key FROM graph_preview_catalog WHERE path='beads/i1'`).Scan(&key); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(currentFamilyContext(t), `INSERT INTO wisps (id,title) VALUES (?, 'alternative authority')`, key); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := s.db.ExecContext(currentFamilyContext(t), `DELETE FROM wisps WHERE id=?`, key); err != nil {
				t.Error(err)
			}
		}()
		read, e := s.ReadCurrentFamily(currentFamilyContext(t), currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/i1", Depth: 0}))
		assertCurrentFamilyError(t, read, e, ErrInvalidStore)
	})

}

func TestMemoryCurrentFamilySuccessionPinsAndScope(t *testing.T) {
	s := currentFamilyStore(t)
	for _, name := range []string{"old", "new", "latest", "branch"} {
		currentFamilyCreate(t, s, name, "memory")
	}
	currentFamilyAdd(t, s, "old-new", "superseded-by", "old", "new", EndpointPin{})
	currentFamilyAdd(t, s, "new-latest", "superseded-by", "new", "latest", EndpointPin{})
	currentFamilyAdd(t, s, "branch-new", "superseded-by", "branch", "new", EndpointPin{})
	r := currentFamilyRequest(s, "superseded-by", memoryquery.CurrentRoot{Path: "beads/old", Depth: 1000})
	got, err := s.ReadCurrentFamily(currentFamilyContext(t), r)
	if err != nil || len(got.Graph.Nodes) != 3 || len(got.Graph.Links) != 2 {
		t.Fatal(got, err)
	}
	r.Roots[0] = memoryquery.CurrentRoot{Path: "beads/new", Depth: 1}
	r.Direction = graph.DirectionIn
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	if err != nil || len(got.Graph.Nodes) != 3 || len(got.Graph.Links) != 2 {
		t.Fatal(got, err)
	}
	if _, err := s.CreateIssue(currentFamilyContext(t), "beads/issue", plainIssueAtPublicID("issue", "beads/issue")); err != nil {
		t.Fatal(err)
	}
	r.Roots[0].Path = "beads/issue"
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	assertCurrentFamilyError(t, got, err, graph.ErrValidation)
	source := currentFamilyCreate(t, s, "source", "memory")
	target := currentFamilyCreate(t, s, "target", "constraint")
	currentFamilyAdd(t, s, "pinned", "references", "source", "target", EndpointPin{Revision: target.Revision})
	r = currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/source", Depth: 1})
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	if err != nil || got.Graph.Links[0].Target().Pin() != target.Revision {
		t.Fatal(got, err)
	}
	body := "new revision"
	if _, err := s.PatchMemory(currentFamilyContext(t), MemoryPatchRequest{Path: "beads/target", Body: &body, ExpectedRevision: target.Revision}); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	assertCurrentFamilyError(t, got, err, ErrCapabilityUnavailable)
	r.Roots[0].Depth = 0
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	assertCurrentFamilyError(t, got, err, ErrCapabilityUnavailable)
	source2 := currentFamilyCreate(t, s, "source2", "memory")
	currentFamilyCreate(t, s, "target2", "memory")
	if _, err := s.AddInformationalLink(currentFamilyContext(t), LinkCreateRequest{Path: "links/source-pin", SourcePath: "beads/source2", TargetPath: "beads/target2", SourcePin: EndpointPin{Revision: source2.Revision}}); err != nil {
		t.Fatal(err)
	}
	sourceReq := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/source2", Depth: 1})
	sourceRead, e := s.ReadCurrentFamily(currentFamilyContext(t), sourceReq)
	if e != nil || sourceRead.Graph.Links[0].Source().Pin() != source2.Revision {
		t.Fatal(sourceRead, e)
	}
	if _, err := s.PatchMemory(currentFamilyContext(t), MemoryPatchRequest{Path: "beads/source2", Body: &body, ExpectedRevision: source2.Revision}); err != nil {
		t.Fatal(err)
	}
	sourceRead, e = s.ReadCurrentFamily(currentFamilyContext(t), sourceReq)
	assertCurrentFamilyError(t, sourceRead, e, ErrCapabilityUnavailable)
	retired := currentFamilyCreate(t, s, "retired", "memory")
	if _, err := s.DeleteMemory(currentFamilyContext(t), MemoryDeleteRequest{Path: "beads/retired", ExpectedRevision: retired.Revision, Actor: "family-author"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want error
	}{{"beads/missing", ErrNotFound}, {"beads/retired", ErrGone}} {
		read, e := s.ReadCurrentFamily(currentFamilyContext(t), currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: tc.path, Depth: 1}))
		assertCurrentFamilyError(t, read, e, tc.want)
	}
	for _, which := range []string{"zero_bytes", "over_bytes", "no_roots", "revisioned_family", "nodes_cap", "links_cap", "work_cap", "direction"} {
		invalid := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/source2", Depth: 1})
		switch which {
		case "zero_bytes":
			invalid.MaxBytes = 0
		case "over_bytes":
			invalid.MaxBytes = PreviewCurrentReadByteLimit + 1
		case "no_roots":
			invalid.Roots = nil
		case "revisioned_family":
			invalid.Family.Revision = "revision"
		case "nodes_cap":
			invalid.Limits.MaxNodes = PreviewSnapshotLimit + 1
		case "links_cap":
			invalid.Limits.MaxLinks = PreviewSnapshotLimit + 1
		case "work_cap":
			invalid.Limits.MaxInspected = 1000001
		case "direction":
			invalid.Direction = 255
		}
		read, e := s.ReadCurrentFamily(currentFamilyContext(t), invalid)
		assertCurrentFamilyError(t, read, e, graph.ErrValidation)
	}

	// Unsupported pins are not silently stripped from a root spelling either.
	r.Roots[0].Path = source.ID + "@" + source.Revision
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	assertCurrentFamilyError(t, got, err, graph.ErrValidation)
	r.Roots[0].Path = "beads/source"
	r.Family = typecatalog.Reference{URI: s.ScopeURL() + "types/requires"}
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	// Requires is admitted by the Constraint successor, but this Memory root
	// does not conform to Constraint and must still return zero state.
	assertCurrentFamilyError(t, got, err, graph.ErrValidation)
}

func TestMemoryCurrentFamilyScopedBytesAndIntegrity(t *testing.T) {
	s := currentFamilyStore(t)
	currentFamilyCreate(t, s, "root", "memory")
	currentFamilyCreate(t, s, "second", "memory")
	if _, err := s.CreateIssue(currentFamilyContext(t), "beads/native", plainIssueAtPublicID("scoped native", "beads/native")); err != nil {
		t.Fatal(err)
	}
	seedUnreadableMemory(t, currentFamilyContext(t), s, "beads/large", strings.Repeat("z", PreviewCurrentReadByteLimit/2+1024))
	if _, err := s.CurrentSnapshot(currentFamilyContext(t)); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("fixture not oversized: %v", err)
	}
	native, err := s.ReadCurrentFamily(currentFamilyContext(t), currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/native", Depth: 1}))
	if err != nil || len(native.Graph.Nodes) != 1 || len(native.Records) != 1 {
		t.Fatal(native, err)
	}
	if _, ok := native.Records[0].(IssueRecord); !ok {
		t.Fatal("native root lost canonical backing")
	}
	r := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/root", Depth: 10})
	got, err := s.ReadCurrentFamily(currentFamilyContext(t), r)
	if err != nil || len(got.Graph.Nodes) != 1 {
		t.Fatal(got, err)
	}
	// Find the observable byte admission threshold, then prove exact admission,
	// cap-minus-one refusal, and accumulation across two independent roots.
	low, high := uint64(1), uint64(PreviewCurrentReadByteLimit)
	for low < high {
		mid := low + (high-low)/2
		r.MaxBytes = mid
		read, e := s.ReadCurrentFamily(currentFamilyContext(t), r)
		if e == nil {
			high = mid
		} else {
			assertCurrentFamilyError(t, read, e, ErrLimitExceeded)
			low = mid + 1
		}
	}
	r.MaxBytes = low
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	if err != nil || len(got.Graph.Nodes) != 1 {
		t.Fatal(got, err)
	}
	r.MaxBytes = low - 1
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	assertCurrentFamilyError(t, got, err, ErrLimitExceeded)
	r.MaxBytes = low
	r.Roots = append(r.Roots, memoryquery.CurrentRoot{Path: "beads/second", Depth: 10})
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	assertCurrentFamilyError(t, got, err, ErrLimitExceeded)
	r.MaxBytes = PreviewCurrentReadByteLimit
	r.Roots = r.Roots[:1]

	if _, err := s.db.ExecContext(currentFamilyContext(t), `UPDATE graph_preview_payloads SET properties='{"title":"tampered","body":""}' WHERE path='beads/root'`); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadCurrentFamily(currentFamilyContext(t), r)
	assertCurrentFamilyError(t, got, err, ErrInvalidStore)
}

// Optional real provider seam: run explicitly with a test-owned released Dolt
// server. The ordinary embedded suite does not stand in for this driver path.
func TestMemoryCurrentFamilyServer(t *testing.T) {
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
	for _, name := range []string{"i1", "i2"} {
		if _, err := s.CreateIssue(ctx, "beads/"+name, plainIssueAtPublicID(name, "beads/"+name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"m1", "m2"} {
		currentFamilyCreate(t, s, name, "memory")
	}
	for _, e := range [][3]string{{"one", "i1", "m1"}, {"two", "m1", "i2"}, {"three", "i2", "m2"}} {
		currentFamilyAdd(t, s, e[0], "references", e[1], e[2], EndpointPin{})
	}
	req := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "beads/i1", Depth: 3}, memoryquery.CurrentRoot{Path: "beads/m1", Depth: 0})
	got, err := s.ReadCurrentFamily(ctx, req)
	if err != nil || len(got.Graph.Nodes) != 4 || len(got.Graph.Links) != 3 || len(got.Graph.Views[0].Nodes) != 4 || len(got.Graph.Views[1].Nodes) != 1 || len(got.Graph.Views[1].Frontier) != 1 {
		t.Fatal(got, err)
	}
	req.Direction = graph.DirectionIn
	req.Roots = []memoryquery.CurrentRoot{{Path: "beads/m2", Depth: 3}}
	got, err = s.ReadCurrentFamily(ctx, req)
	if err != nil || len(got.Graph.Nodes) != 4 || len(got.Graph.Links) != 3 {
		t.Fatal(got, err)
	}
}

func TestMemoryCurrentFamilyCommonPublicIdentity(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			request := plainIssue("Native root")
			request.Issue.ID = "exp-root"
			issue, err := s.CreateIssue(ctx, "beads/private-root", request)
			if err != nil {
				t.Fatal(err)
			}
			memory, err := s.Create(ctx, CreateRequest{PublicID: "links/context", Title: "Memory"})
			if err != nil {
				t.Fatal(err)
			}
			link, err := s.AddInformationalLink(ctx, LinkCreateRequest{PublicID: "beads/reference", TypeURL: s.ScopeURL() + "types/references", SourcePath: issue.ID, TargetPath: memory.ID, UnconditionalSource: true})
			if err != nil {
				t.Fatal(err)
			}
			req := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: request.Issue.ID, Depth: 1})
			read, err := s.ReadCurrentFamily(ctx, req)
			if err != nil || len(read.Graph.Nodes) != 2 || !reflect.DeepEqual(currentFamilyIDs(read), []string{"beads/reference"}) {
				t.Fatalf("public identity graph: %+v %v", read.Graph, err)
			}
			nodes := map[string]bool{}
			for _, node := range read.Graph.Nodes {
				nodes[node.Path] = true
			}
			if !nodes["exp-root"] || !nodes["links/context"] {
				t.Fatalf("private locator leaked: %+v", nodes)
			}
			retained, err := s.ReadVersion(ctx, link.Link.ID, link.Link.Revision)
			if err != nil {
				t.Fatal(err)
			}
			exact := retained.(LinkRecord)
			if exact.ID != link.Link.ID || exact.Source != issue.ID || exact.Target != memory.ID {
				t.Fatal("retained endpoint identity changed", exact)
			}
		})
	}
}

// A valid wrong-Kind caller root is an input refusal, while a wrong-Kind node
// reached through stored graph incidence still indicates an invalid capture.
func TestMemoryCurrentFamilyRootKindTaxonomy(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			a, err := s.Create(ctx, CreateRequest{PublicID: "a", Title: "A"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Create(ctx, CreateRequest{PublicID: "b", Title: "B"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.AddInformationalLink(ctx, LinkCreateRequest{PublicID: "ab", TypeURL: s.ScopeURL() + "types/references", SourcePath: a.ID, TargetPath: b.ID, UnconditionalSource: true})
			if err != nil {
				t.Fatal(err)
			}
			req := currentFamilyRequest(s, "references", memoryquery.CurrentRoot{Path: "ab", Depth: 1})
			got, err := s.ReadCurrentFamily(ctx, req)
			assertCurrentFamilyError(t, got, err, graph.ErrValidation)
			if errors.Is(err, ErrInvalidStore) {
				t.Fatalf("caller selection reported corrupt store: %v", err)
			}
			req.Roots[0].Path = "absent"
			got, err = s.ReadCurrentFamily(ctx, req)
			assertCurrentFamilyError(t, got, err, ErrNotFound)
			req.Roots[0].Path = "a"
			got, err = s.ReadCurrentFamily(ctx, req)
			if err != nil || len(got.Graph.Nodes) != 2 || len(got.Graph.Links) != 1 {
				t.Fatal(got, err)
			}
			err = s.withTx(ctx, false, func(tx *sql.Tx) error {
				reader, err := s.newCurrentFamilyReader(ctx, tx, req)
				if err != nil {
					return err
				}
				_, err = reader.Current(ctx, "ab")
				return err
			})
			if !errors.Is(err, ErrInvalidStore) {
				t.Fatalf("internal wrong-Kind node: %v", err)
			}
			identity, err := s.ResolveAllocatedResourceIdentity(ctx, "ab")
			if err != nil {
				t.Fatal(err)
			}
			req.Roots[0].Path = "ab"
			var revision string
			if err := s.db.QueryRowContext(ctx, "SELECT revision FROM graph_preview_catalog WHERE path=?", identity.PrivatePath).Scan(&revision); err != nil {
				t.Fatal(err)
			}
			for _, broken := range []struct{ column, invalid, valid string }{
				{"resource_kind", "bead", "link"},
				{"allocation_state", "broken", "live"},
				{"revision", "not a token", revision},
			} {
				if _, err := s.db.ExecContext(ctx, "UPDATE graph_preview_catalog SET "+broken.column+"=? WHERE path=?", broken.invalid, identity.PrivatePath); err != nil {
					t.Fatal(err)
				}
				got, err = s.ReadCurrentFamily(ctx, req)
				assertCurrentFamilyError(t, got, err, ErrInvalidStore)
				if errors.Is(err, graph.ErrValidation) {
					t.Fatalf("corrupt %s hidden as caller refusal: %v", broken.column, err)
				}
				if _, err := s.db.ExecContext(ctx, "UPDATE graph_preview_catalog SET "+broken.column+"=? WHERE path=?", broken.valid, identity.PrivatePath); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: identity.PrivatePath, Actor: "taxonomy", Unconditional: true, UnconditionalSource: true}); err != nil {
				t.Fatal(err)
			}
			got, err = s.ReadCurrentFamily(ctx, req)
			assertCurrentFamilyError(t, got, err, ErrGone)
		})
	}
}

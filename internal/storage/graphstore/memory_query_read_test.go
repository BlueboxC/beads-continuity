//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memoryquery"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func memoryQueryRequest(path string) MemoryRequiresRequest {
	ref, _ := graph.NewPublicInScopeRef(path, "")
	return MemoryRequiresRequest{Root: ref, Limits: memoryquery.Limits{MaxNodes: 20, MaxLinks: 20, MaxInspected: 100}, MaxBytes: PreviewCurrentReadByteLimit}
}

func TestMemoryScopedRequires(t *testing.T) {
	memoryScopedRequires(t, "embedded")
}

func TestMemoryScopedRequiresServer(t *testing.T) {
	memoryScopedRequires(t, "server")
}

func memoryScopedRequires(t *testing.T, backend string) {
	t.Helper()
	ctx, o := issueExperimentOptions(t, backend)
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, path := range []string{"a", "b", "c", "d"} {
		if _, err := s.Create(ctx, CreateRequest{Path: "beads/" + path, TypeURL: s.ScopeURL() + "types/constraint", Title: path}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(path, typ, from, to string) {
		t.Helper()
		if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/" + path, TypeURL: s.ScopeURL() + "types/" + typ, SourcePath: "beads/" + from, TargetPath: "beads/" + to, UnconditionalSource: true, Properties: map[string]any{"body": "Witness"}, Metadata: json.RawMessage(`{"test":"keep"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	add("defines", "defines", "a", "b")
	add("ac", "requires", "a", "c")
	add("bd", "requires", "b", "d")
	add("cd", "requires", "c", "d")
	add("parallel", "requires", "c", "d")
	add("cycle", "requires", "d", "a")
	before := workflowState(t, ctx, s)
	result, err := s.ReadMemoryRequires(ctx, memoryQueryRequest("beads/a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Beads) != 4 || len(result.Links) != 6 || len(result.Graph.Links) != 6 || !result.Root.Pinned() {
		t.Fatalf("%+v", result)
	}
	for i, l := range result.Links {
		if l.ID != s.ScopeURL()+result.Graph.Links[i].Link.Path() || string(l.Metadata) != `{"test":"keep"}` {
			t.Fatalf("lost canonical witness %+v", l)
		}
	}
	if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
		t.Fatal("read changed persistence")
	}
	t.Run("bounds", func(t *testing.T) {
		for _, which := range []string{"nodes", "links", "inspected", "bytes"} {
			t.Run(which, func(t *testing.T) {
				r := memoryQueryRequest("beads/a")
				switch which {
				case "nodes":
					r.Limits.MaxNodes = 2
				case "links":
					r.Limits.MaxLinks = 1
				case "inspected":
					r.Limits.MaxInspected = 1
				case "bytes":
					r.MaxBytes = 16
				}
				got, e := s.ReadMemoryRequires(ctx, r)
				if !errors.Is(e, ErrLimitExceeded) || !reflect.DeepEqual(got, MemoryRequiresRead{}) {
					t.Fatalf("partial %s result %+v %v", which, got, e)
				}
			})
		}
	})
	t.Run("canceled", func(t *testing.T) {
		c, stop := context.WithCancel(ctx)
		stop()
		got, e := s.ReadMemoryRequires(c, memoryQueryRequest("beads/a"))
		if !errors.Is(e, context.Canceled) || !reflect.DeepEqual(got, MemoryRequiresRead{}) {
			t.Fatal(got, e)
		}
	})
	t.Run("missing_pin", func(t *testing.T) {
		r := memoryQueryRequest("beads/a")
		r.Root, _ = graph.NewPublicInScopeRef("beads/a", "missing")
		got, e := s.ReadMemoryRequires(ctx, r)
		if !errors.Is(e, ErrVersionUnknown) || !reflect.DeepEqual(got, MemoryRequiresRead{}) {
			t.Fatal(got, e)
		}
	})
}

func TestMemoryScopedRetainedOwner(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	old, err := s.Create(ctx, CreateRequest{Path: "beads/a", TypeURL: s.ScopeURL() + "types/constraint", Title: "a"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.Create(ctx, CreateRequest{Path: "beads/b", TypeURL: s.ScopeURL() + "types/policy", Title: "b", Body: "old policy"})
	if err != nil {
		t.Fatal(err)
	}
	// Link targets old b. Its source snapshot is retained with the actual pin.
	linked, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/old", TypeURL: s.ScopeURL() + "types/requires", SourcePath: "beads/a", TargetPath: "beads/b", TargetPin: EndpointPin{Revision: target.Revision}, UnconditionalSource: true})
	if err != nil {
		t.Fatal(err)
	}
	body := "new policy"
	if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/b", Body: &body, ExpectedRevision: target.Revision}); err != nil {
		t.Fatal(err)
	}
	// A newer Link must not be joined onto the historical a revision.
	if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/new", TypeURL: s.ScopeURL() + "types/requires", SourcePath: "beads/a", TargetPath: "beads/b", UnconditionalSource: true}); err != nil {
		t.Fatal(err)
	}
	r := memoryQueryRequest("beads/a")
	r.Root, _ = graph.NewPublicInScopeRef("beads/a", linked.Source.(Record).Revision)
	got, err := s.ReadMemoryRequires(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Links) != 1 || len(got.Beads) != 2 || got.Links[0].TargetRevision != target.Revision {
		t.Fatalf("joined current context %+v", got)
	}
	for _, b := range got.Beads {
		if b.ID == target.ID && b.Properties.Body != "old policy" {
			t.Fatal("pin fell back")
		}
	}
	// Exact pre-Link root has an empty closure, despite current outgoing Links.
	r.Root, _ = graph.NewPublicInScopeRef("beads/a", old.Revision)
	got, err = s.ReadMemoryRequires(ctx, r)
	if err != nil || len(got.Beads) != 1 || len(got.Links) != 0 {
		t.Fatal(got, err)
	}
}

func TestMemoryScopedCurrentIntegrityAndUnrelatedBytes(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.Create(ctx, CreateRequest{Path: "beads/rule", TypeURL: s.ScopeURL() + "types/constraint", Title: "Rule"}); err != nil {
		t.Fatal(err)
	}
	seedUnreadableMemory(t, ctx, s, "beads/unrelated", strings.Repeat("x", PreviewCurrentReadByteLimit/2+1024))
	if _, err := s.CurrentSnapshot(ctx); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("fixture not globally oversized: %v", err)
	}
	got, err := s.ReadMemoryRequires(ctx, memoryQueryRequest("beads/rule"))
	if err != nil || len(got.Beads) != 1 {
		t.Fatal(got, err)
	}
	// Current authority must still match its retained state; scoped reads don't
	// substitute a valid retained copy for a corrupted current payload.
	if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_payloads SET properties='{"title":"tampered","body":""}' WHERE path='beads/rule'`); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadMemoryRequires(ctx, memoryQueryRequest("beads/rule"))
	if !errors.Is(err, ErrInvalidStore) || !reflect.DeepEqual(got, MemoryRequiresRead{}) {
		t.Fatal(got, err)
	}
}

func TestMemoryCanonicalMixedIssuePath(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, p := range []string{"i1", "i2"} {
		if _, err := s.CreateIssue(ctx, "beads/"+p, plainIssueAtPublicID(p, "beads/"+p)); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"m1", "m2"} {
		if _, err := s.Create(ctx, CreateRequest{Path: "beads/" + p, Title: p}); err != nil {
			t.Fatal(err)
		}
	}
	for i, p := range [][2]string{{"i1", "m1"}, {"m1", "i2"}, {"i2", "m2"}} {
		path := []string{"one", "two", "three"}[i]
		if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/" + path, SourcePath: "beads/" + p[0], TargetPath: "beads/" + p[1]}); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := s.CurrentSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next := map[string]string{}
	kinds := map[string]string{}
	for _, v := range snap.Records {
		switch r := v.(type) {
		case IssueRecord:
			kinds[r.ID] = "issue"
		case Record:
			kinds[r.ID] = "memory"
		case LinkRecord:
			next[r.Source] = r.Target
		}
	}
	path := []string{}
	for id := s.ScopeURL() + "beads/i1"; id != ""; id = next[id] {
		path = append(path, kinds[id])
		if len(path) > 4 {
			t.Fatal("unexpected cycle")
		}
	}
	if !reflect.DeepEqual(path, []string{"issue", "memory", "issue", "memory"}) {
		t.Fatal(path)
	}
	// The generic graph supports this path. The narrower Requires operation
	// correctly refuses Issue roots rather than weakening Constraint semantics.
	got, e := s.ReadMemoryRequires(ctx, memoryQueryRequest("beads/i1"))
	if !errors.Is(e, ErrCapabilityUnavailable) || !reflect.DeepEqual(got, MemoryRequiresRead{}) {
		t.Fatal(got, e)
	}
}

func TestMemoryScopedIgnoresUnrelatedIssuePopulation(t *testing.T) {
	// Setup exercises real legacy batch authoring, independently of the read.
	// Slow CI setup must not exhaust the scoped-reader budget before it runs.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	setupStarted := time.Now()
	o := testOptions(t)
	o.IssuePrefix = "exp"
	if err := Init(ctx, o); err != nil {
		t.Fatal(err)
	}
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := s.Create(ctx, CreateRequest{Path: "beads/rule", TypeURL: s.ScopeURL() + "types/constraint", Title: "Rule"}); err != nil {
		t.Fatal(err)
	}
	filler, err := s.Create(ctx, CreateRequest{Path: "beads/filler", Title: "Legacy filler"})
	if err != nil {
		t.Fatal(err)
	}
	items := make([]publicops.BatchCreateItem, PreviewSnapshotLimit-2)
	for i := range items {
		items[i] = publicops.BatchCreateItem{Issue: &publicops.Issue{ID: fmt.Sprintf("exp-noise%d", i), Title: "Unrelated", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2}}
	}
	t.Logf("creating %d valid native Issues through existing batch authoring", len(items))
	if _, err := s.CreateIssueBatch(ctx, publicops.CreateBatchRequest{Actor: "memory-scoped-test", Items: items}); err != nil {
		t.Fatal(err)
	}
	// Ordinary authoring stays within capacity. Model a separate legacy oversized
	// body only after setup, using the existing raw fixture writer.
	replaceLegacyFixtureMemory(t, ctx, s, "beads/filler", filler, strings.Repeat("x", PreviewCurrentReadByteLimit/2+1024))
	t.Logf("native Issue population setup finished in %s", time.Since(setupStarted))
	controlCtx, controlCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer controlCancel()
	if _, err := s.CurrentSnapshot(controlCtx); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("global snapshot refusal control failed (budget=%v): %v", controlCtx.Err(), err)
	}
	before, err := s.Show(controlCtx, "beads/rule")
	if err != nil {
		t.Fatalf("canonical selected-root control: %v", err)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer readCancel()
	readStarted := time.Now()
	got, err := s.ReadMemoryRequires(readCtx, memoryQueryRequest("beads/rule"))
	if err != nil || len(got.Beads) != 1 || !reflect.DeepEqual(got.Beads[0], before) {
		t.Fatalf("scoped read after completed population setup (budget=%v): %+v %v", readCtx.Err(), got, err)
	}
	t.Logf("scoped read finished in %s", time.Since(readStarted))
}

func TestMemoryScopedAccumulatesSourceLocalLinks(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, p := range []string{"a", "b"} {
		if _, err := s.Create(ctx, CreateRequest{Path: "beads/" + p, TypeURL: s.ScopeURL() + "types/constraint", Title: p}); err != nil {
			t.Fatal(err)
		}
	}
	for i, edge := range []struct{ typ, from, to string }{{"requires", "a", "b"}, {"references", "a", "b"}, {"references", "b", "a"}} {
		if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: fmt.Sprintf("links/cost%d", i), TypeURL: s.ScopeURL() + "types/" + edge.typ, SourcePath: "beads/" + edge.from, TargetPath: "beads/" + edge.to, UnconditionalSource: true}); err != nil {
			t.Fatal(err)
		}
	}
	request := memoryQueryRequest("beads/a")
	request.Limits.MaxInspected = 2
	got, err := s.ReadMemoryRequires(ctx, request)
	if !errors.Is(err, memoryquery.ErrLimitExceeded) || !reflect.DeepEqual(got, MemoryRequiresRead{}) {
		t.Fatalf("ignored cumulative nonmember acquisition: %+v %v", got, err)
	}
	request.Limits.MaxInspected = 3
	got, err = s.ReadMemoryRequires(ctx, request)
	if err != nil || len(got.Beads) != 2 || len(got.Links) != 1 {
		t.Fatal(got, err)
	}
}

func TestMemoryScopedNilContext(t *testing.T) {
	var s Store
	got, err := s.ReadMemoryRequires(nil, memoryQueryRequest("beads/a"))
	if err == nil || !reflect.DeepEqual(got, MemoryRequiresRead{}) {
		t.Fatal(got, err)
	}
}

// Corrupt persisted catalog values must be refused by SQL-side acquisition
// accounting before existing helpers hydrate their unchecked string fields.
func TestMemoryScopedCatalogAcquisition(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, field := range []string{"endpoint_type", "endpoint_public_id", "retained_backing_key"} {
		if !t.Run(field, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			t.Cleanup(cancel)
			rootPath := "beads/" + field
			targetPath := rootPath + "-target"
			root, err := s.Create(ctx, CreateRequest{Path: rootPath, TypeURL: s.ScopeURL() + "types/constraint", Title: "a"})
			if err != nil {
				t.Fatal(err)
			}
			request := memoryQueryRequest(rootPath)
			request.MaxBytes = 32 * 1024
			switch field {
			case "endpoint_type", "endpoint_public_id":
				if _, err := s.Create(ctx, CreateRequest{Path: targetPath, TypeURL: s.ScopeURL() + "types/constraint", Title: "b"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/" + field, TypeURL: s.ScopeURL() + "types/requires", SourcePath: rootPath, TargetPath: targetPath, UnconditionalSource: true}); err != nil {
					t.Fatal(err)
				}
			case "retained_backing_key":
				body := "new body"
				if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: rootPath, Body: &body, ExpectedRevision: root.Revision}); err != nil {
					t.Fatal(err)
				}
				request.Root, err = graph.NewPublicInScopeRef(rootPath, root.Revision)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.ReadMemoryRequires(ctx, request); err != nil {
				t.Fatalf("valid control: %v", err)
			}
			var mutation sql.Result
			switch field {
			case "endpoint_type":
				var originalType string
				if err := s.db.QueryRowContext(ctx, `SELECT type_url FROM graph_preview_catalog WHERE path=?`, targetPath).Scan(&originalType); err != nil {
					t.Fatal(err)
				}
				// Restore this row before the next case authors its valid fixture.
				t.Cleanup(func() {
					restoreCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
					defer stop()
					result, err := s.db.ExecContext(restoreCtx, `UPDATE graph_preview_catalog SET type_url=? WHERE path=?`, originalType, targetPath)
					if err != nil {
						t.Error(err)
						return
					}
					if count, err := result.RowsAffected(); err != nil || count != 1 {
						t.Errorf("restore catalog row: count=%d err=%v", count, err)
					}
				})
				mutation, err = s.db.ExecContext(ctx, `UPDATE graph_preview_catalog SET type_url=? WHERE path=?`, strings.Repeat("x", 64*1024), targetPath)
			case "endpoint_public_id":
				mutation, err = s.db.ExecContext(ctx, `UPDATE graph_preview_catalog SET public_id=? WHERE path=?`, strings.Repeat("x", 64*1024), targetPath)
			case "retained_backing_key":
				mutation, err = s.db.ExecContext(ctx, `UPDATE graph_preview_catalog SET backing_key=? WHERE path=?`, "invalid-domain-key", rootPath)
			}
			if err != nil {
				t.Fatal(err)
			}
			if count, err := mutation.RowsAffected(); err != nil || count != 1 {
				t.Fatalf("corrupt catalog row: count=%d err=%v", count, err)
			}
			result, err := s.ReadMemoryRequires(ctx, request)
			expected := ErrLimitExceeded
			if field == "retained_backing_key" {
				expected = ErrCapabilityUnavailable
			}
			if !errors.Is(err, expected) || !reflect.DeepEqual(result, MemoryRequiresRead{}) {
				t.Fatalf("catalog acquisition escaped budget: %+v %v", result, err)
			}
		}) {
			break // Preserve the first failure; do not reuse an indeterminate fixture.
		}
	}
}

func TestMemoryScopedTypeBindings(t *testing.T) {
	catalog, err := newSelectedTypeCatalog("https://memory.example/scope/")
	if err != nil {
		t.Fatal(err)
	}
	constraint, requires, err := memoryQueryTypeRefs(catalog)
	if err != nil {
		t.Fatal(err)
	}
	family := []string{}
	for _, link := range catalog.types.Public(graph.KindLink) {
		if catalog.types.Conforms(link.Identity, requires) {
			family = append(family, link.Identity.URI)
			owned, err := catalog.types.Owns(constraint, link.Identity)
			if err != nil || !owned {
				t.Fatalf("unowned Requires family: %v %v", link.Identity, err)
			}
		}
	}
	if !reflect.DeepEqual(family, []string{"https://memory.example/scope/types/defines", "https://memory.example/scope/types/requires"}) {
		t.Fatal(family)
	}
	drifted := *catalog
	drifted.projection, err = newSelectedMemoryProjection("https://other.example/scope/")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := memoryQueryTypeRefs(&drifted); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("binding drift silently accepted: %v", err)
	}
}

func TestMemoryScopedRefusals(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	// Each case owns separate identities. Keep the retained-evidence corruption
	// last: no public write should run against that intentionally damaged store.
	for _, name := range []string{"missing-root", "plain-memory-root", "deleted-root", "deleted-target", "unknown-target-pin"} {
		if !t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			t.Cleanup(cancel)
			rootPath := "beads/" + name + "-root"
			targetPath := "beads/" + name + "-target"
			linkPath := "links/" + name
			root, err := s.Create(ctx, CreateRequest{Path: rootPath, TypeURL: s.ScopeURL() + "types/constraint", Title: "a"})
			if err != nil {
				t.Fatal(err)
			}
			req := memoryQueryRequest(rootPath)
			expected := ErrNotFound
			switch name {
			case "missing-root":
				req = memoryQueryRequest("beads/missing")
			case "plain-memory-root":
				if _, err := s.Create(ctx, CreateRequest{Path: rootPath + "-plain", Title: "plain"}); err != nil {
					t.Fatal(err)
				}
				req = memoryQueryRequest(rootPath + "-plain")
				expected = ErrCapabilityUnavailable
			case "deleted-root":
				if _, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: rootPath, ExpectedRevision: root.Revision}); err != nil {
					t.Fatal(err)
				}
				expected = ErrGone
			case "deleted-target", "unknown-target-pin":
				target, err := s.Create(ctx, CreateRequest{Path: targetPath, TypeURL: s.ScopeURL() + "types/constraint", Title: "b"})
				if err != nil {
					t.Fatal(err)
				}
				linked, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: linkPath, TypeURL: s.ScopeURL() + "types/requires", SourcePath: rootPath, TargetPath: targetPath, TargetPin: EndpointPin{Revision: target.Revision}, UnconditionalSource: true})
				if err != nil {
					t.Fatal(err)
				}
				if name == "deleted-target" {
					owner := linked.Source.(Record)
					req.Root, err = graph.NewPublicInScopeRef(rootPath, owner.Revision)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: linkPath, Unconditional: true, UnconditionalSource: true}); err != nil {
						t.Fatal(err)
					}
					if _, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: targetPath, ExpectedRevision: target.Revision}); err != nil {
						t.Fatal(err)
					}
					expected = ErrGone
				} else {
					body := "new target head"
					if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: targetPath, Body: &body, ExpectedRevision: target.Revision}); err != nil {
						t.Fatal(err)
					}
					// Simulate unavailable retained evidence after a valid pinned Link was admitted.
					if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_versions WHERE path=? AND version=?`, targetPath, target.Revision); err != nil {
						t.Fatal(err)
					}
					expected = ErrVersionUnknown
				}
			}
			result, err := s.ReadMemoryRequires(ctx, req)
			if !errors.Is(err, expected) || !reflect.DeepEqual(result, MemoryRequiresRead{}) {
				t.Fatalf("%s: %+v %v", name, result, err)
			}
		}) {
			break // Preserve the first failure; do not reuse an indeterminate fixture.
		}
	}
}

func TestMemoryScopedAccumulatedByteBoundary(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, p := range []string{"a", "b", "c"} {
		if _, err := s.Create(ctx, CreateRequest{Path: "beads/" + p, TypeURL: s.ScopeURL() + "types/constraint", Title: p, Body: strings.Repeat(p, 512)}); err != nil {
			t.Fatal(err)
		}
	}
	for i, pair := range [][2]string{{"a", "b"}, {"b", "c"}} {
		if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: fmt.Sprintf("links/chain%d", i), TypeURL: s.ScopeURL() + "types/requires", SourcePath: "beads/" + pair[0], TargetPath: "beads/" + pair[1], UnconditionalSource: true}); err != nil {
			t.Fatal(err)
		}
	}
	req := memoryQueryRequest("beads/a")
	var rootCost uint64
	if err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		catalog, err := s.selectedTypes()
		if err != nil {
			return err
		}
		reader, err := newMemoryQueryReader(s, tx, catalog, req)
		if err != nil {
			return err
		}
		if _, err := reader.Resolve(ctx, req.Root); err != nil {
			return err
		}
		rootCost = reader.used
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Find the exact public admission threshold without duplicating its SQL cost formula.
	low, high := uint64(1), uint64(128*1024)
	for low < high {
		mid := (low + high) / 2
		req.MaxBytes = mid
		got, err := s.ReadMemoryRequires(ctx, req)
		if errors.Is(err, ErrLimitExceeded) {
			if !reflect.DeepEqual(got, MemoryRequiresRead{}) {
				t.Fatal("partial refused result")
			}
			low = mid + 1
		} else {
			if err != nil || len(got.Beads) != 3 {
				t.Fatal(got, err)
			}
			high = mid
		}
	}
	if low-1 <= rootCost {
		t.Fatalf("fixture does not test cumulative admission: root=%d total=%d", rootCost, low)
	}
	req.MaxBytes = low
	if got, err := s.ReadMemoryRequires(ctx, req); err != nil || len(got.Beads) != 3 {
		t.Fatalf("exact cap refused: %v %v", got, err)
	}
	req.MaxBytes = low - 1
	if got, err := s.ReadMemoryRequires(ctx, req); !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(got, MemoryRequiresRead{}) {
		t.Fatalf("below cap admitted: %v %v", got, err)
	}
}

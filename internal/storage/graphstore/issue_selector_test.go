//go:build cgo

package graphstore

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/workapi"
)

func TestIssueNativeSelectorHeartbeatStateAndReclaim(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			created, err := s.CreateIssue(ctx, "beads/custom-path", plainIssue("Custom"))
			if err != nil {
				t.Fatal(err)
			}
			if created.Properties.ID == "custom-path" {
				t.Fatal("fixture needs a native ID distinct from canonical path")
			}
			claimed, err := s.ClaimIssue(ctx, "beads/custom-path", "holder")
			if err != nil || !claimed.Changed {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			selector := IssueSelector{Path: "beads/" + created.Properties.ID, NativeID: created.Properties.ID}
			shown, err := s.ShowIssueSelected(ctx, selector)
			if err != nil || !reflect.DeepEqual(shown, claimed.Issue) {
				t.Fatalf("selected read: %+v %v", shown, err)
			}
			renewed, err := s.HeartbeatIssueSelected(ctx, selector, "holder")
			if err != nil || renewed.Revision != claimed.Issue.Revision || renewed.Properties.LeaseExpiresAt == nil {
				t.Fatalf("selected heartbeat changed retained History: %+v %v", renewed, err)
			}
			assertIssueEditCounts(t, ctx, s, created.Properties.ID, 2)
			assertIssueEditVersion(t, ctx, s, "beads/custom-path", claimed.Issue)
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE leases SET lease_expires_at=? WHERE issue_id=?`, time.Now().UTC().Add(-time.Hour), created.Properties.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			reclaimed, err := s.ReclaimExpiredIssueLeasesSelected(ctx, 0, types.ReclaimFilter{}, []IssueSelector{selector}, "reaper")
			if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != created.Properties.ID {
				t.Fatalf("selected recovery: %+v %v", reclaimed, err)
			}
			after, err := s.ShowIssue(ctx, "beads/custom-path")
			if err != nil || after.Revision == renewed.Revision || after.Properties.Status != types.StatusOpen {
				t.Fatalf("selected recovery lost Issue writer: %+v %v", after, err)
			}
			assertIssueEditCounts(t, ctx, s, created.Properties.ID, 3)
			stable := workflowState(t, ctx, s)
			if _, err := s.HeartbeatIssueSelected(ctx, selector, "holder"); !errors.Is(err, storage.ErrNotClaimable) || !reflect.DeepEqual(stable, workflowState(t, ctx, s)) {
				t.Fatalf("released holder heartbeat changed state: %v", err)
			}
		})
	}
}

func TestDependencyCreateSelectsOpaqueIssueIDInsideWrite(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			atPath := plainIssue("Path owner")
			atPath.Issue.ID = "exp-path-owner"
			if _, err := s.CreateIssue(ctx, "beads/exp-fixed", atPath); err != nil {
				t.Fatal(err)
			}
			literal := plainIssue("Literal ID owner")
			literal.Issue.ID = "beads/exp-fixed"
			literal.ForceIDPrefix = true
			if _, err := s.CreateIssue(ctx, "beads/other", literal); err != nil {
				t.Fatal(err)
			}
			target := plainIssue("Target")
			target.Issue.ID = "exp-target"
			if _, err := s.CreateIssue(ctx, "beads/target", target); err != nil {
				t.Fatal(err)
			}
			request := DependencyRequest{
				SourcePath: "beads/beads/exp-fixed", SourceNativeID: "beads/exp-fixed",
				TargetPath: "beads/target", Actor: "author",
			}
			created, err := s.AddDependency(ctx, request)
			if err != nil || !created.Changed || created.Source.Properties.ID != "beads/exp-fixed" || created.Link.Source != s.ScopeURL()+literal.Issue.ID {
				t.Fatalf("opaque source selected graph path owner: %+v %v", created, err)
			}
			pathOwner, err := s.ShowIssue(ctx, "beads/exp-fixed")
			if err != nil || len(pathOwner.Owned) != 0 {
				t.Fatalf("path owner changed by opaque dependency: %+v %v", pathOwner, err)
			}
			state := workflowState(t, ctx, s)
			request.SourceNativeID = "missing"
			request.SourcePath = "beads/missing"
			if _, err := s.AddDependency(ctx, request); !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(state, workflowState(t, ctx, s)) {
				t.Fatalf("missing public ID changed dependency state: %v", err)
			}
		})
	}
}

func TestIssueSelectorSharesNativeAbbreviationAcrossReadAndUpdate(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			request := plainIssue("Source")
			request.Issue.ID = "exp-a3f8e9"
			created, err := s.CreateIssue(ctx, "beads/source", request)
			if err != nil {
				t.Fatal(err)
			}
			selector := IssueSelector{Path: "beads/a3f8", NativeID: "a3f8"}
			shown, err := s.ShowIssueSelected(ctx, selector)
			if err != nil || !reflect.DeepEqual(shown, created) {
				t.Fatalf("abbreviated read selected wrong Issue: %+v %v", shown, err)
			}
			mixed, err := s.ReadBeadSelected(ctx, selector)
			if err != nil || !reflect.DeepEqual(mixed, created) {
				t.Fatalf("abbreviated mixed read selected wrong Issue: %+v %v", mixed, err)
			}
			detail, err := s.ShowIssueDetailsSelected(ctx, selector, workapi.DetailOptions{})
			if err != nil || detail == nil || detail.ID != created.Properties.ID || detail.Revision != created.Revision || detail.Title != created.Properties.Title {
				t.Fatalf("abbreviated detail read selected wrong Issue: %+v %v", detail, err)
			}
			combined, combinedDetail, err := s.ReadBeadWithIssueDetailsSelected(ctx, selector, workapi.DetailOptions{})
			if err != nil || !reflect.DeepEqual(combined, created) || !reflect.DeepEqual(combinedDetail, detail) {
				t.Fatalf("combined abbreviated read changed either projection: %+v %+v %v", combined, combinedDetail, err)
			}
			memory, err := s.Create(ctx, CreateRequest{Path: "beads/notes", Title: "Notes"})
			if err != nil {
				t.Fatal(err)
			}
			mixed, err = s.ReadBeadSelected(ctx, IssueSelector{Path: "beads/notes", NativeID: "notes"})
			if err != nil || !reflect.DeepEqual(mixed, memory) {
				t.Fatalf("bare Memory fallback changed selection: %+v %v", mixed, err)
			}
			if _, err := s.ShowIssueDetailsSelected(ctx, IssueSelector{Path: "beads/notes"}, workapi.DetailOptions{}); !errors.Is(err, ErrCapabilityUnavailable) {
				t.Fatalf("explicit Memory detail read should refuse as non-Issue: %v", err)
			}
			combined, combinedDetail, err = s.ReadBeadWithIssueDetailsSelected(ctx, IssueSelector{Path: "beads/notes", NativeID: "notes"}, workapi.DetailOptions{})
			if err != nil || !reflect.DeepEqual(combined, memory) || combinedDetail != nil {
				t.Fatalf("combined Memory read fabricated Issue details: %+v %+v %v", combined, combinedDetail, err)
			}
			paths, err := s.ResolveIssuePaths(ctx, []IssueSelector{selector})
			if err != nil || !reflect.DeepEqual(paths, []string{created.Properties.ID}) {
				t.Fatalf("abbreviated count filter selected wrong path: %v %v", paths, err)
			}
			title := "Updated"
			updated, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: selector.Path, NativeID: selector.NativeID, Actor: "editor", ExpectedRevision: created.Revision, Title: &title})
			if err != nil || !updated.Changed || updated.Issue.ID != created.ID || updated.Issue.Properties.Title != title {
				t.Fatalf("abbreviated update selected wrong Issue: %+v %v", updated, err)
			}
			second := plainIssue("Other")
			second.Issue.ID = "exp-a3f8f0"
			if _, err := s.CreateIssue(ctx, "beads/other", second); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			if _, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: selector.Path, NativeID: selector.NativeID, Actor: "editor", ExpectedRevision: updated.Issue.Revision, Title: &title}); !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("ambiguous abbreviation changed Issue: %v", err)
			}
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/a3f8", Title: "Colliding Memory"}); err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			if _, err := s.ShowIssueSelected(ctx, selector); !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("bare graph-path collision selected an Issue: %v", err)
			}
			if _, err := s.ReadBeadSelected(ctx, selector); !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("bare mixed read hid an Issue/Memory collision: %v", err)
			}
			if _, err := s.ShowIssueDetailsSelected(ctx, selector, workapi.DetailOptions{}); !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("bare detail read hid an Issue/Memory collision: %v", err)
			}
			if _, _, err := s.ReadBeadWithIssueDetailsSelected(ctx, selector, workapi.DetailOptions{}); !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("combined read hid an Issue/Memory collision: %v", err)
			}
			explicit, err := s.ShowIssueSelected(ctx, IssueSelector{Path: "beads/source"})
			if err != nil || explicit.ID != created.ID || explicit.Properties.Title != title {
				t.Fatalf("explicit path did not stay exact: %+v %v", explicit, err)
			}
			detail, err = s.ShowIssueDetailsSelected(ctx, IssueSelector{Path: "beads/source"}, workapi.DetailOptions{})
			if err != nil || detail == nil || detail.ID != created.Properties.ID || detail.Title != title {
				t.Fatalf("explicit detail path did not stay exact: %+v %v", detail, err)
			}
			mixed, err = s.ReadBeadSelected(ctx, IssueSelector{Path: "beads/a3f8"})
			if err != nil {
				t.Fatalf("explicit Memory path did not stay exact: %v", err)
			}
			if _, ok := mixed.(Record); !ok {
				t.Fatalf("explicit Memory path returned %T", mixed)
			}
		})
	}
}

func TestAllocatedPublicIDResolvesHistoricalTargetsAfterDeletion(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			request := plainIssue("Historical Issue")
			request.Issue.ID = "exp-historical"
			if _, err := s.CreateIssue(ctx, "beads/private-allocation", request); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/private-allocation", Unconditional: true}); err != nil {
				t.Fatal(err)
			}
			if path, err := s.ResolveAllocatedResourcePath(ctx, request.Issue.ID); err != nil || path != "beads/private-allocation" {
				t.Fatalf("deleted Issue public ID: %q %v", path, err)
			}
			if path, err := s.ResolveExactAllocatedResourcePath(ctx, request.Issue.ID); err != nil || path != "beads/private-allocation" {
				t.Fatalf("exact deleted Issue public ID: %q %v", path, err)
			}
			if _, err := s.ResolveAllocatedResourcePath(ctx, "private-allocation"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("private Issue path became public shorthand: %v", err)
			}
			if _, err := s.Create(ctx, CreateRequest{PublicID: "beads/context", Title: "Historical Memory"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/context", Unconditional: true}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"beads/context"} {
				if path, err := s.ResolveAllocatedResourcePath(ctx, id); err != nil || path != "beads/context" {
					t.Fatalf("deleted Memory selector %q: %q %v", id, path, err)
				}
			}
			if path, err := s.ResolveExactAllocatedResourcePath(ctx, "beads/context"); err != nil || path != "beads/context" {
				t.Fatalf("exact deleted Memory public ID: %q %v", path, err)
			}
			before := workflowState(t, ctx, s)
			if _, err := s.ResolveAllocatedResourcePath(ctx, "context"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("private Memory suffix became a public alias: %v", err)
			}
			if _, err := s.ResolveExactAllocatedResourcePath(ctx, "context"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("compatibility suffix became an exact write ID: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("refused private suffix changed state")
			}
		})
	}
}

func TestOpaqueResourceReadSelectorKeepsPublicIDsSeparateFromGraphPaths(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			literal := plainIssue("Literal")
			literal.Issue.ID = "exp-beads/literal"
			_, err = s.CreateIssue(ctx, "beads/allocation", literal)
			if err != nil {
				t.Fatal(err)
			}
			path, record, details, err := s.ReadResourceWithIssueDetailsSelected(ctx, "beads/exp-beads/literal", "exp-beads/literal", workapi.DetailOptions{})
			if read, ok := record.(IssueRecord); err != nil || path != "beads/allocation" || !ok || read.Properties.ID != literal.Issue.ID || details == nil || details.ID != literal.Issue.ID {
				t.Fatalf("literal public ID read: path=%q record=%+v details=%+v err=%v", path, record, details, err)
			}
			pathOnly := plainIssue("Path only")
			pathOnly.Issue.ID = "exp-other-id"
			pathOnlyRecord, err := s.CreateIssue(ctx, "beads/path-only", pathOnly)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := s.ReadResourceWithIssueDetailsSelected(ctx, "beads/beads/path-only", "beads/path-only", workapi.DetailOptions{}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("local path spelling silently selected an unrelated Issue: %v", err)
			}
			path, record, details, err = s.ReadResourceWithIssueDetailsSelected(ctx, pathOnlyRecord.ID, "", workapi.DetailOptions{})
			if read, ok := record.(IssueRecord); err != nil || path != "beads/path-only" || !ok || read.Properties.ID != pathOnly.Issue.ID || details == nil || details.ID != pathOnly.Issue.ID {
				t.Fatalf("canonical public ID read: path=%q record=%+v details=%+v err=%v", path, record, details, err)
			}
			memory, err := s.Create(ctx, CreateRequest{PublicID: "beads/notes", Title: "Notes"})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			if _, _, _, err := s.ReadResourceWithIssueDetailsSelected(ctx, "beads/notes", "notes", workapi.DetailOptions{}); !errors.Is(err, ErrNotFound) {
				t.Fatalf("explicit Memory public ID acquired a private suffix alias: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("refused private Memory alias changed state")
			}

			path, record, details, err = s.ReadResourceWithIssueDetailsSelected(ctx, "beads/beads/notes", "beads/notes", workapi.DetailOptions{})
			if read, ok := record.(Record); err != nil || path != "beads/notes" || !ok || read.ID != memory.ID || details != nil {
				t.Fatalf("Memory public path spelling: path=%q record=%+v details=%+v err=%v", path, record, details, err)
			}
			link, err := s.AddDependency(ctx, DependencyRequest{Path: "links/edge", PublicID: "links/edge", SourcePath: "beads/allocation", TargetPath: "beads/path-only", Actor: "author"})
			if err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			if _, _, _, err := s.ReadResourceWithIssueDetailsSelected(ctx, "beads/edge", "edge", workapi.DetailOptions{}); !errors.Is(err, ErrNotFound) {
				t.Errorf("explicit Link public ID acquired a private suffix alias: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("private Link alias read changed state")
			}
			// Old allocations without explicit public IDs retain suffix compatibility.
			// Its public ID already equals the private path, so this marker change
			// does not change any current or retained canonical record identity.
			if _, err := s.db.ExecContext(ctx, "UPDATE graph_preview_catalog SET public_id=NULL WHERE path='links/edge'"); err != nil {
				t.Fatal(err)
			}
			legacyBefore := workflowState(t, ctx, s)
			path, record, details, err = s.ReadResourceWithIssueDetailsSelected(ctx, "beads/edge", "edge", workapi.DetailOptions{})
			if read, ok := record.(LinkRecord); err != nil || path != "links/edge" || !ok || read.ID != link.Link.ID || details != nil {
				t.Fatalf("legacy NULL-ID Link suffix compatibility changed: %q %+v %v", path, record, err)
			}
			if !reflect.DeepEqual(legacyBefore, workflowState(t, ctx, s)) {
				t.Fatal("legacy suffix read changed state")
			}
			if _, err := s.db.ExecContext(ctx, "UPDATE graph_preview_catalog SET public_id='links/edge' WHERE path='links/edge'"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("legacy marker fixture did not restore exact allocation")
			}

			path, record, details, err = s.ReadResourceWithIssueDetailsSelected(ctx, "beads/links/edge", "links/edge", workapi.DetailOptions{})
			if read, ok := record.(LinkRecord); err != nil || path != "links/edge" || !ok || read.ID != link.Link.ID || details != nil {
				t.Fatalf("Link public path spelling: path=%q record=%+v details=%+v err=%v", path, record, details, err)
			}
			distinct, err := s.Create(ctx, CreateRequest{PublicID: "edge", Title: "Distinct public spelling"})
			if err != nil {
				t.Fatal(err)
			}
			path, record, details, err = s.ReadResourceWithIssueDetailsSelected(ctx, "beads/edge", "edge", workapi.DetailOptions{})
			if read, ok := record.(Record); err != nil || !ok || read.ID != distinct.ID || details != nil {
				t.Fatalf("exact Memory ID did not win over unrelated private Link suffix: %q %+v %v", path, record, err)
			}

			if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/edge", Actor: "remover", ExpectedRevision: link.Link.Revision, ExpectedSourceRevision: link.Source.Revision}); err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			if _, _, _, err := s.ReadResourceWithIssueDetailsSelected(ctx, "beads/links/edge", "links/edge", workapi.DetailOptions{}); !errors.Is(err, ErrGone) {
				t.Fatalf("deleted exact Link ID lost lifetime authority: %v", err)
			}
			retained, err := s.ReadVersion(ctx, link.Link.ID, link.Link.Revision)
			if read, ok := retained.(LinkRecord); err != nil || !ok || read.ID != link.Link.ID || read.Source != link.Link.Source || read.Target != link.Link.Target {
				t.Fatalf("retained Link identity/endpoints changed: %+v %v", retained, err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("deleted/retained Link reads changed state")
			}
			path, record, details, err = s.ReadResourceWithIssueDetailsSelected(ctx, "beads/edge", "edge", workapi.DetailOptions{})
			if read, ok := record.(Record); err != nil || path != "beads/edge" || !ok || read.Properties.Title != "Distinct public spelling" || details != nil {
				t.Fatalf("removed Link still shadowed live Memory: path=%q record=%+v details=%+v err=%v", path, record, details, err)
			}
		})
	}
}

func TestOpaqueIssueIDsSurvivePrivatePathOverlapInBothCreateOrders(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		for _, order := range [][]string{{"exp-x", "exp-beads/x"}, {"exp-beads/x", "exp-x"}} {
			t.Run(backend+"/"+order[0]+"-first", func(t *testing.T) {
				ctx, options := issueExperimentOptions(t, backend)
				s, err := OpenExisting(ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = s.Close() })
				paths := map[string]string{}
				privatePaths := map[string]string{}
				for _, id := range order {
					request := plainIssue(id)
					request.Issue.ID = id
					created, err := s.CreateIssue(ctx, "", request)
					if err != nil {
						t.Fatalf("create literal %q: %v", id, err)
					}
					paths[id] = created.ID
					privatePaths[id], err = s.ResolveExactAllocatedResourcePath(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
				}
				if paths[order[0]] == paths[order[1]] || privatePaths[order[0]] == privatePaths[order[1]] {
					t.Fatal("distinct public IDs shared a graph allocation")
				}
				for _, id := range order {
					selector := IssueSelector{Path: "beads/" + id, NativeID: id}
					path, record, details, err := s.ReadResourceWithIssueDetailsSelected(ctx, selector.Path, id, workapi.DetailOptions{})
					if issue, ok := record.(IssueRecord); err != nil || !ok || issue.Properties.ID != id || issue.ID != paths[id] || details == nil || details.ID != id || paths[id] != options.Binding.ScopeURL+id || path != privatePaths[id] {
						t.Fatalf("literal %q read: path=%q record=%+v details=%+v err=%v", id, path, record, details, err)
					}
					closed, err := s.CloseIssueWithOptions(ctx, IssueCloseRequest{Path: selector.Path, NativeID: id, Reason: "done", Actor: "test-author"})
					if err != nil || !closed.Changed || closed.Issue.Properties.ID != id {
						t.Fatalf("literal %q close: %+v %v", id, closed, err)
					}
				}
			})
		}
	}
}

func TestPublicResourceIDCreationRejectsCrossKindCollisions(t *testing.T) {
	for _, memoryFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "memory-first", false: "issue-first"}[memoryFirst], func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, "embedded")
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			issue := plainIssue("Same public ID")
			issue.Issue.ID = "beads/exp-shared"
			issue.ForceIDPrefix = true
			createIssue := func() error { _, err := s.CreateIssue(ctx, "", issue); return err }
			createMemory := func() error {
				_, err := s.Create(ctx, CreateRequest{Path: "beads/exp-shared", Title: "Same public ID"})
				return err
			}
			if memoryFirst {
				if err := createMemory(); err != nil {
					t.Fatal(err)
				}
				if err := createIssue(); !errors.Is(err, ErrAlreadyExists) {
					t.Fatalf("Issue reused Memory public ID: %v", err)
				}
			} else {
				if err := createIssue(); err != nil {
					t.Fatal(err)
				}
				if err := createMemory(); !errors.Is(err, ErrAlreadyExists) {
					t.Fatalf("Memory reused Issue public ID: %v", err)
				}
			}
		})
	}
	for _, linkFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "link-first", false: "issue-first"}[linkFirst], func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, "embedded")
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			for _, id := range []string{"exp-source", "exp-target"} {
				request := plainIssue(id)
				request.Issue.ID = id
				if _, err := s.CreateIssue(ctx, "", request); err != nil {
					t.Fatal(err)
				}
			}
			issue := plainIssue("Same public ID")
			issue.Issue.ID = "links/exp-edge"
			issue.ForceIDPrefix = true
			createIssue := func() error { _, err := s.CreateIssue(ctx, "", issue); return err }
			createLink := func() error {
				_, err := s.AddDependency(ctx, DependencyRequest{Path: "links/exp-edge", SourcePath: "beads/exp-source", TargetPath: "beads/exp-target", Actor: "test-author"})
				return err
			}
			if linkFirst {
				if err := createLink(); err != nil {
					t.Fatal(err)
				}
				if err := createIssue(); !errors.Is(err, ErrAlreadyExists) {
					t.Fatalf("Issue reused Link public ID: %v", err)
				}
			} else {
				if err := createIssue(); err != nil {
					t.Fatal(err)
				}
				if err := createLink(); !errors.Is(err, ErrAlreadyExists) {
					t.Fatalf("Link reused Issue public ID: %v", err)
				}
			}
		})
	}
}

func TestDeletedIssueAllocationKeepsPublicIDReserved(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			store, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			request := plainIssue("Original")
			request.Issue.ID = "exp-reserved"
			created, err := store.CreateIssue(ctx, "beads/original", request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.DeleteIssue(ctx, IssueDeleteRequest{
				Path: "beads/original", ExpectedRevision: created.Revision, Actor: "deleter",
			}); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, store)
			request = plainIssue("Second allocation")
			request.Issue.ID = "exp-reserved"
			_, err = store.CreateIssue(ctx, "", request)
			if !errors.Is(err, ErrAlreadyExists) || !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
				t.Fatalf("deleted Issue public ID was reused or state changed: %v", err)
			}
		})
	}
}

func TestExactIssuePublicIDBeatsMemoryPathShorthand(t *testing.T) {
	for _, memoryFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "memory-first", false: "issue-first"}[memoryFirst], func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, "embedded")
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			request := plainIssue("Issue exp-x")
			request.Issue.ID = "exp-x"
			createMemory := func() {
				t.Helper()
				if _, err := s.Create(ctx, CreateRequest{Path: "beads/exp-x", Title: "Memory beads/exp-x"}); err != nil {
					t.Fatal(err)
				}
			}
			if memoryFirst {
				createMemory()
			}
			issuePath := "beads/exp-x-issue"
			if memoryFirst {
				issuePath = ""
			}
			created, err := s.CreateIssue(ctx, issuePath, request)
			if err != nil {
				t.Fatal(err)
			}
			if !memoryFirst {
				createMemory()
			}
			_, record, details, err := s.ReadResourceWithIssueDetailsSelected(ctx, "beads/exp-x", "exp-x", workapi.DetailOptions{})
			if got, ok := record.(IssueRecord); err != nil || !ok || got.ID != created.ID || details == nil || details.ID != "exp-x" {
				t.Fatalf("exact Issue ID lost to Memory shorthand: %+v %+v %v", record, details, err)
			}
			_, record, details, err = s.ReadResourceWithIssueDetailsSelected(ctx, "beads/beads/exp-x", "beads/exp-x", workapi.DetailOptions{})
			if got, ok := record.(Record); err != nil || !ok || got.Properties.Title != "Memory beads/exp-x" || details != nil {
				t.Fatalf("exact Memory public path selected wrong resource: %+v %+v %v", record, details, err)
			}
		})
	}
}

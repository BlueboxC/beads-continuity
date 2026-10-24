//go:build cgo

package graphstore

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	publicops "github.com/steveyegge/beads/issueops"
)

func TestIssueDeleteRetainsHistoryAndRefusesIncidentLinks(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, backend)
			issue, err := s.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			preview, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/work", Preview: true})
			if err != nil || !preview.Preview || preview.Deleted || !reflect.DeepEqual(preview.Issue, issue) {
				t.Fatalf("preview: %+v %v", preview, err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("preview changed state")
			}
			if _, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/work"}); err == nil {
				t.Fatal("apply without a guard succeeded")
			}
			if _, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/work", ExpectedRevision: "stale"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale guard: %v", err)
			}
			other, err := s.CreateIssue(ctx, "beads/other", plainIssue("Other"))
			if err != nil {
				t.Fatal(err)
			}
			// Preserve the native neighbor control while using a legal unowned
			// Issue-to-Memory References Link for the incident-Link refusal.
			contextMemory, err := s.Create(ctx, CreateRequest{Path: "beads/context", Title: "Context"})
			if err != nil {
				t.Fatal(err)
			}
			link, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/relation", SourcePath: "beads/work", TargetPath: "beads/context", TypeURL: RelatedTypeURL(s.ScopeURL())})
			if err != nil {
				t.Fatal(err)
			}
			for _, preview := range []bool{true, false} {
				_, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/work", ExpectedRevision: issue.Revision, Preview: preview})
				if !errors.Is(err, ErrIncidentLinkConstraint) || !strings.Contains(err.Error(), link.Link.ID) {
					t.Fatalf("incident Link refusal: %v", err)
				}
			}
			if got, err := s.ShowIssue(ctx, "beads/work"); err != nil || !reflect.DeepEqual(got, issue) {
				t.Fatalf("refusal changed Issue: %+v %v", got, err)
			}
			if got, err := s.ShowIssue(ctx, "beads/other"); err != nil || !reflect.DeepEqual(got, other) {
				t.Fatalf("refusal changed neighbor: %+v %v", got, err)
			}
			if got, err := s.Read(ctx, "beads/context"); err != nil || !reflect.DeepEqual(got, contextMemory) {
				t.Fatalf("refusal changed Memory neighbor: %+v %v", got, err)
			}
			if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/relation", ExpectedRevision: link.Link.Revision, DefaultInformationalSource: true}); err != nil {
				t.Fatal(err)
			}
			final, err := s.ShowIssue(ctx, "beads/work")
			if err != nil {
				t.Fatal(err)
			}
			beforeDelete := workflowState(t, ctx, s)
			for _, stage := range []string{"coordination", "issue-delete-native", "issue-delete-allocation", "issue-delete-retained"} {
				fault := errors.New("injected Issue delete failure")
				s.afterWrite = func(at string) error {
					if at == stage {
						return fault
					}
					return nil
				}
				got, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/work", ExpectedRevision: final.Revision})
				s.afterWrite = nil
				if !errors.Is(err, fault) || !reflect.ValueOf(got).IsZero() || !reflect.DeepEqual(beforeDelete, workflowState(t, ctx, s)) {
					t.Fatalf("%s rollback: %+v %v", stage, got, err)
				}
			}
			kind, prior, err := s.Versions(ctx, "beads/work")
			if err != nil || kind != KindIssue {
				t.Fatalf("prior history: %s %+v %v", kind, prior, err)
			}
			deleted, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/work", ExpectedRevision: final.Revision, Actor: "deleter"})
			if err != nil || !deleted.Deleted || deleted.Preview || !reflect.DeepEqual(deleted.Issue, final) {
				t.Fatalf("apply: %+v %v", deleted, err)
			}
			if _, err := s.ShowIssue(ctx, "beads/work"); !errors.Is(err, ErrGone) {
				t.Fatalf("current Issue: %v", err)
			}
			kind, retained, err := s.Versions(ctx, "beads/work")
			if err != nil || kind != KindIssue || deleted.Terminal == nil || len(retained) != len(prior)+1 || !retained[0].Removed || retained[0].Version != deleted.Terminal.Revision || !reflect.DeepEqual(retained[1:], prior) {
				t.Fatalf("deletion lost live history or distinct D: %s %+v %v", kind, retained, err)
			}
			old, err := s.ReadVersion(ctx, "beads/work", final.Version)
			retainedIssue, ok := old.(IssueRecord)
			if err != nil || !ok || retainedIssue.ID != final.ID || retainedIssue.Type != final.Type || retainedIssue.Version != final.Version ||
				retainedIssue.Properties == nil || retainedIssue.Properties.ID != final.Properties.ID ||
				retainedIssue.Properties.Title != final.Properties.Title || len(retainedIssue.Owned) != 0 {
				t.Fatalf("final live snapshot: %+v %v", old, err)
			}
			if _, err := s.CreateIssue(ctx, "beads/work", plainIssue("Reuse")); !errors.Is(err, ErrAlreadyExists) {
				t.Fatalf("identity reused: %v", err)
			}
			snapshot, err := s.CurrentSnapshot(ctx)
			if err != nil || len(snapshot.Records) != 2 || !reflect.DeepEqual(snapshot.Records[0], contextMemory) || !reflect.DeepEqual(snapshot.Records[1], other) {
				t.Fatalf("current inventory: %+v %v", snapshot, err)
			}
			if _, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/work", Unconditional: true}); !errors.Is(err, ErrGone) {
				t.Fatalf("repeat delete: %v", err)
			}
		})
	}
}

func TestIssueDeleteRefusesBlockingLinksWithoutNativeCascade(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			f := setupDependencyUnlink(t, backend, 2)
			before := workflowState(t, f.ctx, f.store)
			for _, preview := range []bool{true, false} {
				_, err := f.store.DeleteIssue(f.ctx, IssueDeleteRequest{Path: "beads/source", ExpectedRevision: f.source.Revision, Preview: preview})
				if !errors.Is(err, ErrIncidentLinkConstraint) {
					t.Fatalf("blocking Link refusal: %v", err)
				}
				first, second := f.links[0].ID, f.links[1].ID
				if !strings.Contains(err.Error(), first+", "+second) {
					t.Fatalf("incident Link IDs not sorted and complete: %v", err)
				}
				if !reflect.DeepEqual(before, workflowState(t, f.ctx, f.store)) {
					t.Fatal("refusal changed native Issue, dependency or graph state")
				}
			}
		})
	}
}

func TestIssueDeleteComposesDependencyUnlinksInOneTransaction(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			f := setupDependencyUnlink(t, backend, 2)
			request := IssueDeleteRequest{
				Path: "beads/source", Actor: "deleter", ExpectedRevision: f.source.Revision,
			}
			before := workflowState(t, f.ctx, f.store)
			stale := request
			stale.ExpectedRevision = "stale"
			if got, err := f.store.DeleteIssueWithIncidentLinks(f.ctx, stale); !errors.Is(err, ErrConflict) ||
				!reflect.ValueOf(got).IsZero() || !reflect.DeepEqual(before, workflowState(t, f.ctx, f.store)) {
				t.Fatalf("stale linked deletion: %+v %v", got, err)
			}
			preview, err := f.store.DeleteIssueWithIncidentLinks(f.ctx, IssueDeleteRequest{
				Path: request.Path, ExpectedRevision: request.ExpectedRevision,
				Preview: true,
			})
			if err != nil || !preview.Preview || preview.Deleted || len(preview.IncidentLinkIDs) != 2 ||
				preview.IncidentLinkIDs[0] != f.links[0].ID || preview.IncidentLinkIDs[1] != f.links[1].ID ||
				!reflect.DeepEqual(before, workflowState(t, f.ctx, f.store)) {
				t.Fatalf("checked preview: %+v %v", preview, err)
			}
			unforced, err := f.store.PreviewDeleteIssueWithIncidentLinks(f.ctx, request)
			if err != nil || !unforced.Preview || unforced.Deleted ||
				!reflect.DeepEqual(unforced.IncidentLinkIDs, preview.IncidentLinkIDs) ||
				!reflect.DeepEqual(before, workflowState(t, f.ctx, f.store)) {
				t.Fatalf("unforced outgoing preview: %+v %v", unforced, err)
			}
			for _, stage := range []string{"dependency-remove", "link-catalog", "link-retained", "issue-delete-native", "issue-delete-retained"} {
				fault := errors.New("injected composed delete failure")
				f.store.afterWrite = func(at string) error {
					if at == stage {
						return fault
					}
					return nil
				}
				got, err := f.store.DeleteIssueWithIncidentLinks(f.ctx, request)
				f.store.afterWrite = nil
				if !errors.Is(err, fault) || !reflect.ValueOf(got).IsZero() || !reflect.DeepEqual(before, workflowState(t, f.ctx, f.store)) {
					t.Fatalf("%s rollback: %+v %v", stage, got, err)
				}
			}
			deleted, err := f.store.DeleteIssueWithIncidentLinks(f.ctx, request)
			if err != nil || !deleted.Deleted || deleted.Terminal == nil || len(deleted.IncidentLinkIDs) != 2 ||
				deleted.Issue.Revision == f.source.Revision || deleted.Terminal.PreviousVersion != deleted.Issue.Revision {
				t.Fatalf("composed deletion: %+v %v", deleted, err)
			}
			if _, err := f.store.ShowIssue(f.ctx, "beads/source"); !errors.Is(err, ErrGone) {
				t.Fatalf("deleted Issue still live: %v", err)
			}
			for i, link := range f.links {
				path := strings.TrimPrefix(link.ID, f.store.ScopeURL())
				if _, err := f.store.ShowLink(f.ctx, path); !errors.Is(err, ErrGone) {
					t.Fatalf("deleted Link %d still live: %v", i, err)
				}
				kind, versions, err := f.store.Versions(f.ctx, path)
				if err != nil || kind != KindLink || len(versions) != 2 || !versions[0].Removed {
					t.Fatalf("Link %d tombstone: %s %+v %v", i, kind, versions, err)
				}
			}
			for i, target := range f.targets {
				got, err := f.store.ShowIssue(f.ctx, fixtureResourcePath(t, f.ctx, f.store, target.ID))
				if err != nil || !reflect.DeepEqual(got, target) {
					t.Fatalf("target %d changed: %+v %v", i, got, err)
				}
			}
		})
	}
}

func TestIssueDeleteComposesIncomingDependencyUnlink(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			f := setupDependencyUnlink(t, backend, 1)
			before := workflowState(t, f.ctx, f.store)
			preview, err := f.store.PreviewDeleteIssueWithIncidentLinks(f.ctx, IssueDeleteRequest{
				Path: "beads/first", ExpectedRevision: f.targets[0].Revision,
			})
			var blocked *publicops.DependentsOutsideRequestError
			if !errors.As(err, &blocked) || blocked.IssueID != f.targets[0].Properties.ID ||
				!reflect.DeepEqual(blocked.Dependents, []string{f.source.Properties.ID}) ||
				!reflect.ValueOf(preview).IsZero() || !reflect.DeepEqual(before, workflowState(t, f.ctx, f.store)) {
				t.Fatalf("unforced incoming preview: %+v %v", preview, err)
			}
			forcePreview, err := f.store.DeleteIssueWithIncidentLinks(f.ctx, IssueDeleteRequest{
				Path: "beads/first", ExpectedRevision: f.targets[0].Revision, Preview: true,
			})
			if err != nil || !forcePreview.Preview || forcePreview.Deleted || len(forcePreview.IncidentLinkIDs) != 1 ||
				!reflect.DeepEqual(before, workflowState(t, f.ctx, f.store)) {
				t.Fatalf("force-mode incoming preview: %+v %v", forcePreview, err)
			}
			deleted, err := f.store.DeleteIssueWithIncidentLinks(f.ctx, IssueDeleteRequest{
				Path: "beads/first", Actor: "deleter", ExpectedRevision: f.targets[0].Revision,
			})
			if err != nil || !deleted.Deleted || deleted.Terminal == nil ||
				deleted.Terminal.PreviousVersion != f.targets[0].Revision {
				t.Fatalf("incoming dependency delete: %+v %v", deleted, err)
			}
			source, err := f.store.ShowIssue(f.ctx, "beads/source")
			if err != nil || source.Revision == f.source.Revision || len(source.Owned) != 0 {
				t.Fatalf("surviving dependent: %+v %v", source, err)
			}
			if _, err := f.store.ShowLink(f.ctx, "links/first"); !errors.Is(err, ErrGone) {
				t.Fatalf("incoming Link still live: %v", err)
			}
		})
	}
}

func TestIssueDeleteComposesInformationalLinkUnlink(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, backend)
			issue, err := s.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil {
				t.Fatal(err)
			}
			memory, err := s.Create(ctx, CreateRequest{Path: "beads/context", Title: "Context"})
			if err != nil {
				t.Fatal(err)
			}
			added, err := s.AddInformationalLink(ctx, LinkCreateRequest{
				Path: "links/context", SourcePath: "beads/context", TargetPath: "beads/work",
				Actor: "author", ExpectedSourceRevision: memory.Revision,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := IssueDeleteRequest{Path: "beads/work", Actor: "deleter", ExpectedRevision: issue.Revision}
			before := workflowState(t, ctx, s)
			preview, err := s.PreviewDeleteIssueWithIncidentLinks(ctx, request)
			if err != nil || !preview.Preview || preview.Deleted || len(preview.IncidentLinkIDs) != 1 ||
				!reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("informational Link preview: %+v %v", preview, err)
			}
			for _, stage := range []string{"link-payload", "link-retained", "issue-delete-native"} {
				fault := errors.New("injected owner delete failure")
				s.afterWrite = func(at string) error {
					if at == stage {
						return fault
					}
					return nil
				}
				got, err := s.DeleteIssueWithIncidentLinks(ctx, request)
				s.afterWrite = nil
				if !errors.Is(err, fault) || !reflect.ValueOf(got).IsZero() || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("%s rollback: %+v %v", stage, got, err)
				}
			}
			deleted, err := s.DeleteIssueWithIncidentLinks(ctx, request)
			if err != nil || !deleted.Deleted || len(deleted.IncidentLinkIDs) != 1 || deleted.IncidentLinkIDs[0] != added.Link.ID {
				t.Fatalf("delete with Memory owner: %+v %v", deleted, err)
			}
			current, err := s.Read(ctx, "beads/context")
			memoryAfter, ok := current.(Record)
			if err != nil || !ok || memoryAfter.Revision != memory.Revision || len(memoryAfter.Owned) != 0 {
				t.Fatalf("unowned Memory source after unlink: %+v %v", current, err)
			}
			if _, err := s.ShowLink(ctx, "links/context"); !errors.Is(err, ErrGone) {
				t.Fatalf("incident Link still live: %v", err)
			}
		})
	}
}

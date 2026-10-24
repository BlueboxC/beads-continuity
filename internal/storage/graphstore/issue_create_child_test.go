//go:build cgo

package graphstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestCreateChildIssueAtomicNativeParentLink(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			_, options := issueExperimentOptions(t, backend)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			t.Cleanup(cancel)
			ctx = issueops.WithEventsJournal(ctx, true)
			store, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			parent, err := store.CreateIssue(ctx, "beads/selected-parent", publicops.CreateRequest{
				Actor: "parent-author", Issue: &types.Issue{Title: "Parent", Status: types.StatusOpen,
					Priority: 2, IssueType: types.TypeEpic, Labels: []string{"parent-label"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := IssueChildCreateRequest{
				ParentPath: "beads/selected-parent", ExpectedParentRevision: parent.Revision, InheritLabels: true,
				Create: publicops.CreateRequest{Actor: "child-author", Issue: &types.Issue{
					Title: "Child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
					Labels: []string{"child-label"}, Notes: "first note",
				}},
			}
			before := workflowState(t, ctx, store)
			var beforeCounters int
			if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM child_counters`).Scan(&beforeCounters); err != nil {
				t.Fatal(err)
			}
			generation := store.options.TypeGeneration
			store.options.TypeGeneration = 6
			if _, err := store.CreateChildIssue(ctx, request); !errors.Is(err, ErrInvalidStore) ||
				!reflect.DeepEqual(workflowState(t, ctx, store), before) {
				t.Fatalf("marker/Type mismatch admitted child create or changed state: %v", err)
			}
			store.options.TypeGeneration = generation
			if _, err := store.CreateChildIssue(ctx, IssueChildCreateRequest{
				ParentPath: request.ParentPath, ExpectedParentRevision: request.ExpectedParentRevision,
				Create: publicops.CreateRequest{Actor: request.Create.Actor, Issue: &types.Issue{
					ID: "explicit", Title: "Rejected", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
				}},
			}); !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(workflowState(t, ctx, store), before) {
				t.Fatalf("explicit child ID admitted or changed state: %v", err)
			}
			stale := request
			stale.ExpectedParentRevision = "00000000000000000000000000000000"
			if _, err := store.CreateChildIssue(ctx, stale); !errors.Is(err, ErrConflict) ||
				!reflect.DeepEqual(workflowState(t, ctx, store), before) {
				t.Fatalf("stale parent revision changed state: %v", err)
			}
			fault := errors.New("injected child create failure")
			for _, stage := range []string{"child-issue", "child-catalog", "child-link-catalog", "child-link-retained", "child-retained"} {
				store.afterWrite = func(at string) error {
					if at == stage {
						return fault
					}
					return nil
				}
				_, err := store.CreateChildIssue(ctx, request)
				store.afterWrite = nil
				if !errors.Is(err, fault) || !reflect.DeepEqual(workflowState(t, ctx, store), before) {
					t.Fatalf("failure at %s left Issue/Link effects: %v", stage, err)
				}
				var counters int
				if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM child_counters`).Scan(&counters); err != nil || counters != beforeCounters {
					t.Fatalf("failure at %s burned a child counter: count=%d err=%v", stage, counters, err)
				}
			}
			created, err := store.CreateChildIssue(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			wantType, ok := RelationshipDependencyTypeURL(options.Binding.ScopeURL, types.DepParentChild)
			if !ok {
				t.Fatal("missing concrete parent-child mapping")
			}
			if !strings.HasPrefix(created.Child.Properties.ID, parent.Properties.ID+".") ||
				created.Child.ID != options.Binding.ScopeURL+created.Child.Properties.ID ||
				created.Link.Source != created.Child.ID || created.Link.Target != parent.ID ||
				created.Link.Type != wantType ||
				created.Link.Properties["dep_type"] != string(types.DepParentChild) ||
				len(created.Child.Owned) != 1 {
				t.Fatalf("child Issue and Link disagree: %+v", created)
			}
			if created.Child.Attribution.Inception != "create" || created.Child.Attribution.HistoricalUpdate != nil {
				t.Fatalf("child initial context: %+v", created.Child.Attribution)
			}
			retained, err := store.ReadVersion(ctx, created.Child.ID, created.Child.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if got := retained.(IssueRecord); got.ID != created.Child.ID || got.Revision != created.Child.Revision || !reflect.DeepEqual(got.Attribution, created.Child.Attribution) {
				t.Fatalf("child initial retained context differs: %+v", got.Attribution)
			}
			if !reflect.DeepEqual(created.Child.Properties.Labels, []string{"child-label", "parent-label"}) {
				t.Fatalf("parent labels were not inherited once: %+v", created.Child.Properties.Labels)
			}
			children, err := store.ListChildren(ctx, request.ParentPath)
			if err != nil || len(children) != 1 || !reflect.DeepEqual(children[0], created.Child) {
				t.Fatalf("native parent-child edge was not readable: %+v %v", children, err)
			}
			linkPath := strings.TrimPrefix(created.Link.ID, options.Binding.ScopeURL)
			link, err := store.ShowLink(ctx, linkPath)
			if err != nil || !reflect.DeepEqual(link, created.Link) {
				t.Fatalf("canonical parent-child Link mismatch: %+v %v", link, err)
			}
			var nativeVersions, graphVersions, dependencies int
			for _, query := range []struct {
				text string
				out  *int
			}{
				{`SELECT COUNT(*) FROM issue_versions WHERE issue_id=?`, &nativeVersions},
				{`SELECT COUNT(*) FROM graph_preview_issue_versions WHERE issue_id=?`, &graphVersions},
				{`SELECT COUNT(*) FROM dependencies WHERE issue_id=?`, &dependencies},
			} {
				if err := store.db.QueryRowContext(ctx, query.text, created.Child.Properties.ID).Scan(query.out); err != nil {
					t.Fatal(err)
				}
			}
			if nativeVersions != 1 || graphVersions != 1 || dependencies != 1 {
				t.Fatalf("native/graph History=%d/%d, Dependencies=%d", nativeVersions, graphVersions, dependencies)
			}
		})
	}
}

func TestCreateChildIssueRejectsReservedPublicIDWithoutEffects(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			store, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			parentRequest := plainIssue("Parent")
			parentRequest.Issue.ID = "beads/exp-parent"
			parentRequest.ForceIDPrefix = true
			parent, err := store.CreateIssue(ctx, "beads/parent", parentRequest)
			if err != nil {
				t.Fatal(err)
			}
			childID := parent.Properties.ID + ".1"
			if _, err := store.Create(ctx, CreateRequest{Path: childID, Title: "Memory with child ID"}); err != nil {
				t.Fatal(err)
			}
			var reservedPath, reservedBacking string
			if err := store.db.QueryRowContext(ctx, `SELECT path,backing FROM graph_preview_catalog WHERE path=?`, childID).Scan(&reservedPath, &reservedBacking); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, store)
			var counters int
			if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM child_counters`).Scan(&counters); err != nil {
				t.Fatal(err)
			}
			created, err := store.CreateChildIssue(ctx, IssueChildCreateRequest{
				ParentPath: "beads/parent", ExpectedParentRevision: parent.Revision,
				Create: publicops.CreateRequest{Actor: "child-author", ForceIDPrefix: true, Issue: &types.Issue{
					Title: "Rejected child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask,
				}},
			})
			if !errors.Is(err, ErrAlreadyExists) || !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
				t.Fatalf("child reused Memory public ID %q (%s %s) or changed state: created=%+v err=%v", childID, reservedPath, reservedBacking, created, err)
			}
			var afterCounters int
			if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM child_counters`).Scan(&afterCounters); err != nil || afterCounters != counters {
				t.Fatalf("refused child burned a counter: before=%d after=%d err=%v", counters, afterCounters, err)
			}
		})
	}
}

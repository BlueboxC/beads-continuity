//go:build cgo

package graphstore

import (
	"errors"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestGraphEpicProgressAndEligibleClose(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, store := issueListFixture(t, backend)
			epicRequest := plainIssue("Release epic")
			epicRequest.Issue.IssueType = types.TypeEpic
			epic, err := store.CreateIssue(ctx, "beads/epic", epicRequest)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateIssue(ctx, "beads/child", plainIssue("Child work")); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateIssue(ctx, "beads/lookalike", plainIssue("Dotted lookalike")); err != nil {
				t.Fatal(err)
			}
			parentType, ok := RelationshipDependencyTypeURL(store.ScopeURL(), types.DepParentChild)
			if !ok {
				t.Fatal("parent-child Type unavailable")
			}
			if _, err := store.AddDependency(ctx, DependencyRequest{Path: "links/epic-child", Type: types.DepParentChild, TypeURL: parentType,
				SourcePath: "beads/child", TargetPath: "beads/epic", Actor: "author"}); err != nil {
				t.Fatal(err)
			}
			statuses, err := store.EpicStatuses(ctx)
			if err != nil || len(statuses) != 1 || statuses[0].Epic.ID != epic.Properties.ID ||
				statuses[0].TotalChildren != 1 || statuses[0].ClosedChildren != 0 || statuses[0].EligibleForClose {
				t.Fatalf("initial epic status: %+v %v", statuses, err)
			}
			if _, err := store.CloseEligibleEpic(ctx, EpicCloseRequest{Path: "beads/epic", ExpectedRevision: epic.Revision,
				Reason: "All children completed", Actor: "system"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("open child permitted epic close: %v", err)
			}
			if _, err := store.CloseIssue(ctx, "beads/child", "Done", "closer"); err != nil {
				t.Fatal(err)
			}
			statuses, err = store.EpicStatuses(ctx)
			if err != nil || len(statuses) != 1 || statuses[0].TotalChildren != 1 ||
				statuses[0].ClosedChildren != 1 || !statuses[0].EligibleForClose {
				t.Fatalf("completed epic status: %+v %v", statuses, err)
			}
			current, err := store.ShowIssue(ctx, "beads/epic")
			if err != nil {
				t.Fatal(err)
			}
			request := EpicCloseRequest{Path: "beads/epic", ExpectedRevision: current.Revision,
				Reason: "All children completed", Actor: "system"}
			before := workflowState(t, ctx, store)
			fault := errors.New("epic close fault")
			for _, stage := range []string{"epic-close-native", "epic-close-retained"} {
				store.afterWrite = func(at string) error {
					if at == stage {
						return fault
					}
					return nil
				}
				_, err := store.CloseEligibleEpic(ctx, request)
				store.afterWrite = nil
				if !errors.Is(err, fault) || !reflect.DeepEqual(workflowState(t, ctx, store), before) {
					t.Fatalf("fault at %s left effects: %v", stage, err)
				}
			}
			closed, err := store.CloseEligibleEpic(ctx, request)
			if err != nil || !closed.Changed || closed.Issue.Properties.Status != types.StatusClosed ||
				closed.Issue.Properties.CloseReason != request.Reason {
				t.Fatalf("eligible epic close: %+v %v", closed, err)
			}
			statuses, err = store.EpicStatuses(ctx)
			if err != nil || len(statuses) != 0 {
				t.Fatalf("closed epic still open: %+v %v", statuses, err)
			}
		})
	}
}

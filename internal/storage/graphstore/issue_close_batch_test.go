//go:build cgo

package graphstore

import (
	"errors"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// The graph adapter does not turn a native batch no-op into a coordination,
// lease, event, History or retained-version write.
func TestGraphIssueCloseBatchClaimNextNoop(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
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
			if _, err := s.CreateIssue(ctx, "beads/first", plainIssue("Close first")); err != nil {
				t.Fatal(err)
			}
			candidate := plainIssue("Claim next")
			candidate.Issue.Priority = 0
			if _, err := s.CreateIssue(ctx, "beads/next", candidate); err != nil {
				t.Fatal(err)
			}
			request := IssueCloseBatchRequest{Paths: []string{"beads/first"}, Reasons: []string{"done"}, Actor: "fixture", ClaimNext: true}
			first, err := s.CloseIssues(ctx, request)
			if err != nil || len(first.Outcomes) != 1 || !first.Outcomes[0].Changed || first.ClaimedNext == nil ||
				first.ClaimedNext.Properties.Status != types.StatusInProgress || first.ClaimedNext.Properties.Assignee != "fixture" {
				t.Fatalf("close and claim did not land together: %+v %v", first, err)
			}
			before := reopenState(t, ctx, s)
			again, err := s.CloseIssues(ctx, request)
			if err != nil || len(again.Outcomes) != 1 || again.Outcomes[0].Changed || again.ClaimedNext != nil {
				t.Fatalf("idempotent retry claimed or changed work: %+v %v", again, err)
			}
			if after := reopenState(t, ctx, s); !reflect.DeepEqual(after, before) {
				t.Fatal("all-closed retry changed current authority, lease, History or retained state")
			}
			refused, err := s.CloseIssues(ctx, IssueCloseBatchRequest{Paths: []string{"beads/missing"}, Reasons: []string{"done"}, Actor: "fixture", ClaimNext: true})
			if err != nil || len(refused.Outcomes) != 1 || refused.Outcomes[0].Err == nil || refused.ClaimedNext != nil {
				t.Fatalf("missing-only batch changed or claimed work: %+v %v", refused, err)
			}
			if after := reopenState(t, ctx, s); !reflect.DeepEqual(after, before) {
				t.Fatal("all-refused batch changed current authority, lease, History or retained state")
			}
			if _, err := s.CreateIssue(ctx, "beads/preflight", plainIssue("Preflight target")); err != nil {
				t.Fatal(err)
			}
			beforeMissing := reopenState(t, ctx, s)
			_, err = s.CloseIssues(ctx, IssueCloseBatchRequest{
				Paths: []string{"beads/preflight", "beads/missing"}, Reasons: []string{"done", "done"},
				Actor: "fixture", ClaimNext: true, RequireAllPaths: true,
			})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing selector did not abort checked batch: %v", err)
			}
			if after := reopenState(t, ctx, s); !reflect.DeepEqual(after, beforeMissing) {
				t.Fatal("missing selector changed a sibling Issue, claim, History or retained state")
			}
			if _, err := s.CreateIssue(ctx, "beads/duplicate", plainIssue("Duplicate batch target")); err != nil {
				t.Fatal(err)
			}
			duplicated, err := s.CloseIssues(ctx, IssueCloseBatchRequest{
				Paths: []string{"beads/duplicate", "beads/duplicate"}, Reasons: []string{"first", "ignored"}, Actor: "fixture",
			})
			if err != nil || len(duplicated.Outcomes) != 2 || !duplicated.Outcomes[0].Changed || duplicated.Outcomes[1].Changed ||
				duplicated.Outcomes[0].Issue.Properties.Status != types.StatusClosed ||
				!reflect.DeepEqual(duplicated.Outcomes[0].Issue, duplicated.Outcomes[1].Issue) ||
				duplicated.Outcomes[1].Issue.Properties.CloseReason != "first" {
				t.Fatalf("duplicate batch item did not report the first close post-state: %+v %v", duplicated, err)
			}
		})
	}
}

func TestGraphIssueCloseBatchClaimNextRollback(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
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
			if _, err := s.CreateIssue(ctx, "beads/first", plainIssue("Close first")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateIssue(ctx, "beads/next", plainIssue("Claim next")); err != nil {
				t.Fatal(err)
			}
			before := reopenState(t, ctx, s)
			injected := errors.New("injected before close-and-claim commit")
			s.afterWrite = func(stage string) error {
				if stage == "issue-close-batch" {
					return injected
				}
				return nil
			}
			_, err = s.CloseIssues(ctx, IssueCloseBatchRequest{Paths: []string{"beads/first"}, Reasons: []string{"done"}, Actor: "fixture", ClaimNext: true})
			if !errors.Is(err, injected) {
				t.Fatalf("close-and-claim did not expose commit refusal: %v", err)
			}
			if after := reopenState(t, ctx, s); !reflect.DeepEqual(after, before) {
				t.Fatal("failed close-and-claim left an Issue, lease, History or graph version behind")
			}
		})
	}
}

func TestGraphIssueCloseBatchCheckedSelectorsKeepPerItemPolicy(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			assigned := plainIssue("Assigned elsewhere")
			assigned.Issue.Assignee = "other-actor"
			if _, err := s.CreateIssue(ctx, "beads/assigned", assigned); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateIssue(ctx, "beads/free", plainIssue("Free to close")); err != nil {
				t.Fatal(err)
			}
			result, err := s.CloseIssues(ctx, IssueCloseBatchRequest{
				Paths: []string{"beads/assigned", "beads/free"}, Reasons: []string{"done", "done"},
				Actor: "fixture", RequireAllPaths: true,
			})
			if err != nil || len(result.Outcomes) != 2 || result.Outcomes[0].Err == nil ||
				result.Outcomes[1].Err != nil || !result.Outcomes[1].Changed {
				t.Fatalf("checked selectors changed per-item policy: %+v %v", result, err)
			}
			stillOpen, err := s.ShowIssue(ctx, "beads/assigned")
			if err != nil || stillOpen.Properties.Status != types.StatusOpen {
				t.Fatalf("policy-refused Issue changed: %+v %v", stillOpen, err)
			}
		})
	}
}

package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// CheckReclaimOwnership seeds stale node-local leases as can be
// retained by an import or interrupted ownership change. Only a lease whose
// holder still owns the live claim may release that claim.
func CheckReclaimOwnership(t *testing.T, ctx context.Context, f HeartbeaterFixture) {
	t.Helper()
	for _, tc := range []struct {
		name     string
		assignee string
		holder   string
		reclaim  bool
	}{
		{"different-owner", "bob", "alice", false},
		{"unassigned", "", "alice", false},
		{"different-case-owner", "ALICE", "alice", false},
		{"matching-owner", "alice", "alice", true},
		{"matching-canonical-owner", "worker--alice", "worker/alice", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := f.IssuePrefix + "-" + tc.name
			issue := &types.Issue{ID: id, Title: tc.name, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
			if err := f.CreateIssue(ctx, issue, "seed"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.Claimer.Claim(ctx, publicops.ClaimRequest{IssueID: id, Actor: tc.holder}); err != nil {
				t.Fatal(err)
			}
			if err := f.Exec(ctx, []SQLStatement{
				{Query: "UPDATE issues SET assignee = ? WHERE id = ?", Args: []any{tc.assignee, id}},
				{Query: "UPDATE leases SET lease_expires_at = ? WHERE issue_id = ?", Args: []any{time.Now().UTC().Add(-time.Hour), id}},
			}); err != nil {
				t.Fatal(err)
			}
			var beforeVersion int64
			var beforeUpdated string
			if err := f.QueryScalar(ctx, "SELECT row_lock, CAST(updated_at AS CHAR) FROM issues WHERE id = ?", []any{id}, &beforeVersion, &beforeUpdated); err != nil {
				t.Fatal(err)
			}
			if err := f.Reclaim(ctx, id, "reaper"); err != nil {
				t.Fatal(err)
			}
			var status, assignee, updated string
			var version int64
			if err := f.QueryScalar(ctx, "SELECT status, COALESCE(assignee, ''), row_lock, CAST(updated_at AS CHAR) FROM issues WHERE id = ?", []any{id}, &status, &assignee, &version, &updated); err != nil {
				t.Fatal(err)
			}
			if tc.reclaim {
				if status != string(types.StatusOpen) || assignee != "" || version == beforeVersion {
					t.Fatalf("matching expired claim not reclaimed: status=%s assignee=%q row_lock=%d", status, assignee, version)
				}
			} else if status != string(types.StatusInProgress) || assignee != tc.assignee || version != beforeVersion || updated != beforeUpdated {
				t.Fatalf("unrelated claim changed: status=%s assignee=%q row_lock=%d updated=%s", status, assignee, version, updated)
			}
			var events int
			if err := f.QueryScalar(ctx, "SELECT COUNT(*) FROM events WHERE issue_id = ? AND event_type = 'lease_reclaimed'", []any{id}, &events); err != nil {
				t.Fatal(err)
			}
			wantEvents := 0
			if tc.reclaim {
				wantEvents = 1
			}
			if events != wantEvents {
				t.Fatalf("reclaim events=%d want=%d", events, wantEvents)
			}
		})
	}
}

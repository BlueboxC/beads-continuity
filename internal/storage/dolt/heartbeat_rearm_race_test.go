package dolt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
)

func TestHeartbeatRearmRacingClaimEndLeavesNoOrphan(t *testing.T) {
	base, cleanup := setupReplayTestStore(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	proxy := startSQLWireProxy(t)
	via := openStoreOnPort(t, ctx, base, proxy.port())
	heartbeater, err := via.Heartbeater()
	if err != nil {
		t.Fatal(err)
	}
	for _, end := range []string{"release", "close"} {
		for round := range 3 {
			id := fmt.Sprintf("test-rearm-%s-%d", end, round)
			if err := base.CreateIssue(ctx, &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}, "seed"); err != nil {
				t.Fatal(err)
			}
			if err := base.UpdateIssue(ctx, id, map[string]interface{}{"status": "in_progress", "assignee": "worker"}, "worker"); err != nil {
				t.Fatal(err)
			}
			var initial int
			if err := base.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM leases WHERE issue_id = ?", id).Scan(&initial); err != nil || initial != 0 {
				t.Fatalf("fixture has a lease: %d, %v", initial, err)
			}
			held, release := proxy.holdNext(func(q string) bool { return strings.Contains(strings.ToLower(q), "update issues set row_lock") })
			done := make(chan error, 1)
			go func() {
				_, err := heartbeater.Heartbeat(ctx, issueops.HeartbeatRequest{Actor: "worker", IssueID: id})
				done <- err
			}()
			select {
			case <-held:
			case err := <-done:
				t.Fatalf("heartbeat did not reach the re-arm fence: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var endErr error
			if end == "close" {
				endErr = base.CloseIssue(ctx, id, "finished", "worker", "")
			} else {
				endErr = base.UnclaimIssue(ctx, id, "worker", false)
			}
			release()
			if endErr != nil {
				t.Fatal(endErr)
			}
			select {
			case err := <-done:
				if !errors.Is(err, storage.ErrNotClaimable) {
					t.Fatalf("heartbeat after %s: %v", end, err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var remaining int
			if err := base.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM leases WHERE issue_id = ?", id).Scan(&remaining); err != nil {
				t.Fatal(err)
			}
			if remaining != 0 {
				t.Fatalf("%s left %d orphaned leases", end, remaining)
			}
		}
	}
}

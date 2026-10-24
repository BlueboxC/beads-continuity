//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

func TestGraphIssueHeartbeatAndReclaimNativeHistory(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, s, original, _, _ := reopenFixture(t, backend)
			claimed, err := s.ClaimIssue(ctx, "beads/work", "holder")
			if err != nil || !claimed.Changed {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			assertIssueEditCounts(t, ctx, s, original.Properties.ID, 3)
			before := reopenState(t, ctx, s)
			if _, err := s.HeartbeatIssue(ctx, "beads/work", "stranger"); !errors.Is(err, storage.ErrAlreadyClaimed) || !reflect.DeepEqual(before, reopenState(t, ctx, s)) {
				t.Fatalf("foreign heartbeat changed state: %v", err)
			}
			renewed, err := s.HeartbeatIssue(issueops.WithLeaseTTL(ctx, 10*time.Minute), "beads/work", "holder")
			if err != nil || renewed.Revision != claimed.Issue.Revision || renewed.Properties.LeaseExpiresAt == nil ||
				renewed.Properties.LeaseExpiresAt.Sub(*renewed.Properties.HeartbeatAt) != 10*time.Minute {
				t.Fatalf("heartbeat: %+v %v", renewed, err)
			}
			assertIssueEditCounts(t, ctx, s, original.Properties.ID, 3)
			assertIssueEditVersion(t, ctx, s, "beads/work", claimed.Issue)
			current, err := s.ShowIssue(ctx, "beads/work")
			if err != nil || !reflect.DeepEqual(current, renewed) {
				t.Fatalf("current lease after heartbeat: %+v %v", current, err)
			}
			// A scoped sweep cannot touch a live lease or unrelated Issue.
			none, err := s.ReclaimExpiredIssueLeases(ctx, 0, types.ReclaimFilter{IDs: []string{original.Properties.ID}}, "reaper")
			if err != nil || len(none) != 0 || !reflect.DeepEqual(current, mustShowLeaseIssue(t, ctx, s, "beads/work")) {
				t.Fatalf("live lease reclaimed: %+v %v", none, err)
			}
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE leases SET lease_expires_at=? WHERE issue_id=?`, time.Now().UTC().Add(-time.Hour), original.Properties.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			reclaimed, err := s.ReclaimExpiredIssueLeases(ctx, 0, types.ReclaimFilter{IDs: []string{original.Properties.ID}}, "reaper")
			if err != nil || len(reclaimed) != 1 || reclaimed[0].PreviousOwner != "holder" || reclaimed[0].ID != original.Properties.ID {
				t.Fatalf("reclaim: %+v %v", reclaimed, err)
			}
			after := mustShowLeaseIssue(t, ctx, s, "beads/work")
			if after.Revision == renewed.Revision || after.Properties.Status != types.StatusOpen || after.Properties.Assignee != "" || after.Properties.LeaseExpiresAt != nil {
				t.Fatalf("reclaim did not release Issue: %+v", after)
			}
			assertIssueEditCounts(t, ctx, s, original.Properties.ID, 4)
			assertIssueEditVersion(t, ctx, s, "beads/work", after)
			state := reopenState(t, ctx, s)
			none, err = s.ReclaimExpiredIssueLeases(ctx, 0, types.ReclaimFilter{}, "reaper")
			if err != nil || len(none) != 0 || !reflect.DeepEqual(state, reopenState(t, ctx, s)) {
				t.Fatalf("repeat reclaim changed state: %+v %v", none, err)
			}
			if _, err := s.HeartbeatIssue(ctx, "beads/work", "holder"); !errors.Is(err, storage.ErrNotClaimable) || !reflect.DeepEqual(state, reopenState(t, ctx, s)) {
				t.Fatalf("reclaimed holder renewed lease: %v", err)
			}
		})
	}
}

func TestGraphIssueMissingNodeLocalLeaseRemainsReadableAndHeartbeatRearms(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, s, original, _, _ := reopenFixture(t, backend)
			claimed, err := s.ClaimIssue(ctx, "beads/work", "holder")
			if err != nil || !claimed.Changed {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			assertIssueEditCounts(t, ctx, s, original.Properties.ID, 3)
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `DELETE FROM leases WHERE issue_id=?`, original.Properties.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			// Another replica has the durable claim but not the granting node's
			// ignored lease row. Its graph read still validates retained History.
			remote, err := s.ShowIssue(ctx, "beads/work")
			if err != nil || remote.Revision != claimed.Issue.Revision || remote.Properties.Status != types.StatusInProgress ||
				remote.Properties.Assignee != "holder" || remote.Properties.LeaseExpiresAt != nil || remote.Properties.HeartbeatAt != nil {
				t.Fatalf("remote claim read: %+v %v", remote, err)
			}
			assertIssueEditVersion(t, ctx, s, "beads/work", claimed.Issue)
			var eventsBefore, journalBefore int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE issue_id=?`, original.Properties.ID).Scan(&eventsBefore); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bd_events_journal WHERE issue_id=?`, original.Properties.ID).Scan(&journalBefore); err != nil {
				t.Fatal(err)
			}
			before := reopenState(t, ctx, s)
			if _, err := s.HeartbeatIssue(ctx, "beads/work", "stranger"); !errors.Is(err, storage.ErrAlreadyClaimed) || !reflect.DeepEqual(before, reopenState(t, ctx, s)) {
				t.Fatalf("foreign rearm changed state: %v", err)
			}
			rearmed, err := s.HeartbeatIssue(ctx, "beads/work", "holder")
			if err != nil || rearmed.Revision == claimed.Issue.Revision || rearmed.Properties.LeaseExpiresAt == nil || rearmed.Properties.HeartbeatAt == nil {
				t.Fatalf("holder rearm: %+v %v", rearmed, err)
			}
			assertIssueEditCounts(t, ctx, s, original.Properties.ID, 4)
			assertIssueEditVersion(t, ctx, s, "beads/work", claimed.Issue)
			assertIssueEditVersion(t, ctx, s, "beads/work", rearmed)
			var eventsAfter, journalAfter int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE issue_id=?`, original.Properties.ID).Scan(&eventsAfter); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bd_events_journal WHERE issue_id=?`, original.Properties.ID).Scan(&journalAfter); err != nil {
				t.Fatal(err)
			}
			// This graph workspace does not enable the optional JSONL journal,
			// but the native audit event and both retained histories advance.
			if eventsAfter != eventsBefore+1 || journalAfter != journalBefore {
				t.Fatalf("lease rearm audit: events %d->%d journal %d->%d", eventsBefore, eventsAfter, journalBefore, journalAfter)
			}
			var rearmEvents int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE issue_id=? AND event_type=? AND actor=?`,
				original.Properties.ID, types.EventLeaseRearmed, "holder").Scan(&rearmEvents); err != nil || rearmEvents != 1 {
				t.Fatalf("lease rearm event=%d: %v", rearmEvents, err)
			}
			refreshed, err := s.HeartbeatIssue(ctx, "beads/work", "holder")
			if err != nil || refreshed.Revision != rearmed.Revision {
				t.Fatalf("holder refresh after rearm: %+v %v", refreshed, err)
			}
			assertIssueEditCounts(t, ctx, s, original.Properties.ID, 4)
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE issue_id=?`, original.Properties.ID).Scan(&eventsAfter); err != nil || eventsAfter != eventsBefore+1 {
				t.Fatalf("refresh audit events=%d: %v", eventsAfter, err)
			}
		})
	}
}

func mustShowLeaseIssue(t *testing.T, ctx context.Context, s *Store, path string) IssueRecord {
	t.Helper()
	issue, err := s.ShowIssue(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return issue
}

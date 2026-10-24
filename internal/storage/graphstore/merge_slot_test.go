//go:build cgo

package graphstore

import (
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestGraphMergeSlotLifecycleAndHistory(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, s := createFieldsStore(t, backend)
			created, err := s.MergeSlotCreate(ctx, "creator")
			if err != nil || created.Properties.Status != types.StatusOpen || !hasGraphSlotLabel(created.Properties.Labels) {
				t.Fatalf("create: %+v %v", created, err)
			}
			assertIssueEditCounts(t, ctx, s, created.Properties.ID, 1)
			initial := createFieldsState(t, ctx, s)
			repeated, err := s.MergeSlotCreate(ctx, "creator")
			if err != nil || repeated.Revision != created.Revision || !reflect.DeepEqual(initial, createFieldsState(t, ctx, s)) {
				t.Fatalf("repeat create changed state: %+v %v", repeated, err)
			}
			available, err := s.MergeSlotCheck(ctx)
			if err != nil || !available.Available || available.SlotID != created.Properties.ID {
				t.Fatalf("check available: %+v %v", available, err)
			}
			acquired, changed, err := s.MergeSlotAcquire(ctx, "agent-a", "actor", false)
			if err != nil || !changed || !acquired.Acquired || acquired.Holder != "agent-a" {
				t.Fatalf("acquire: %+v %v", acquired, err)
			}
			assertIssueEditCounts(t, ctx, s, created.Properties.ID, 2)
			held := createFieldsState(t, ctx, s)
			denied, changed, err := s.MergeSlotAcquire(ctx, "agent-b", "actor", false)
			if err != nil || changed || denied.Acquired || denied.Holder != "agent-a" || !reflect.DeepEqual(held, createFieldsState(t, ctx, s)) {
				t.Fatalf("held attempt changed state: %+v %v", denied, err)
			}
			waiting, changed, err := s.MergeSlotAcquire(ctx, "agent-b", "actor", true)
			if err != nil || !changed || !waiting.Waiting || waiting.Position != 1 {
				t.Fatalf("wait: %+v %v", waiting, err)
			}
			assertIssueEditCounts(t, ctx, s, created.Properties.ID, 3)
			queued := createFieldsState(t, ctx, s)
			waiting, changed, err = s.MergeSlotAcquire(ctx, "agent-b", "actor", true)
			if err != nil || changed || !waiting.Waiting || waiting.Position != 1 || !reflect.DeepEqual(queued, createFieldsState(t, ctx, s)) {
				t.Fatalf("repeat wait changed state: %+v %v", waiting, err)
			}
			if _, _, err := s.MergeSlotRelease(ctx, "agent-b", "actor"); err == nil || !reflect.DeepEqual(queued, createFieldsState(t, ctx, s)) {
				t.Fatalf("foreign release changed state: %v", err)
			}
			id, changed, err := s.MergeSlotRelease(ctx, "agent-a", "actor")
			if err != nil || !changed || id != created.Properties.ID {
				t.Fatalf("release: %q %t %v", id, changed, err)
			}
			assertIssueEditCounts(t, ctx, s, created.Properties.ID, 4)
			var createdEvents, labelEvents, statusEvents, updateEvents int
			if err := s.db.QueryRowContext(ctx, `SELECT
				SUM(event_type='created'), SUM(event_type='label_added'),
				SUM(event_type='status_changed'), SUM(event_type='updated')
				FROM events WHERE issue_id=?`, created.Properties.ID).Scan(&createdEvents, &labelEvents, &statusEvents, &updateEvents); err != nil ||
				createdEvents != 1 || labelEvents != 1 || statusEvents != 2 || updateEvents != 1 {
				t.Fatalf("native merge-slot events created=%d labels=%d status=%d updated=%d: %v", createdEvents, labelEvents, statusEvents, updateEvents, err)
			}
			available, err = s.MergeSlotCheck(ctx)
			if err != nil || !available.Available || available.Holder != "" || !reflect.DeepEqual(available.Waiters, []string{"agent-b"}) {
				t.Fatalf("check released: %+v %v", available, err)
			}
			released := createFieldsState(t, ctx, s)
			_, changed, err = s.MergeSlotRelease(ctx, "", "actor")
			if err != nil || changed || !reflect.DeepEqual(released, createFieldsState(t, ctx, s)) {
				t.Fatalf("repeat release changed state: %v", err)
			}
		})
	}
}

func TestGraphMergeSlotRefusesOccupiedIdentityAndInvalidActor(t *testing.T) {
	ctx, s := createFieldsStore(t, "embedded")
	path, err := s.mergeSlotPath(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := plainIssue("Not a slot")
	request.Issue.ID = path[len("beads/"):]
	occupied, err := s.CreateIssue(ctx, path, request)
	if err != nil {
		t.Fatal(err)
	}
	before := createFieldsState(t, ctx, s)
	if _, err := s.MergeSlotCreate(ctx, "creator"); err == nil {
		t.Fatal("occupied slot ID accepted")
	}
	if _, _, err := s.MergeSlotAcquire(ctx, "", "actor", false); err == nil {
		t.Fatal("empty holder accepted")
	}
	if _, _, err := s.MergeSlotRelease(ctx, "", ""); err == nil {
		t.Fatal("empty actor accepted")
	}
	if !reflect.DeepEqual(before, createFieldsState(t, ctx, s)) {
		t.Fatal("invalid merge-slot operation changed storage")
	}
	assertIssueEditCounts(t, ctx, s, occupied.Properties.ID, 1)
}

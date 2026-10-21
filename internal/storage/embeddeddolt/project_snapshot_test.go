//go:build cgo

package embeddeddolt_test

import (
	"context"
	"database/sql"
	"errors"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/memoryops"
	"testing"
)

func TestProjectSnapshotReadonlyLifetimeAndPreservation(t *testing.T) {
	fixture := newPristineEmbeddedDoltFixture(t, "projectsnapshot")
	ctx := t.Context()
	for _, id := range []string{"parent", "child"} {
		if err := fixture.store.CreateIssue(ctx, &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.store.AddDependency(ctx, &types.Dependency{IssueID: "child", DependsOnID: "parent", Type: types.DepBlocks}, "fixture"); err != nil {
		t.Fatal(err)
	}
	m, err := fixture.store.Memories()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Remember(ctx, memoryops.RememberRequest{Key: "human", Content: "keep solution"}); err != nil {
		t.Fatal(err)
	}
	if err = fixture.store.Commit(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	before, err := fixture.store.GetCurrentCommit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := embeddeddolt.OpenReadOnly(ctx, fixture.beadsDir, fixture.database, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	var borrowed storage.ProjectReader
	err = ro.ReadProjectSnapshot(ctx, func(r storage.ProjectReader) error {
		borrowed = r
		v, err := r.Recall(ctx, memoryops.RecallRequest{Key: "human"})
		if err != nil || v.Value != "keep solution" {
			t.Fatalf("memory changed: %v", err)
		}
		issues, err := r.SearchIssues(ctx, "", types.IssueFilter{})
		if err != nil || len(issues) != 2 {
			t.Fatalf("task selection changed: %v", err)
		}
		deps, err := r.GetDependencyRecordsForIssues(ctx, []string{"parent", "child"})
		if err != nil || len(deps["child"]) != 1 || deps["child"][0].DependsOnID != "parent" {
			t.Fatalf("relations changed: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = borrowed.SearchIssues(ctx, "", types.IssueFilter{}); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal("reader outlived transaction", err)
	}
	sentinel := errors.New("stop")
	if err = ro.ReadProjectSnapshot(ctx, func(storage.ProjectReader) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = ro.ReadProjectSnapshot(canceled, func(storage.ProjectReader) error { t.Fatal("canceled callback ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = ro.ReadProjectSnapshot(ctx, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	after, err := ro.GetCurrentCommit(ctx)
	if err != nil || after != before {
		t.Fatal("readonly snapshot changed history", err)
	}
}

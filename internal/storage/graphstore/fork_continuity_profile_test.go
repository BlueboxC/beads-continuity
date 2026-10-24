//go:build cgo

package graphstore

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/memoryops"
)

func TestSelectedContinuitySnapshotLifetimeAndBudgets(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s := openVersionsStore(t, ctx, o)
	m := s.Memories("fixture")
	if _, err := m.Remember(ctx, memoryops.RememberRequest{Key: "human%_", Content: "preserved"}); err != nil {
		t.Fatal(err)
	}
	// Simulate an out-of-band oversized historical payload; normal config rows
	// have a smaller SQL TEXT bound. Selected reads must refuse before transfer.
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE config MODIFY COLUMN value LONGBLOB"); err != nil {
		t.Fatalf("fixture DDL: %.180s", err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES ('kv.memory.@knowledge/code/obsolete',?)", strings.Repeat("x", PreviewContinuityReadByteLimit+1)); err != nil {
		t.Fatal(err)
	}
	var escaped memoryops.MemoryReader
	if err := m.ReadSnapshot(ctx, func(r memoryops.MemoryReader) error {
		escaped = r
		got, err := r.List(ctx, memoryops.ListRequest{KeyPrefix: "human%_"})
		if err != nil || got.Memories["human%_"] != "preserved" {
			t.Fatalf("selected read: %v %v", got, err)
		}
		_, err = r.List(ctx, memoryops.ListRequest{})
		if !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("unbounded plane admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.Recall(ctx, memoryops.RecallRequest{Key: "human%_"}); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("borrow escaped: %v", err)
	}
	if _, err := m.Remember(ctx, memoryops.RememberRequest{Key: "growth", Content: "must refuse"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("oversized plane grew: %v", err)
	}
	if got, err := m.Recall(ctx, memoryops.RecallRequest{Key: "human%_"}); err != nil || got.Value != "preserved" {
		t.Fatalf("refusal changed knowledge: %v", err)
	}
}

func TestNativeDependencyThreadSurvivesRetainedRecovery(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s := openVersionsStore(t, ctx, o)
	a, err := s.CreateIssue(ctx, "beads/thread-source", plainIssue("source"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateIssue(ctx, "beads/thread-target", plainIssue("target"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.AddDependency(ctx, DependencyRequest{Path: "links/thread", SourcePath: "beads/thread-source", TargetPath: "beads/thread-target", Actor: "fixture", ThreadID: "thread-original", ExpectedSourceRevision: a.Revision})
	if err != nil {
		t.Fatal(err)
	}
	before := created.Link
	_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/thread", Properties: map[string]any{"dep_type": "blocks", "threadId": "changed"}, ExpectedRevision: before.Revision, ExpectedSourceRevision: created.Source.Revision, Actor: "fixture"})
	if err == nil {
		t.Fatal("immutable thread changed")
	}
	removed, err := s.RemoveDependency(ctx, DependencyRemoveRequest{SourcePath: "beads/thread-source", TargetPath: "beads/thread-target", Actor: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/thread", FromRevision: before.Revision, Properties: map[string]any{"dep_type": "blocks"}, ExpectedRevision: removed.Link.Revision, ExpectedSourceRevision: removed.Source.Revision, Actor: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Link.Properties["nativeId"] != before.Properties["nativeId"] || restored.Link.Properties["threadId"] != "thread-original" {
		t.Fatalf("facts lost: %+v", restored.Link)
	}
}

func TestSelectedDerivedRecallChargesBeforeTransfer(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s := openVersionsStore(t, ctx, o)
	m := s.Memories("fixture")
	const key = "@knowledge/code/manifest"
	if _, err := m.Remember(ctx, memoryops.RememberRequest{Key: key, Content: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := m.ReadSnapshot(ctx, func(reader memoryops.MemoryReader) error {
		got, err := reader.Recall(ctx, memoryops.RecallRequest{Key: key})
		if err != nil || !got.Found || got.Value != "fixture" {
			t.Fatalf("selected blob: %v", err)
		}
		r := reader.(*continuityReader)
		before := r.bytes
		r.bytes = PreviewContinuityReadByteLimit - 1
		got, err = reader.Recall(ctx, memoryops.RecallRequest{Key: key})
		if !errors.Is(err, ErrLimitExceeded) || got.Value != "" || r.bytes != PreviewContinuityReadByteLimit-1 {
			t.Fatalf("unbudgeted transfer: %v", err)
		}
		r.bytes = before
		r.rows = PreviewContinuityReadRowLimit
		if _, err = reader.Recall(ctx, memoryops.RecallRequest{Key: key}); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("unbudgeted row: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

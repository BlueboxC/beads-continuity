//go:build cgo

package graphstore

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/steveyegge/beads/memoryops"
)

func TestContinuityOperationalProfileRetainedGrowthRollbackAndReopen(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// Model retained derived generations imported from an older workspace. Raw
	// seeding is fixture-only; the public writer must still refuse ordinary growth.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("x", 60000)
	for i := 0; i < 1200; i++ {
		if _, err := tx.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES (?,?)", fmt.Sprintf("kv.memory.@knowledge/code/old/%d", i), body); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Memories("").Remember(ctx, memoryops.RememberRequest{Key: "@knowledge/code/refused", Content: "refused"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ordinary growth %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	o.OperationalProfile = ContinuityProfile
	s, err = OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		if _, err := s.Create(ctx, CreateRequest{Path: fmt.Sprintf("beads/large%d", i), Title: "large", Body: strings.Repeat("y", 1<<20)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CurrentSnapshot(ctx); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ordinary snapshot changed: %v", err)
	}
	if _, err := s.OperationalSnapshot(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := s.Create(ctx, CreateRequest{Path: "beads/new", Title: "new"})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("rollback control")
	s.afterWrite = func(string) error { return injected }
	if _, err := s.Create(ctx, CreateRequest{Path: "beads/rolled-back", Title: "rolled back"}); !errors.Is(err, injected) {
		t.Fatalf("injected failure %v", err)
	}
	s.afterWrite = nil
	if _, err := s.Read(ctx, "beads/rolled-back"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed create became visible: %v", err)
	}
	content := "edited"
	edited, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/new", Body: &content, ExpectedRevision: created.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/new", ExpectedRevision: edited.Memory.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Memories("").Remember(ctx, memoryops.RememberRequest{Key: "@knowledge/code/new", Content: "new retained generation"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadVersion(ctx, "beads/new", created.Revision); err != nil {
		t.Fatalf("original version lost: %v", err)
	}
	if got, err := s.Memories("").Recall(ctx, memoryops.RecallRequest{Key: "@knowledge/code/old/0"}); err != nil || got.Value != body {
		t.Fatalf("retained data changed: %v", err)
	}
	if _, err := s.Memories("").List(ctx, memoryops.ListRequest{}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("profile silently enlarged full-plane reads: %v", err)
	}
}

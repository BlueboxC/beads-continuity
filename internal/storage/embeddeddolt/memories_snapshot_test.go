//go:build cgo

package embeddeddolt_test

import (
	"database/sql"
	"errors"
	"github.com/steveyegge/beads/backend/conformance"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/memoryops"
	"testing"
)

func TestMemorySnapshotReadonlyLifetimeFailureAndPreservation(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	fixture := newPristineEmbeddedDoltFixture(t, "mrs")
	ctx := t.Context()
	memories, err := fixture.store.Memories()
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"human": "keep exact solution", "@knowledge/code/retained": "keep old generation"} {
		if _, err := memories.Remember(ctx, memoryops.RememberRequest{Key: k, Content: v}); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.store.Commit(ctx, "snapshot fixture"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	readonly, err := embeddeddolt.OpenReadOnly(ctx, fixture.beadsDir, fixture.database, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	m, err := readonly.Memories()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := m.(memoryops.SnapshotMemories)
	if !ok {
		t.Fatal("embedded reader lacks snapshot")
	}
	conformance.RunMemoriesSnapshotReadonlyLifetime(t, ctx, snapshot, map[string]string{"human": "keep exact solution", "@knowledge/code/retained": "keep old generation"})
	var borrowed memoryops.MemoryReader
	err = snapshot.ReadSnapshot(ctx, func(reader memoryops.MemoryReader) error {
		borrowed = reader
		selected, err := reader.List(ctx, memoryops.ListRequest{ExcludeKeyPrefix: "@knowledge/code/"})
		if err != nil || len(selected.Memories) != 1 || selected.Memories["human"] != "keep exact solution" {
			t.Fatal("snapshot selection diverged", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := borrowed.Recall(ctx, memoryops.RecallRequest{Key: "human"}); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal("native reader outlived SQL transaction", err)
	}
	original, err := m.List(ctx, memoryops.ListRequest{})
	if err != nil || len(original.Memories) != 2 || original.Memories["@knowledge/code/retained"] != "keep old generation" || original.Memories["human"] != "keep exact solution" {
		t.Fatal("snapshot mutated retained data", err)
	}
}

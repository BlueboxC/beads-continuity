package conformance

import (
	"context"
	"errors"
	"github.com/steveyegge/beads/memoryops"
	"reflect"
	"testing"
)

// RunMemoriesSnapshotReadonlyLifetime checks an advertised snapshot on a
// dedicated plane seeded with the supplied rows; it never needs mutation access.
func RunMemoriesSnapshotReadonlyLifetime(t *testing.T, ctx context.Context, snapshot memoryops.SnapshotMemories, expected map[string]string) {
	t.Helper()
	failure := errors.New("reader stops")
	var borrowed memoryops.MemoryReader
	err := snapshot.ReadSnapshot(ctx, func(reader memoryops.MemoryReader) error {
		borrowed = reader
		if _, ok := reader.(memoryops.Memories); ok {
			t.Fatal("borrowed snapshot permits mutations")
		}
		for key, value := range expected {
			got, err := reader.Recall(ctx, memoryops.RecallRequest{Key: key})
			if err != nil || !got.Found || got.Value != value {
				t.Fatal("snapshot lost exact row", key, err)
			}
		}
		got, err := reader.List(ctx, memoryops.ListRequest{})
		if err != nil || !reflect.DeepEqual(got.Memories, expected) {
			t.Fatal("snapshot enumeration changed plane", err)
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal("callback error lost", err)
	}
	if borrowed == nil {
		t.Fatal("callback did not run")
	}
	if _, err := borrowed.List(ctx, memoryops.ListRequest{}); err == nil {
		t.Fatal("reader escaped callback lifetime")
	}
	if err := snapshot.ReadSnapshot(ctx, nil); !errors.Is(err, memoryops.ErrValidation) {
		t.Fatal("nil callback accepted", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := snapshot.ReadSnapshot(canceled, func(memoryops.MemoryReader) error { t.Fatal("canceled callback ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if err := snapshot.ReadSnapshot(ctx, func(reader memoryops.MemoryReader) error {
		got, err := reader.List(ctx, memoryops.ListRequest{})
		if err != nil || !reflect.DeepEqual(got.Memories, expected) {
			t.Fatal("prior failed/canceled reads changed rows", err)
		}
		return nil
	}); err != nil {
		t.Fatal("snapshot failed after prior cancellation", err)
	}
}

//go:build cgo

package embeddeddolt_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dolthub/dolt/go/libraries/doltcore/doltdb"
	"github.com/dolthub/dolt/go/libraries/doltcore/env/actions"
	"github.com/dolthub/dolt/go/store/chunks"
	"github.com/dolthub/dolt/go/store/hash"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

type failingRootStore struct {
	chunks.ChunkStore
	err   error
	reads int
}

func (s *failingRootStore) Root(context.Context) (hash.Hash, error) {
	s.reads++
	return hash.Hash{}, s.err
}

func TestEngineBackupPropagatesSourceRootError(t *testing.T) {
	sentinel := errors.New("fixture source root unavailable")
	source := &failingRootStore{ChunkStore: (&chunks.MemoryStorage{}).NewView(), err: sentinel}
	destination := &failingRootStore{ChunkStore: (&chunks.MemoryStorage{}).NewView()}
	src, err := doltdb.DoltDBFromCS(source, "source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = src.Close() })
	dst, err := doltdb.DoltDBFromCS(destination, "destination")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	err = actions.SyncRoots(context.Background(), src, dst, t.TempDir(), actions.SyncRootsDBRelationshipUnknown, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("backup lost source error: %v", err)
	}
	if destination.reads != 0 {
		t.Fatalf("destination accessed after source failure: %d", destination.reads)
	}
}

func branchTrackerWorkers() int {
	buf := make([]byte, 4<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "(*BranchActivityTracker).processEvents(")
}

func TestEngineCloseStopsBranchTrackers(t *testing.T) {
	baseline := branchTrackerWorkers()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for range 3 {
		_, closeEngine, err := embeddeddolt.OpenSQL(ctx, t.TempDir(), "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := closeEngine(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for branchTrackerWorkers() > baseline && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := branchTrackerWorkers(); got > baseline {
		t.Fatalf("branch trackers leaked after close: before=%d after=%d", baseline, got)
	}
}

//go:build cgo

package embeddeddolt_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/activity"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/memoryops"
)

func TestExistingMemorySessionAtomicityAndLifetime(t *testing.T) {
	f := newPristineEmbeddedDoltFixture(t, "memorysession")
	ctx := t.Context()
	if err := f.store.Commit(ctx, "baseline"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	opts := embeddeddolt.MemorySessionOptions{Write: true, EventsJournalEnabled: true, CommitMessage: "fixture memory session"}
	run := func(o embeddeddolt.MemorySessionOptions, fn func(memoryops.Memories) (bool, error)) error {
		return embeddeddolt.WithExistingMemories(ctx, f.beadsDir, f.database, "main", o, fn)
	}
	var borrowed memoryops.Memories
	input := activity.Input{SessionID: "fixture-session", TurnID: "turn", CWD: "/fixture", Event: "Stop", LastAssistantMessage: "reported result"}
	event, err := activity.Build(input, "/fixture", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	err = run(opts, func(m memoryops.Memories) (bool, error) {
		borrowed = m
		if _, err := m.Remember(ctx, memoryops.RememberRequest{Key: "human", Content: "keep verified solution"}); err != nil {
			return false, err
		}
		return activity.Save(ctx, m, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = borrowed.Recall(ctx, memoryops.RecallRequest{Key: "human"}); !errors.Is(err, sql.ErrTxDone) {
		t.Fatal("role outlived transaction", err)
	}
	root := func() string {
		t.Helper()
		te := &testEnv{dataDir: f.dataDir, database: f.database}
		var h string
		te.queryScalar(t, ctx, "SELECT DOLT_HASHOF_DB('WORKING')", nil, &h)
		return h
	}
	before := root()
	later := event
	later.ObservedAt = time.Unix(200, 0).UTC().Format(time.RFC3339Nano)
	err = run(opts, func(m memoryops.Memories) (bool, error) {
		changed, err := activity.Save(ctx, m, later)
		if changed {
			t.Fatal("retry rewrote original observation")
		}
		return changed, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if root() != before {
		t.Fatal("retry changed working root")
	}
	stop := errors.New("abort fixture")
	err = run(opts, func(m memoryops.Memories) (bool, error) {
		if _, err := m.Remember(ctx, memoryops.RememberRequest{Key: "transient", Content: "rollback"}); err != nil {
			return false, err
		}
		return true, stop
	})
	if !errors.Is(err, stop) || root() != before {
		t.Fatal("callback did not roll back", err)
	}
	ro := opts
	ro.Write = false
	err = run(ro, func(m memoryops.Memories) (bool, error) {
		if _, err := m.Remember(ctx, memoryops.RememberRequest{Key: "refused", Content: "readonly"}); !errors.Is(err, embeddeddolt.ErrReadOnly) {
			t.Fatal("readonly write admitted", err)
		}
		human, err := m.Recall(ctx, memoryops.RecallRequest{Key: "human"})
		if err != nil {
			return false, err
		}
		if human.Value != "keep verified solution" {
			t.Fatal("human knowledge changed")
		}
		prior, err := m.Recall(ctx, memoryops.RecallRequest{Key: activity.Prefix + "event/" + event.ID})
		if err != nil {
			return false, err
		}
		var stored activity.Event
		if json.Unmarshal([]byte(prior.Value), &stored) != nil || stored.ObservedAt != event.ObservedAt {
			t.Fatal("original event not retained")
		}
		return false, nil
	})
	if err != nil || root() != before {
		t.Fatal("readonly session changed data", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := embeddeddolt.WithExistingMemories(canceled, f.beadsDir, f.database, "main", opts, func(memoryops.Memories) (bool, error) { t.Fatal("canceled callback ran"); return false, nil }); err == nil {
		t.Fatal("cancellation accepted")
	}
	if err := run(opts, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
}

func TestExistingMemorySessionRefusesMissingWorkspace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	called := false
	err := embeddeddolt.WithExistingMemories(t.Context(), dir, "missing", "main", embeddeddolt.MemorySessionOptions{Write: true}, func(memoryops.Memories) (bool, error) { called = true; return true, nil })
	if err == nil || called {
		t.Fatal("missing workspace admitted", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("session initialized a workspace", err)
	}
}

func TestExistingMemorySessionRefusesSchemaDrift(t *testing.T) {
	for _, future := range []bool{false, true} {
		t.Run(map[bool]string{false: "behind", true: "future"}[future], func(t *testing.T) {
			t.Setenv("BD_IGNORE_SCHEMA_SKEW", "0")
			f := newPristineEmbeddedDoltFixture(t, "sessiondrift")
			ctx := t.Context()
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			te := &testEnv{dataDir: f.dataDir, database: f.database}
			if future {
				var version int
				te.queryScalar(t, ctx, "SELECT MAX(version) FROM schema_migrations", nil, &version)
				te.exec(t, ctx, "UPDATE schema_migrations SET version=999999 WHERE version=?", version)
			} else {
				te.exec(t, ctx, "DELETE FROM schema_migrations")
			}
			called := false
			err := embeddeddolt.WithExistingMemories(ctx, f.beadsDir, f.database, "main", embeddeddolt.MemorySessionOptions{Write: true}, func(memoryops.Memories) (bool, error) { called = true; return true, nil })
			if err == nil || called {
				t.Fatal("schema drift admitted or repaired", err)
			}
		})
	}
}

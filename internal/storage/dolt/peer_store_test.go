package dolt

import (
	"context"
	"os"
	"strconv"
	"testing"
)

// openPeerStore opens a second store on store's database, with a connection
// pool of its own as a second bd process would have.
func openPeerStore(t *testing.T, ctx context.Context, store *DoltStore) *DoltStore {
	t.Helper()
	return openStoreOnPort(t, ctx, store, replayServerPort(t))
}

// openStoreOnPort opens a store on store's database through whatever listens
// on port: the test server itself, or a test proxy in front of it.
func openStoreOnPort(t *testing.T, ctx context.Context, store *DoltStore, port int) *DoltStore {
	t.Helper()
	peer, err := New(ctx, &Config{
		Path:           t.TempDir(),
		CommitterName:  "test",
		CommitterEmail: "test@example.com",
		ServerHost:     "127.0.0.1",
		ServerPort:     port,
		Database:       store.database,
		MaxOpenConns:   2,
	})
	if err != nil {
		t.Fatalf("open store on %s through port %d: %v", store.database, port, err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	return peer
}

// Reuse the existing owned-loopback fixture when containers are unavailable.
func setupReplayTestStore(t *testing.T) (*DoltStore, func()) {
	t.Helper()
	if os.Getenv("BEADS_GRAPH_TEST_SERVER_PORT") == "" {
		return setupConcurrentTestStore(t)
	}
	s, cleanup := setupConfigCommitTestStore(t)
	s.db.SetMaxOpenConns(2)
	if err := s.SetConfig(t.Context(), "issue_prefix", "test"); err != nil {
		t.Fatal(err)
	}
	return s, cleanup
}

func replayServerPort(t *testing.T) int {
	t.Helper()
	if raw := os.Getenv("BEADS_GRAPH_TEST_SERVER_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			t.Fatal("invalid disposable SQL-server port")
		}
		return p
	}
	return testServerPort
}

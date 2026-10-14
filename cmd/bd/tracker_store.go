package main

import (
	"context"
	"fmt"
	"os"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// trackerStoreForCommand selects the tracker synchronization seam without
// opening a second direct store in proxied mode.
func trackerStoreForCommand(ctx context.Context) (tracker.Store, error) {
	if usesProxiedServer() {
		if uowProvider == nil {
			return nil, fmt.Errorf("proxied-server provider not initialized")
		}
		store := tracker.NewUOWStore(uowProvider)
		if store == nil {
			return nil, fmt.Errorf("proxied-server provider is invalid")
		}
		return store, nil
	}
	if err := ensureStoreActiveWithContext(ctx); err != nil {
		return nil, err
	}
	return tracker.NewStore(store), nil
}

func buildTrackerPullHooks(ctx context.Context) *tracker.PullHooks {
	prefix := "bd"
	// YAML config takes precedence — in shared-server mode the DB
	// may belong to a different project (GH#2469).
	if p := config.GetString("issue-prefix"); p != "" {
		prefix = p
	} else if store != nil {
		if p, err := store.GetConfig(ctx, "issue_prefix"); err == nil && p != "" {
			prefix = p
		}
	}

	return &tracker.PullHooks{
		GenerateID: func(_ context.Context, issue *types.Issue) error {
			if issue.ID == "" {
				issue.ID = generateIssueID(prefix)
			}
			return nil
		},
	}
}

// readTrackerEndpoint retains the existing temporary readonly open when needed,
// but never opens storage for an explicitly local destination.
func readTrackerEndpoint(ctx context.Context, key, envVar string) (string, error) {
	if config.GetString(key) != "" || os.Getenv(envVar) != "" {
		return tracker.ReadEndpoint(ctx, nil, key, envVar)
	}
	if usesProxiedServer() {
		st, err := trackerStoreForCommand(ctx)
		if err != nil {
			return "", err
		}
		return tracker.ReadEndpoint(ctx, st, key, envVar)
	}
	if store != nil || dbPath == "" {
		return tracker.ReadEndpoint(ctx, tracker.NewStore(store), key, envVar)
	}
	st, err := openReadOnlyStoreForDBPath(ctx, dbPath)
	if err != nil {
		return "", fmt.Errorf("checking tracker endpoint: %w", err)
	}
	defer func() { _ = st.Close() }()
	return tracker.ReadEndpoint(ctx, tracker.NewStore(st), key, envVar)
}

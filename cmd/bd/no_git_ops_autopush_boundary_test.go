package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
)

type noGitOpsAutoPushStore struct {
	storage.DoltStorage
	pushes int
}

func (s *noGitOpsAutoPushStore) GetCurrentCommit(context.Context) (string, error) {
	return "fixture-head", nil
}

func (s *noGitOpsAutoPushStore) Push(context.Context) error {
	s.pushes++
	return nil
}

func (s *noGitOpsAutoPushStore) HasRemote(context.Context, string) (bool, error) {
	return true, nil
}

// Adapted from #7908 to pin the independent Git workflow and auto-push settings.
func TestAutoPushWithNoGitOpsAndLocalOnly(t *testing.T) {
	for _, tt := range []struct {
		name, doltConfig string
		wantPushes       int
	}{
		{name: "default off"},
		{name: "opted in", doltConfig: "dolt:\n  auto-push: true\n", wantPushes: 1},
		{name: "local only", doltConfig: "dolt:\n  auto-push: true\n  local-only: true\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("USERPROFILE", os.Getenv("HOME"))
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
			beadsDir := newForgeSyncRemoteWorkspace(t, "")
			if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("no-git-ops: true\n"+tt.doltConfig), 0o600); err != nil {
				t.Fatal(err)
			}
			config.ResetForTesting()
			if err := config.Initialize(); err != nil {
				t.Fatal(err)
			}
			if !config.GetBool("no-git-ops") {
				t.Fatal("fixture did not load no-git-ops")
			}
			oldStore, oldCmdCtx, oldSandbox := store, cmdCtx, sandboxMode
			cmdCtx, sandboxMode = nil, false
			t.Cleanup(func() { store, cmdCtx, sandboxMode = oldStore, oldCmdCtx, oldSandbox })
			fake := &noGitOpsAutoPushStore{}
			store = fake
			maybeAutoPush(t.Context())
			maybeAutoPush(t.Context())
			if fake.pushes != tt.wantPushes {
				t.Errorf("push calls = %d, want %d including debounce", fake.pushes, tt.wantPushes)
			}
			_, err := os.Stat(filepath.Join(beadsDir, "push-state.json"))
			if (tt.wantPushes == 0 && !os.IsNotExist(err)) || (tt.wantPushes != 0 && err != nil) {
				t.Errorf("push-state stat error = %v, want state iff auto-push ran", err)
			}
		})
	}
}

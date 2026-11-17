package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/storage"
)

// Named and default remotes are observed through an inert store.
type positionalDoltRemoteStore struct {
	storage.DoltStorage
	operation string
	remote    string
}

func (s *positionalDoltRemoteStore) ListRemotes(context.Context) ([]storage.RemoteInfo, error) {
	return []storage.RemoteInfo{{Name: "origin"}}, nil
}

func (s *positionalDoltRemoteStore) Push(context.Context) error {
	s.operation = "push"
	return nil
}

func (s *positionalDoltRemoteStore) ForcePush(ctx context.Context) error {
	return s.Push(ctx)
}

func (s *positionalDoltRemoteStore) PushRemote(_ context.Context, remote string, _ bool) error {
	s.operation, s.remote = "push", remote
	return nil
}
func (s *positionalDoltRemoteStore) Pull(context.Context) error {
	s.operation = "pull"
	return nil
}
func (s *positionalDoltRemoteStore) PullRemote(_ context.Context, remote string) error {
	s.operation, s.remote = "pull", remote
	return nil
}

// Adapted from #7937. Positionals and flags must resolve to the same named
// operation while conflicts are refused before opening a store.
func TestDoltPushPullNamedRemoteBoundaries(t *testing.T) {
	beadsDir := newForgeSyncRemoteWorkspace(t, "")
	t.Setenv("HOME", filepath.Dir(beadsDir))
	t.Setenv("USERPROFILE", filepath.Dir(beadsDir))
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BD_DOLT_LOCAL_ONLY", "false")
	t.Setenv("BD_NO_PUSH", "false")
	t.Setenv("BD_NO_REMOTE_ADOPT", "1")
	oldStore, oldCmdCtx := store, cmdCtx
	cmdCtx = nil
	t.Cleanup(func() {
		store, cmdCtx = oldStore, oldCmdCtx
	})

	for _, tt := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"push", doltPushCmd}, {"pull", doltPullCmd},
	} {
		t.Run(tt.name, func(t *testing.T) {
			flag := tt.cmd.Flags().Lookup("remote")
			oldValue, oldChanged := flag.Value.String(), flag.Changed
			t.Cleanup(func() {
				if err := flag.Value.Set(oldValue); err != nil {
					t.Error(err)
				}
				flag.Changed = oldChanged
			})
			fake := &positionalDoltRemoteStore{}
			store = fake
			if err := flag.Value.Set(""); err != nil {
				t.Fatal(err)
			}
			if err := tt.cmd.RunE(tt.cmd, []string{"fixture-remote"}); err != nil {
				t.Errorf("named %s failed: %v", tt.name, err)
			}
			if fake.operation != tt.name || fake.remote != "fixture-remote" {
				t.Errorf("operation = %q remote = %q, want %q with fixture-remote", fake.operation, fake.remote, tt.name)
			}
			if !strings.Contains(tt.cmd.UseLine(), "[remote]") {
				t.Errorf("use line %q omits the positional remote", tt.cmd.UseLine())
			}
			if err := flag.Value.Set("flag-remote"); err != nil {
				t.Fatal(err)
			}
			fake.operation, fake.remote = "", ""
			if err := tt.cmd.RunE(tt.cmd, nil); err != nil {
				t.Errorf("%s with --remote failed: %v", tt.name, err)
			}
			if fake.operation != tt.name || fake.remote != "flag-remote" {
				t.Errorf("operation = %q remote = %q, want %q with flag-remote", fake.operation, fake.remote, tt.name)
			}
			if err := flag.Value.Set("second"); err != nil {
				t.Fatal(err)
			}
			if tt.cmd.Args == nil || tt.cmd.Args(tt.cmd, []string{"first"}) == nil {
				t.Error("Cobra argument validation accepted conflicting remote selectors")
			}
			if err := flag.Value.Set(""); err != nil {
				t.Fatal(err)
			}
			if tt.cmd.Args == nil || tt.cmd.Args(tt.cmd, []string{""}) == nil {
				t.Error("Cobra argument validation accepted an empty positional remote")
			}
		})
	}
}

// #7904 corrects the usage text about managed endpoint selection.
func TestDoltStartHelpDescribesManagedPortAllocation(t *testing.T) {
	out := captureStdout(t, func() error { return doltStartCmd.Help() })
	for _, phrase := range []string{"dolt-server.port", "dolt.port", "3308", "bd dolt show"} {
		if !strings.Contains(out, phrase) {
			t.Errorf("dolt start help omitted %q", phrase)
		}
	}
	if !strings.Contains(out, "outside bd") || !strings.Contains(out, "proxied-server") {
		t.Error("dolt start help lost the existing topology restrictions")
	}
}

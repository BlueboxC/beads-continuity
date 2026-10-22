package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

type vcStatusForkStore struct {
	storage.DoltStorage
	changes *storage.Status
	err     error
}

func (s *vcStatusForkStore) CurrentBranch(context.Context) (string, error) { return "main", nil }
func (s *vcStatusForkStore) GetCurrentCommit(context.Context) (string, error) {
	return "01234567890123456789012345678901", nil
}
func (s *vcStatusForkStore) Status(context.Context) (*storage.Status, error) { return s.changes, s.err }

func TestForkVCStatusShowsWorkingSet(t *testing.T) {
	oldStore, oldJSON, oldProxy, oldCmdCtx := store, jsonOutput, proxiedServerMode, cmdCtx
	t.Cleanup(func() { store = oldStore; jsonOutput = oldJSON; proxiedServerMode = oldProxy; cmdCtx = oldCmdCtx })
	cmdCtx = nil
	proxiedServerMode = false
	changes := &storage.Status{Staged: []storage.StatusEntry{{Table: "config", Status: "modified"}}, Unstaged: []storage.StatusEntry{{Table: "labels", Status: "modified"}, {Table: "metadata", Status: "new table"}}}
	store = &vcStatusForkStore{changes: changes}
	t.Run("text", func(t *testing.T) {
		jsonOutput = false
		out := captureStdout(t, func() error { return vcStatusCmd.RunE(vcStatusCmd, nil) })
		for _, want := range []string{"config", "labels", "metadata", "Staged", "Unstaged"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %s", want, out)
			}
		}
	})
	t.Run("json", func(t *testing.T) {
		jsonOutput = true
		out := captureStdout(t, func() error { return vcStatusCmd.RunE(vcStatusCmd, nil) })
		var r struct {
			Branch string `json:"branch"`
			Commit string `json:"commit"`
			Staged []struct {
				Table string `json:"table"`
			} `json:"staged"`
			Unstaged []struct {
				Table string `json:"table"`
			} `json:"unstaged"`
		}
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		if r.Branch != "main" || r.Commit == "" || len(r.Staged) != 1 || len(r.Unstaged) != 2 {
			t.Fatalf("incomplete status: %s", out)
		}
	})
	t.Run("clean", func(t *testing.T) {
		jsonOutput = false
		store = &vcStatusForkStore{changes: &storage.Status{}}
		out := captureStdout(t, func() error { return vcStatusCmd.RunE(vcStatusCmd, nil) })
		if !strings.Contains(out, "clean") {
			t.Fatalf("missing clean working set: %s", out)
		}
	})
	t.Run("read-failure", func(t *testing.T) {
		jsonOutput = false
		store = &vcStatusForkStore{err: errors.New("status read unavailable")}
		var got error
		out := captureStdout(t, func() error { got = vcStatusCmd.RunE(vcStatusCmd, nil); return nil })
		if got == nil || strings.Contains(out, "clean") || strings.Contains(out, "Branch:") {
			t.Fatalf("partial/false-clean result: %q, %v", out, got)
		}
	})
}

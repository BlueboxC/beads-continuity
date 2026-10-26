//go:build integration

package dolt

import (
	"context"
	"github.com/steveyegge/beads/internal/testutil"
	"github.com/steveyegge/beads/internal/types"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Adapted from PR #7870: a config-only loopback port serves a separate
// directory while the workspace retains a stale clone. No shared base is used.
func TestForkPushPullUsesConfiguredServersData(t *testing.T) {
	testutil.RequireDoltBinary(t)
	skipIfNoGit(t)

	for _, env := range []string{"BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_PORT", "BEADS_TEST_MODE", "BEADS_DOLT_SHARED_SERVER"} {
		t.Setenv(env, "")
	}

	baseDir := t.TempDir()
	remoteDir := filepath.Join(baseDir, "remote.git")
	runCmd(t, baseDir, "git", "init", "--bare", "-b", "main", remoteDir)
	seedDir := filepath.Join(baseDir, "seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, seedDir, "git", "init", "-b", "main")
	runCmd(t, seedDir, "git", "commit", "--allow-empty", "-m", "init")
	runCmd(t, seedDir, "git", "push", remoteDir, "main")
	remoteURL := "git+file://" + remoteDir

	serverDir := filepath.Join(baseDir, "server")
	if err := os.MkdirAll(serverDir, 0o755); err != nil {
		t.Fatal(err)
	}
	port, _ := startLocalDoltServer(t, serverDir)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	beadsDir := filepath.Join(baseDir, "workspace", ".beads")
	doltDir := filepath.Join(beadsDir, "dolt")
	dbName := uniqueTestDBName(t)
	store, err := New(ctx, &Config{
		Path:            doltDir,
		BeadsDir:        beadsDir,
		Database:        dbName,
		ServerHost:      "127.0.0.1",
		ServerPort:      port,
		ServerUser:      "root",
		CommitterName:   "test",
		CommitterEmail:  "test@example.com",
		AutoStart:       false,
		CreateIfMissing: true,
		Remote:          "origin",
	})
	if err != nil {
		t.Fatalf("New at 127.0.0.1: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.SetConfig(ctx, "issue_prefix", "lo"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := store.AddRemote(ctx, "origin", remoteURL); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	if _, err := store.CommitAll(ctx, "genesis"); err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	create := func(id, title string) {
		t.Helper()
		issue := &types.Issue{ID: id, Title: title, IssueType: types.TypeTask, Status: types.StatusOpen, Priority: 2}
		if err := store.CreateIssue(ctx, issue, "tester"); err != nil {
			t.Fatalf("CreateIssue %s: %v", id, err)
		}
		if err := store.Commit(ctx, "add "+id); err != nil {
			t.Fatalf("Commit %s: %v", id, err)
		}
	}
	create("lo-first", "first, pushed before the clone")
	if err := store.Push(ctx); err != nil {
		t.Fatalf("first Push: %v", err)
	}

	// The clone of the remote in .beads/dolt/<db>, not served by the server.
	if err := os.MkdirAll(doltDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, doltDir, "dolt", "clone", remoteURL, dbName)

	create("lo-second", "second, only on the server")
	if err := store.Push(ctx); err != nil {
		t.Fatalf("Push with the clone present: %v", err)
	}
	pushed := filepath.Join(baseDir, "after-push")
	doltClone(t, remoteURL, pushed)
	if rows := queryCSV(t, pushed, "SELECT id FROM issues WHERE id = 'lo-second'"); len(rows) != 1 {
		// Errorf, not Fatalf: the pull below is checked on its own.
		t.Errorf("remote lacks the server's commit lo-second after Push reported success; the push moved the clone in %s instead of the server's data", filepath.Join(doltDir, dbName))
	}

	// Another client pushes to the remote; a pull must merge it into the server.
	sourceInsertIssue(t, pushed, "lo-third", "third, from another client")
	sourceCommitAndPush(t, pushed, "add lo-third")
	waitOutGitRemoteReadCache()
	if err := store.Pull(ctx); err != nil {
		t.Fatalf("Pull with the clone present: %v", err)
	}
	if _, err := store.GetIssue(ctx, "lo-third"); err != nil {
		t.Fatalf("the server lacks the remote's lo-third after Pull: %v", err)
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
)

// seedSyncRemote records a sync remote in beadsDir the way bd records one, and
// proves it is readable afterwards.
//
// The tests that use this used to hand-write `sync.remote: <url>` into
// config.yaml — a key whose NAME contains a dot. That spelling is not what bd's
// writer produces and not what config.GetStringFromDir can read: it splits on
// the dot and walks nested mappings. Viper happens to find a literal dotted key
// as well as a nested one, so those fixtures worked through one reader while
// being invisible to the other — the exact asymmetry bd-zj95 was, and the
// reason the fixtures were green while the bug was live.
//
// Writing through the real writer and asserting the read-back means these tests
// can no longer encode a spelling at all. If the round trip breaks again, the
// fixture fails here, naming the property, rather than some downstream
// assertion failing for a reason that looks unrelated.
func seedSyncRemote(t *testing.T, beadsDir, remote string) {
	t.Helper()
	path := filepath.Join(beadsDir, "config.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// SetYamlConfigInDir edits an existing file; it declines to create one,
		// which is a deliberate guard against writing config into a directory
		// that is not a workspace.
		if writeErr := os.WriteFile(path, nil, 0o644); writeErr != nil { //nolint:gosec // test fixture
			t.Fatalf("create config.yaml: %v", writeErr)
		}
	} else if err != nil {
		t.Fatalf("stat config.yaml: %v", err)
	}

	if err := config.SetYamlConfigInDir(beadsDir, "sync.remote", remote); err != nil {
		t.Fatalf("record sync.remote: %v", err)
	}
	if got := config.GetStringFromDir(beadsDir, "sync.remote"); got != remote {
		body, _ := os.ReadFile(path) //nolint:gosec,errcheck // diagnostic only
		t.Fatalf("sync.remote does not read back after being written: got %q, want %q\nconfig.yaml:\n%s",
			got, remote, body)
	}
}

// snapshotBootstrapEnv saves all BD_/BEADS_-prefixed env vars, clears them,
// and registers a t.Cleanup to restore them. It restores via t.Cleanup
// (rather than returning a function for the caller to defer) because
// t.Cleanup callbacks only start running after the test function's own
// regular defers have all completed. Tests using this helper commonly call
// t.Setenv("BEADS_DIR", ...) / t.Setenv("BEADS_TEST_IGNORE_REPO_CONFIG", ...)
// afterward; t.Setenv captures its "restore to" value at call time, which
// would be the already-wiped (absent) state. If this helper's own
// restoration ran via a caller-deferred function, it would run before those
// t.Setenv cleanups fire and its correct restoration would be clobbered by
// them, permanently leaking the wipe forward into later tests in the same
// binary. Registering via t.Cleanup here instead makes this the
// first-registered (and thus, by t.Cleanup's LIFO order, last-run) cleanup,
// so it always has the final word.
func snapshotBootstrapEnv(t *testing.T) {
	t.Helper()
	saved := make(map[string]string)
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "BD_") || strings.HasPrefix(env, "BEADS_") {
			parts := strings.SplitN(env, "=", 2)
			key := parts[0]
			saved[key] = os.Getenv(key)
			_ = os.Unsetenv(key)
		}
	}
	t.Cleanup(func() {
		for _, env := range os.Environ() {
			if strings.HasPrefix(env, "BD_") || strings.HasPrefix(env, "BEADS_") {
				parts := strings.SplitN(env, "=", 2)
				_ = os.Unsetenv(parts[0])
			}
		}
		for key, val := range saved {
			_ = os.Setenv(key, val)
		}
	})
}

// setupSyncRemoteConfig writes sync.remote to .beads/config.yaml and
// initializes the config subsystem so resolveSyncRemote() can read it.
// Returns a cleanup function that resets config state.
func setupSyncRemoteConfig(t *testing.T, beadsDir, remote string) func() {
	t.Helper()
	seedSyncRemote(t, beadsDir, remote)
	config.ResetForTesting()
	t.Setenv("BEADS_DIR", beadsDir)
	t.Setenv("BEADS_TEST_IGNORE_REPO_CONFIG", "1")
	if err := config.Initialize(); err != nil {
		t.Fatalf("config.Initialize: %v", err)
	}
	return func() { config.ResetForTesting() }
}

// stubProbeGitRemoteDoltData replaces the refs/dolt/data probe for the
// duration of a test. Every test that configures a forge sync.remote must
// install a stub: the real probe shells out to `git ls-remote` against
// whatever host the fixture URL names.
//
// A configured sync.remote also suppresses the git-origin auto-detect leg
// (gitOriginHasDoltDataRef, which is not behind this seam), so these tests
// cannot reach the network through that path either — including the
// fall-through cases, whose whole point is to keep going after the probe.
func stubProbeGitRemoteDoltData(t *testing.T, fn func(string) (bool, error)) {
	t.Helper()
	orig := probeGitRemoteDoltData
	probeGitRemoteDoltData = fn
	t.Cleanup(func() { probeGitRemoteDoltData = orig })
}

// newForgeSyncRemoteWorkspace builds a workspace whose config.yaml carries
// remote as sync.remote and chdirs into it, returning the .beads path.
func newForgeSyncRemoteWorkspace(t *testing.T, remote string) string {
	t.Helper()
	snapshotBootstrapEnv(t)

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cleanup := setupSyncRemoteConfig(t, beadsDir, remote)
	t.Cleanup(cleanup)

	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	return beadsDir
}

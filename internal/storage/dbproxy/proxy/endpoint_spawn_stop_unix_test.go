//go:build unix

package proxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/procid"
	"github.com/steveyegge/beads/internal/storage/dbproxy/util"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShutdownWaitsForStartDelayedBeforeExec(t *testing.T) {
	runShutdownForParkedStarter(t, 150*time.Millisecond)
}

func TestShutdownWaitsForLiveStarterPastConfirmDeadline(t *testing.T) {
	runShutdownForParkedStarter(t, shutdownConfirmDeadline+time.Second)
}

func runShutdownForParkedStarter(t *testing.T, hold time.Duration) {
	t.Helper()
	root := t.TempDir()
	childPath := filepath.Join(root, "exit-child.sh")
	require.NoError(t, os.WriteFile(childPath, []byte("#!/bin/sh\nexit 0\n"), 0o700))

	previousResolve := ResolveExecutable
	previousHook := beforeProxyChildStart
	ResolveExecutable = func() (string, error) { return childPath, nil }
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseStarter := func() { releaseOnce.Do(func() { close(release) }) }
	var once sync.Once
	beforeProxyChildStart = func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	t.Cleanup(func() {
		ResolveExecutable = previousResolve
		beforeProxyChildStart = previousHook
	})

	startDone := make(chan error, 1)
	startExited := make(chan struct{})
	go func() {
		defer close(startExited)
		_, err := GetCreateDatabaseProxyServerEndpoint(root, externalOpenOpts(root))
		startDone <- err
	}()
	t.Cleanup(func() {
		releaseStarter()
		select {
		case <-startExited:
		case <-time.After(20 * time.Second):
			t.Error("parked starter did not exit during fixture cleanup")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("start did not reach injected pre-exec delay")
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- Shutdown(root) }()
	select {
	case err := <-stopDone:
		t.Fatalf("Shutdown returned during delayed start: %v", err)
	case <-time.After(hold):
		// The injected hook, rather than scheduler timing, holds the exact
		// historical release-lock-before-Start window open.
	}

	releaseStarter()
	select {
	case err := <-startDone:
		require.Error(t, err)
		assert.True(t, errors.Is(err, errStartInterrupted), "start error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("start did not terminate after concurrent shutdown")
	}
	select {
	case err := <-stopDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not finish after delayed start resolved")
	}

	assert.NoFileExists(t, filepath.Join(root, spawnMarkerFileName))
}

func TestGetCreateContextCancelDuringSpawnReapsOwnChild(t *testing.T) {
	root := t.TempDir()
	childPath := filepath.Join(root, "waiting-child.sh")
	require.NoError(t, os.WriteFile(childPath, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	previousResolve, previousHook := ResolveExecutable, beforeProxyChildStart
	ResolveExecutable = func() (string, error) { return childPath, nil }
	beforeProxyChildStart = cancel
	t.Cleanup(func() { ResolveExecutable, beforeProxyChildStart = previousResolve, previousHook })
	_, err := GetCreateDatabaseProxyServerEndpointContext(ctx, root, externalOpenOpts(root))
	require.ErrorIs(t, err, context.Canceled)
	assert.NoFileExists(t, filepath.Join(root, spawnMarkerFileName))
	lock, err := util.TryLock(filepath.Join(root, LockFileName))
	require.NoError(t, err)
	lock.Unlock()
}

// A starter must be free to take proxy.lock and clear its marker while
// Shutdown verifies that the starter is live.
func TestShutdownChecksLiveStarterWithoutHoldingProxyLock(t *testing.T) {
	root := t.TempDir()
	pid := os.Getpid()
	birth, err := procid.Capture(pid)
	require.NoError(t, err)
	require.NoError(t, writeSpawnMarker(root, spawnMarker{
		Schema: 1, PID: pid, Birth: string(birth), StartedUnix: time.Now().Unix(),
	}))
	marker := filepath.Join(root, spawnMarkerFileName)
	previous := verifyProcessIdentity
	defer func() { verifyProcessIdentity = previous }()
	var checks int
	var lockErr error
	verifyProcessIdentity = func(candidate int, token procid.Token) (bool, error) {
		if candidate == pid && token == birth {
			checks++
			lock, err := util.TryLock(filepath.Join(root, LockFileName))
			if err != nil {
				lockErr = err
			} else {
				_ = os.Remove(marker)
				lock.Unlock()
			}
		}
		return previous(candidate, token)
	}
	// Release the fixture marker even if the old code bypasses the hook,
	// allowing the behavioral failure without a five-second timeout.
	timer := time.AfterFunc(750*time.Millisecond, func() { _ = os.Remove(marker) })
	defer timer.Stop()
	require.NoError(t, Shutdown(root))
	require.Positive(t, checks, "Shutdown skipped the live-marker verification")
	require.NoError(t, lockErr, "marker verification excluded its owner from proxy.lock")
}

// hookMarkerVerify runs hook before each identity verification, passing the
// 1-based call number. In these tests no proxy record exists, so every call
// is a check of the spawn marker's owner. A non-nil error from hook fails
// that verification; otherwise the real one runs.
func hookMarkerVerify(t *testing.T, hook func(call int) error) {
	t.Helper()
	previous := verifyProcessIdentity
	var calls atomic.Int32
	verifyProcessIdentity = func(pid int, tok procid.Token) (bool, error) {
		if err := hook(int(calls.Add(1))); err != nil {
			return false, err
		}
		return previous(pid, tok)
	}
	t.Cleanup(func() { verifyProcessIdentity = previous })
}

// A start can publish its marker after Shutdown's lock-free check found none
// and before Shutdown takes proxy.lock. The re-check under the lock must see
// it, and Shutdown must wait until the marker is cleared.
func TestShutdownWaitsForMarkerPublishedBeforeItsLock(t *testing.T) {
	root := t.TempDir()
	markerPath := filepath.Join(root, spawnMarkerFileName)
	birth, err := procid.Capture(os.Getpid())
	require.NoError(t, err)

	var publishOnce sync.Once
	previousHook := afterSpawnMarkerLockFreeCheck
	afterSpawnMarkerLockFreeCheck = func() {
		publishOnce.Do(func() {
			// This test process is the marker's live owner.
			if err := writeSpawnMarker(root, spawnMarker{
				Schema:      1,
				PID:         os.Getpid(),
				Birth:       string(birth),
				StartedUnix: time.Now().Unix(),
			}); err != nil {
				t.Errorf("publish spawn marker: %v", err)
			}
		})
	}
	t.Cleanup(func() { afterSpawnMarkerLockFreeCheck = previousHook })

	var cleared atomic.Bool
	hookMarkerVerify(t, func(call int) error {
		// Call 1 is the locked re-check. Call 2 is the next lock-free check,
		// reached only if that re-check made Shutdown wait; the start clears
		// its marker there.
		if call == 2 {
			cleared.Store(true)
			if err := os.Remove(markerPath); err != nil {
				t.Errorf("clear spawn marker: %v", err)
			}
		}
		return nil
	})

	require.NoError(t, Shutdown(root))
	require.True(t, cleared.Load(), "Shutdown returned while a live start's marker was present")
	assert.NoFileExists(t, markerPath)
}

// clearOwnSpawnMarker retries a delete that fails, as one does on Windows
// while a waiter's lock-free read holds the marker open, instead of leaving a
// live-owner marker behind.
func TestClearOwnSpawnMarkerRetriesFailedDelete(t *testing.T) {
	root := t.TempDir()
	own := spawnMarker{Schema: 1, PID: os.Getpid(), Birth: "this-start", StartedUnix: time.Now().Unix()}
	require.NoError(t, writeSpawnMarker(root, own))

	attempts := 0
	previousRemove := removeSpawnMarkerFile
	removeSpawnMarkerFile = func(name string) error {
		attempts++
		if attempts <= 3 {
			return &os.PathError{Op: "remove", Path: name, Err: errors.New("simulated sharing violation")}
		}
		return previousRemove(name)
	}
	t.Cleanup(func() { removeSpawnMarkerFile = previousRemove })

	require.NoError(t, clearOwnSpawnMarker(root, own))
	assert.Equal(t, 4, attempts)
	assert.NoFileExists(t, filepath.Join(root, spawnMarkerFileName))
}

// A marker whose owner is gone (a crashed starter, or a PID since reused) is
// not an in-progress start: Shutdown removes it under proxy.lock and succeeds.
func TestShutdownRemovesDeadOwnerSpawnMarker(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, writeSpawnMarker(root, spawnMarker{
		Schema:      1,
		PID:         os.Getpid(),
		Birth:       "birth-token-of-an-exited-process",
		StartedUnix: time.Now().Unix(),
	}))

	require.NoError(t, Shutdown(root))
	assert.NoFileExists(t, filepath.Join(root, spawnMarkerFileName))
}

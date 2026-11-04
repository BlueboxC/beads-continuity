package server_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/dbproxy/server"
	"github.com/steveyegge/beads/internal/storage/dbproxy/util"
	"github.com/stretchr/testify/require"
)

func TestDoltServer_OwnedLogAdmissionWaitsForStart(t *testing.T) {
	t.Setenv("BEADS_DOLT_LOG_MAX_BYTES", "32")
	root := t.TempDir()
	path := filepath.Join(root, "server.log")
	old := strings.Repeat("keep", 100)
	require.NoError(t, os.WriteFile(path, []byte(old), 0o600))
	s, err := server.NewDoltServer("dolt", root, writeConfig(t, freePort(t)), path, 0, "", server.WithBoundedLog())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Stop(context.Background())) })
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, old, string(got), "a constructor cannot rotate a log before acquiring the backend lock")
	require.NoFileExists(t, path+".1")
	lock, err := util.TryLock(filepath.Join(root, server.LockFileName))
	require.NoError(t, err)
	defer lock.Unlock()
	require.Error(t, s.Start(context.Background()))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, old, string(got), "a competing start cannot mutate the current owner's log")
	require.NoFileExists(t, path+".1")
}

func TestDoltServer_OwnedLogStartupRetentionFailureStillServesAndRecovers(t *testing.T) {
	bin := requireDolt(t)
	t.Setenv("DOLT_DISABLE_EVENT_FLUSH", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BEADS_DOLT_LOG_MAX_BYTES", "1024")
	root := t.TempDir()
	path := filepath.Join(root, "server.log")
	old := strings.Repeat("previous diagnostics\n", 200)
	require.NoError(t, os.WriteFile(path, []byte(old), 0o600))
	require.NoError(t, os.Mkdir(path+".1", 0o700))
	var warnings atomic.Int32
	s, err := server.NewDoltServer(bin, root, writeConfig(t, freePort(t)), path, 0, "",
		server.WithBoundedLog(), server.WithLogErrorHandler(func(error) { warnings.Add(1) }))
	require.NoError(t, err)
	t.Cleanup(func() { stopWithTimeout(t, s) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, s.Start(ctx), "historical retention failure must not prevent SQL startup")
	db, err := sql.Open("mysql", s.DSN(ctx, "", "root", ""))
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	require.NoError(t, db.PingContext(ctx))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, old, string(got), "failed archiving must preserve previous diagnostics without growing them")
	require.Equal(t, int32(1), warnings.Load())

	// Repair only the disposable archive destination. The same backend must
	// resume bounded logging without requiring a restart or another warning.
	require.NoError(t, os.Remove(path+".1"))
	for range 200 {
		conn, err := s.Dial(ctx)
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	}
	require.Eventually(t, func() bool {
		active, err := os.Stat(path)
		if err != nil || active.Size() > 1024 {
			return false
		}
		tail, err := os.Stat(path + ".1")
		return err == nil && tail.Size() <= 1024
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, db.PingContext(ctx))
	require.True(t, s.Running(ctx))
	require.Equal(t, int32(1), warnings.Load())
}

// A startup-only rotation accepts this test's empty log and then lets real
// sql-server connection diagnostics grow it indefinitely. The assertion is
// made while that same backend is still running, before any restart or Stop.
func TestDoltServer_OwnedLogBoundsConnectionChurn(t *testing.T) {
	bin := requireDolt(t)
	t.Setenv("DOLT_DISABLE_EVENT_FLUSH", "1")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BEADS_DOLT_LOG_MAX_BYTES", "1024")
	root := t.TempDir()
	path := filepath.Join(root, "server.log")
	s, err := server.NewDoltServer(bin, root, writeConfig(t, freePort(t)), path, 0, "", server.WithBoundedLog())
	require.NoError(t, err)
	t.Cleanup(func() { stopWithTimeout(t, s) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, s.Start(ctx))
	for range 200 {
		conn, err := s.Dial(ctx)
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	}
	// Wait for the child to consume its connections, not an arbitrary delay.
	require.Eventually(t, func() bool {
		_, err := os.Stat(path + ".1")
		if err == nil {
			return true
		}
		info, err := os.Stat(path)
		return err == nil && info.Size() > 1024
	}, 5*time.Second, 10*time.Millisecond)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.LessOrEqual(t, info.Size(), int64(1024), "server.log grew while its backend was running")
	require.True(t, s.Running(ctx), "rotation must not restart or kill the backend")
	db, err := sql.Open("mysql", s.DSN(ctx, "", "root", ""))
	require.NoError(t, err)
	require.NoError(t, db.PingContext(ctx), "backend must still serve after rotating its output")
	require.NoError(t, db.Close())
	require.NoError(t, s.Stop(ctx))
	for _, file := range []string{path, path + ".1"} {
		info, err := os.Stat(file)
		require.NoError(t, err)
		require.LessOrEqual(t, info.Size(), int64(1024), "retained server output exceeded its bound")
	}
}

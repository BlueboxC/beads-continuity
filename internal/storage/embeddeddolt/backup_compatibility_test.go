//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/memoryops"
	"github.com/stretchr/testify/require"
)

func backupGuardStore(t *testing.T, value string) *EmbeddedDoltStore {
	t.Helper()
	s, err := newStore(t.Context(), t.TempDir(), "fixture", "main", openStrict)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.withConn(t.Context(), true, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(t.Context(), "CREATE TABLE restore_guard (id INT PRIMARY KEY, content TEXT)"); err != nil {
			return err
		}
		_, err := tx.ExecContext(t.Context(), "INSERT INTO restore_guard VALUES (1, ?)", value)
		return err
	}))
	require.NoError(t, s.Commit(t.Context(), "guard fixture"))
	return s
}

func backupGuardValue(t *testing.T, s *EmbeddedDoltStore) string {
	t.Helper()
	var value string
	require.NoError(t, s.withConn(t.Context(), false, func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT content FROM restore_guard WHERE id=1").Scan(&value)
	}))
	return value
}

func TestEmbeddedBackupRestoreCompatibility(t *testing.T) {
	source := backupGuardStore(t, "source")
	backup := t.TempDir()
	require.NoError(t, source.BackupDatabase(t.Context(), backup))
	head, err := source.GetCurrentCommit(t.Context())
	require.NoError(t, err)
	target := backupGuardStore(t, "destination")
	require.Error(t, target.RestoreDatabase(t.Context(), backup, false))
	require.Equal(t, "destination", backupGuardValue(t, target))
	require.NoError(t, target.RestoreDatabase(t.Context(), backup, true))
	require.Equal(t, "source", backupGuardValue(t, target))
	got, err := target.GetCurrentCommit(t.Context())
	require.NoError(t, err)
	require.Equal(t, head, got, "the rehearsal commit must stay in the disposable engine")
	require.NoError(t, target.Commit(t.Context(), "after restore"))
}

func TestEmbeddedBackupRestoreRejectsInvalidBackupWithoutReplacingTarget(t *testing.T) {
	backup := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(backup, "manifest"), []byte("invalid native backup"), 0o600))
	target := backupGuardStore(t, "destination")
	head, err := target.GetCurrentCommit(t.Context())
	require.NoError(t, err)
	err = target.RestoreDatabase(t.Context(), backup, true)
	require.ErrorContains(t, err, "before replacing the destination")
	require.Equal(t, "destination", backupGuardValue(t, target))
	got, err := target.GetCurrentCommit(t.Context())
	require.NoError(t, err)
	require.Equal(t, head, got)
}

// The fixture is a synthetic Dolt 2.4.2 backup with one issue and backup-proof memory.
// It was rejected by the old pin; this pins restore, subsequent writes and a second restore.
func TestEmbeddedDolt242BackupRoundTrip(t *testing.T) {
	backup := os.Getenv("BEADS_TEST_DOLT242_BACKUP")
	if backup == "" {
		t.Skip("set BEADS_TEST_DOLT242_BACKUP to the owned synthetic 2.4.2 fixture")
	}
	dir := t.TempDir()
	s, err := newStore(t.Context(), dir, "fixture", "main", openStrict)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.RestoreDatabase(t.Context(), backup, true))
	head, err := s.GetCurrentCommit(t.Context())
	require.NoError(t, err)
	var id string
	require.NoError(t, s.withConn(t.Context(), false, func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT id FROM issues WHERE title = ?", "Synthetic backup issue").Scan(&id)
	}))
	memories, err := s.Memories()
	require.NoError(t, err)
	got, err := memories.Recall(t.Context(), memoryops.RecallRequest{Key: "backup-proof"})
	require.NoError(t, err)
	require.True(t, got.Found)
	require.Equal(t, "Synthetic memory", got.Value)
	require.NoError(t, s.UpdateIssue(t.Context(), id, map[string]interface{}{"title": "Updated after restore"}, "fixture"))
	_, err = memories.Remember(t.Context(), memoryops.RememberRequest{Key: "backup-proof", Content: "Updated memory"})
	require.NoError(t, err)
	require.NoError(t, s.Commit(t.Context(), "writes after 2.4.2 restore"))
	require.NoError(t, s.Close())
	s, err = newStore(t.Context(), dir, "fixture", "main", openStrict)
	require.NoError(t, err)
	var retained int
	require.NoError(t, s.withConn(t.Context(), false, func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM dolt_log WHERE commit_hash = ?", head).Scan(&retained)
	}))
	require.Equal(t, 1, retained, "original backup history must remain reachable")
	nextBackup := t.TempDir()
	require.NoError(t, s.BackupDatabase(t.Context(), nextBackup))
	next := backupGuardStore(t, "destination")
	require.NoError(t, next.RestoreDatabase(t.Context(), nextBackup, true))
	issue, err := next.GetIssue(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "Updated after restore", issue.Title)
	memories, err = next.Memories()
	require.NoError(t, err)
	got, err = memories.Recall(t.Context(), memoryops.RecallRequest{Key: "backup-proof"})
	require.NoError(t, err)
	require.True(t, got.Found)
	require.Equal(t, "Updated memory", got.Value)
}

func TestEmbeddedBackupRestoreReadOnlyAndClosed(t *testing.T) {
	for _, closed := range []bool{false, true} {
		s := &EmbeddedDoltStore{readOnly: !closed}
		s.closed.Store(closed)
		err := s.RestoreDatabase(context.Background(), "not-a-backup", true)
		if closed {
			require.ErrorIs(t, err, errClosed)
		} else {
			require.ErrorIs(t, err, ErrReadOnly)
		}
	}
}

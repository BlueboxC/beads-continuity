//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"
	"os"
	"testing"

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

func TestEmbeddedBackupRestoreRejectsNewerFixtureWithoutReplacingTarget(t *testing.T) {
	backup := os.Getenv("BEADS_TEST_NEWER_DOLT_BACKUP")
	if backup == "" {
		t.Skip("set BEADS_TEST_NEWER_DOLT_BACKUP to an owned synthetic Dolt 2.4.2 backup")
	}
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

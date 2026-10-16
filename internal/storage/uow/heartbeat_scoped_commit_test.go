package uow

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestHeartbeatScopedCommitMergesSQLBeforeStaging(t *testing.T) {
	p, mock := newMockTxProvider(t)
	mock.ExpectExec("START TRANSACTION").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("^COMMIT;").WillReturnResult(sqlmock.NewResult(0, 0))
	for _, table := range []string{"issues", "events"} {
		mock.ExpectQuery(`CALL DOLT_ADD\(\?\)`).WithArgs(table).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(0))
	}
	mock.ExpectQuery(`CALL DOLT_COMMIT\('-m', \?\)`).WithArgs("re-arm").WillReturnRows(sqlmock.NewRows([]string{"hash"}).AddRow("commit"))
	tx, err := p.BeginTx(context.Background())
	require.NoError(t, err)
	require.NoError(t, tx.(scopedCommitTx).CommitScoped(context.Background(), []string{"issues", "events"}, "re-arm"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHeartbeatScopedCommitNeverReplaysUncertainSQLCommit(t *testing.T) {
	p, mock := newMockTxProvider(t)
	mock.ExpectExec("START TRANSACTION").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("^COMMIT;").WillReturnError(io.EOF)
	tx, err := p.BeginTx(context.Background())
	require.NoError(t, err)
	err = tx.(scopedCommitTx).CommitScoped(context.Background(), []string{"issues"}, "re-arm")
	require.ErrorIs(t, err, storage.ErrCommitIndeterminate)
	require.False(t, isSerializationError(err))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHeartbeatScopedCommitReturnsRollbackGuaranteedConflict(t *testing.T) {
	p, mock := newMockTxProvider(t)
	mock.ExpectExec("START TRANSACTION").WillReturnResult(sqlmock.NewResult(0, 0))
	cause := &mysql.MySQLError{Number: 1213, Message: "transaction rolled back"}
	mock.ExpectExec("^COMMIT;").WillReturnError(cause)
	mock.ExpectExec("ROLLBACK").WillReturnResult(sqlmock.NewResult(0, 0))
	tx, err := p.BeginTx(context.Background())
	require.NoError(t, err)
	err = tx.(scopedCommitTx).CommitScoped(context.Background(), []string{"issues"}, "re-arm")
	require.True(t, isSerializationError(err))
	require.False(t, errors.Is(err, storage.ErrCommitIndeterminate))
	tx.RollbackUnlessCommitted(context.Background())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHeartbeatScopedPublicationFailureDoesNotFailDurableMutation(t *testing.T) {
	p, mock := newMockTxProvider(t)
	mock.ExpectExec("START TRANSACTION").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("^COMMIT;").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`CALL DOLT_ADD\(\?\)`).WithArgs("issues").WillReturnError(errors.New("publication unavailable"))
	tx, err := p.BeginTx(context.Background())
	require.NoError(t, err)
	require.NoError(t, tx.(scopedCommitTx).CommitScoped(context.Background(), []string{"issues"}, "re-arm"))
	require.NoError(t, mock.ExpectationsWereMet())
}

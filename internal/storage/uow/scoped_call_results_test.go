package uow

import (
	"bytes"
	"context"
	"errors"
	"log"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestScopedPublicationObservesLateResultErrors(t *testing.T) {
	for _, phase := range []string{"add", "commit"} {
		for _, emptyFirst := range []bool{false, true} {
			t.Run(phase+"/"+map[bool]string{false: "row-first", true: "empty-first"}[emptyFirst], func(t *testing.T) {
				provider, mock := newMockTxProvider(t)
				ctx := context.Background()
				mock.ExpectExec("START TRANSACTION").WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("^COMMIT;").WillReturnResult(sqlmock.NewResult(0, 0))
				first := sqlmock.NewRows([]string{"status"})
				if !emptyFirst {
					first.AddRow(0)
				}
				cause := errors.New("late result failure")
				late := sqlmock.NewRows([]string{"status"}).AddRow(0).RowError(0, cause)
				add := mock.ExpectQuery(`CALL DOLT_ADD\(\?\)`).WithArgs("issues")
				if phase == "add" {
					add.WillReturnRows(first, late).RowsWillBeClosed()
				} else {
					add.WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(0))
					mock.ExpectQuery(`CALL DOLT_COMMIT\('-m', \?\)`).WithArgs("test").WillReturnRows(first, late).RowsWillBeClosed()
				}
				var output bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&output)
				t.Cleanup(func() { log.SetOutput(previous) })
				tx, err := provider.BeginTx(ctx)
				require.NoError(t, err)
				// SQL already committed: report the history failure, never ask
				// callers to replay the durable mutation.
				require.NoError(t, tx.(scopedCommitTx).CommitScoped(ctx, []string{"issues"}, "test"))
				require.Contains(t, output.String(), cause.Error())
				require.Zero(t, provider.db.Stats().OpenConnections, "failed publication must discard its session")
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}

func TestScopedPublicationDrainsSuccessfulResultSets(t *testing.T) {
	provider, mock := newMockTxProvider(t)
	ctx := context.Background()
	mock.ExpectExec("START TRANSACTION").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("^COMMIT;").WillReturnResult(sqlmock.NewResult(0, 0))
	for _, query := range []string{`CALL DOLT_ADD\(\?\)`, `CALL DOLT_COMMIT\('-m', \?\)`} {
		mock.ExpectQuery(query).WillReturnRows(
			sqlmock.NewRows([]string{"status"}),
			sqlmock.NewRows([]string{"message"}).AddRow("one").AddRow("two"),
			sqlmock.NewRows([]string{"status"}).AddRow(0),
		).RowsWillBeClosed()
	}
	tx, err := provider.BeginTx(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.(scopedCommitTx).CommitScoped(ctx, []string{"issues"}, "test"))
	require.Equal(t, 1, provider.db.Stats().OpenConnections)
	mock.ExpectQuery("SELECT 42").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(42))
	var value int
	require.NoError(t, provider.db.QueryRowContext(ctx, "SELECT 42").Scan(&value))
	require.Equal(t, 42, value)
	require.NoError(t, mock.ExpectationsWereMet())
}

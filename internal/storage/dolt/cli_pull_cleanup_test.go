package dolt

import (
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
)

func TestCLISettlementDeterminateFailuresAbort(t *testing.T) {
	for _, phase := range []string{"conflict read", "Dolt commit", "SQL commit"} {
		t.Run(phase, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			rejected := &mysql.MySQLError{Number: 1213, Message: "transaction rejected"}
			mock.ExpectExec(regexp.QuoteMeta("SET @@dolt_allow_commit_conflicts = 1")).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectBegin()
			conflicts := regexp.QuoteMeta("SELECT `table`, num_conflicts FROM dolt_conflicts")
			if phase == "conflict read" {
				mock.ExpectQuery(conflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}))
				mock.ExpectQuery(conflicts).WillReturnError(rejected)
				mock.ExpectRollback()
			} else {
				mock.ExpectQuery(conflicts).WillReturnRows(sqlmock.NewRows([]string{"table", "num_conflicts"}).AddRow("metadata", 1))
				mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_CONFLICTS_RESOLVE('--theirs', 'metadata')")).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_ADD('metadata')")).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectQuery(regexp.QuoteMeta("SELECT `table` FROM dolt_constraint_violations WHERE num_violations > 0")).WillReturnRows(sqlmock.NewRows([]string{"table"}))
				commit := mock.ExpectExec("CALL DOLT_COMMIT")
				if phase == "Dolt commit" {
					commit.WillReturnError(rejected)
					mock.ExpectRollback()
				} else {
					commit.WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectCommit().WillReturnError(rejected)
				}
			}
			mock.ExpectExec(regexp.QuoteMeta("CALL DOLT_MERGE('--abort')")).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta("SET @@dolt_allow_commit_conflicts = 0")).WillReturnResult(sqlmock.NewResult(0, 0))
			store := &DoltStore{db: db}
			err = store.autoResolveConflictsAfterCLIPull(t.Context(), nil, false)
			if !errors.Is(err, rejected) {
				t.Fatalf("error = %v, want original rejection", err)
			}
			if errors.Is(err, ErrCommitIndeterminate) {
				t.Fatalf("determinate error marked indeterminate: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

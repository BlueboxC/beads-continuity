package versioncontrolops

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// The MySQL driver can report a procedure failure after its first result set.
// Exercise the pinned StageAndCommit session and check that no commit is
// attempted after a staging error and that the next statement still works.
func TestStageAndCommitReportsLateCallError(t *testing.T) {
	db, mock := newMockDB(t)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cause := errors.New("staging failed in the second result set")
	mock.ExpectQuery(matchPendingQuery.String()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("CALL DOLT_ADD(?)")).WithArgs("issues").
		WillReturnRows(sqlmock.NewRows([]string{"status"}),
			sqlmock.NewRows([]string{"status"}).AddRow(0).RowError(0, cause))
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	err = StageAndCommit(ctx, conn, map[string]bool{"issues": true}, "stage test", "")
	if !errors.Is(err, cause) {
		t.Errorf("StageAndCommit error = %v, want %v", err, cause)
	}
	var one int
	if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Errorf("same-session read = %d, %v, want 1, nil", one, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

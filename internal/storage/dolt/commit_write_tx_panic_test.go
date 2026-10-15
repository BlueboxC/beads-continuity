package dolt

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// Adapted from raymond-w-ko's upstream PR #7571. This baseline uses the pool
// directly; the contributor's pinned-connection path is not present here.
func TestCommitWriteTx_PanicRollsBackAndPropagates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := &DoltStore{db: db, database: "fixture"}
	mock.ExpectBegin()
	mock.ExpectRollback()
	func() {
		defer func() {
			if got := recover(); got != "write body panic" {
				t.Errorf("recovered %v, want the write body's panic", got)
			}
		}()
		_, _ = store.commitWriteTx(context.Background(), func(*sql.Tx) error { panic("write body panic") })
	}()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
	if got := db.Stats().InUse; got != 0 {
		t.Errorf("a panicking write body left %d session(s) in use", got)
	}
}

package dolt

import (
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
	"testing"
)

func TestCompactServerRefusesBeforeAnySQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &DoltStore{db: db}
	err = store.Compact(t.Context(), "initial", "head", 3, nil)
	if !errors.Is(err, versioncontrolops.ErrOnlineCompaction) {
		t.Fatalf("online rewrite was admitted: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

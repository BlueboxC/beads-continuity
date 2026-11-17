package uow

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/steveyegge/beads/internal/storage/schema"
)

// The ignored lane forces the lock path even though the main cursor is
// ahead of this binary. The provider must reject forward skew before the
// migration pass can change ignore rules or reconcile the database.
func TestInitSchemaRefusesForwardDriftBeforeMigration(t *testing.T) {
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "")
	t.Setenv(schema.AllowRemoteMigrateEnv, "0")
	ahead := schema.LatestVersion() + 1

	for _, lockRelease := range []bool{true, false} {
		name := "lock_released"
		if !lockRelease {
			name = "lock_release_failure"
		}
		t.Run(name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })

			expectNoSessionDatabase(mock)
			expectDatabaseExistsProbe(mock, "beads", true)
			mock.ExpectExec(regexp.QuoteMeta("USE `beads`")).WillReturnResult(sqlmock.NewResult(0, 0))
			expectVersionQuery(mock, ahead)
			expectCursorProbe(mock, "ignored_schema_migrations", false)
			lockName := schema.MigrationLockName("beads")
			mock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(?, ?)")).
				WithArgs(lockName, 5).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(1))
			mock.ExpectExec(regexp.QuoteMeta("CREATE DATABASE `beads`")).
				WillReturnError(&mysql.MySQLError{Number: 1007, Message: "database exists"})
			mock.ExpectExec(regexp.QuoteMeta("USE `beads`")).WillReturnResult(sqlmock.NewResult(0, 0))
			// The shared-store gate sees no pending main migrations. The
			// separate drift check must be asked before any migration DML.
			expectVersionQuery(mock, ahead)
			expectVersionQuery(mock, ahead)
			expectVersionQuery(mock, ahead)
			if lockRelease {
				expectLockRelease(mock, lockName)
			} else {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT RELEASE_LOCK(?)")).
					WithArgs(lockName).WillReturnRows(sqlmock.NewRows([]string{"released"}).AddRow(0))
			}

			p := &doltSQLProvider{defaultBranch: defaultBranch, db: db, serverEndpoint: "tcp:127.0.0.1:3306"}
			err = p.initSchemaAttempt(t.Context(), "beads", &bootstrapPreparer{provider: p, database: "beads"})
			var skew *schema.SchemaSkewError
			if !errors.As(err, &skew) || skew.DBVersion != ahead || skew.BinaryVersion != schema.LatestVersion() {
				t.Errorf("initSchemaAttempt at version %d = %v, want a typed schema skew refusal", ahead, err)
			}
			if !lockRelease && !errors.Is(err, schema.ErrMigrationLockRelease) {
				t.Errorf("expected the lock-release cause as well, got %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestInitSchemaConvergedChecksForwardDriftWithoutWrites(t *testing.T) {
	previous := migrateUpWithLock
	migrateUpWithLock = func(context.Context, *sql.Conn, string, ...schema.MigrateLockOption) (int, error) {
		return 0, nil
	}
	t.Cleanup(func() { migrateUpWithLock = previous })
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "")

	for _, offset := range []int{0, 1} {
		mockDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = mockDB.Close() })
		expectVersionQuery(mock, schema.LatestVersion()+offset)
		p := &doltSQLProvider{defaultBranch: defaultBranch, db: mockDB}
		err = p.initSchemaAttempt(t.Context(), "beads", nil)
		if offset == 0 && err != nil {
			t.Fatalf("converged schema: %v", err)
		}
		if offset == 1 && !schema.IsSchemaSkewError(err) {
			t.Fatalf("converged ahead schema: %v, want a schema skew refusal", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	}
}

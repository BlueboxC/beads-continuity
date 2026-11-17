package versioncontrolops

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const testCompactPendingQuery = `SELECT COUNT(*) FROM dolt_status s
WHERE NOT EXISTS ( SELECT 1 FROM dolt_ignore di WHERE di.ignored = 1 AND s.table_name LIKE di.pattern )`

func expectCompactPreflight(mock sqlmock.Sqlmock, head string) {
	mock.ExpectQuery("SELECT DOLT_HASHOF('main')").WillReturnRows(sqlmock.NewRows([]string{"hash"}).AddRow(head))
	mock.ExpectQuery(testCompactPendingQuery).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
}

const testCompactMetadataQuery = compactLogQuery

func compactMetaRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"hash", "committer", "email", "date", "author", "author_email", "author_date", "message"})
}

const testCompactIdentityQuery = "SELECT @@dolt_committer_name, @@dolt_committer_email, @@dolt_committer_date"
const testCompactIdentitySet = "SET @@dolt_committer_name = ?, @@dolt_committer_email = ?, @@dolt_committer_date = ?"

// Adapted from uschtwill's #7296; committer overrides follow bee-ghosttrack's review.
func TestCompactRestoresCommitMetadata(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		failReplay, failRestore bool
	}{
		{name: "success"}, {name: "replay_error", failReplay: true}, {name: "restore_error", failRestore: true}, {name: "both_errors", failReplay: true, failRestore: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failReplay := tc.failReplay
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			expectCompactPreflight(mock, "recent")
			mock.ExpectQuery(testCompactMetadataQuery).WillReturnRows(compactMetaRows().
				AddRow("boundary", "base", "base@example.com", "2026-09-01T07:00:00.000000Z", "original author", "author@example.com", "first authored", "base message").
				AddRow("recent", "Jane (work) <tag>", "jane@example.com", "2026-10-01T08:00:00.125000Z", "new author", "author@example.com", "later authored", "recent message"))
			mock.ExpectQuery(testCompactIdentityQuery).WillReturnRows(sqlmock.NewRows([]string{"name", "email", "date"}).AddRow("owner", "owner@example.com", "2026-10-05T10:00:00Z"))
			mock.ExpectExec("CALL DOLT_BRANCH('compact-tmp', ?)").WithArgs("boundary").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("CALL DOLT_CHECKOUT('compact-tmp')").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("CALL DOLT_RESET('--soft', ?)").WithArgs("initial").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(testCompactIdentitySet).WithArgs("owner", "owner@example.com", "2026-09-01T07:00:00.000000Z").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec("CALL DOLT_COMMIT('-Am', ?, '--date', ?)").WithArgs("compact: squash 2 commits into base snapshot", "2026-09-01T07:00:00.000000Z").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(testCompactIdentitySet).WithArgs("Jane (work) <tag>", "jane@example.com", "2026-10-01T08:00:00.125000Z").WillReturnResult(sqlmock.NewResult(0, 0))
			replay := mock.ExpectExec("CALL DOLT_CHERRY_PICK('--allow-empty', ?)").WithArgs("recent")
			want := errors.New("replay failed")
			if failReplay {
				replay.WillReturnError(want)
			} else {
				replay.WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery(testCompactMetadataQuery + " ORDER BY commit_order ASC").
					WillReturnRows(compactMetaRows().
						AddRow("initial", "base", "base@example.com", "old", "author", "author@example.com", "old", "init").
						AddRow("rewritten-base", "owner", "owner@example.com", "2026-09-01T07:00:00.000000Z", "owner", "owner@example.com", "date", "squashed").
						AddRow("rewritten-recent", "Jane (work) <tag>", "jane@example.com", "2026-10-01T08:00:00.125000Z", "new author", "author@example.com", "later authored", "recent message"))
			}
			mock.ExpectExec("CALL DOLT_CHECKOUT('main')").WillReturnResult(sqlmock.NewResult(0, 0))
			if !failReplay {
				expectCompactPreflight(mock, "recent")
				mock.ExpectQuery("SELECT COUNT(*) FROM dolt_diff_summary('main', 'compact-tmp')").
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
				mock.ExpectExec("CALL DOLT_RESET('--hard', 'compact-tmp')").WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectExec("CALL DOLT_BRANCH('-D', 'compact-tmp')").WillReturnResult(sqlmock.NewResult(0, 0))
			restore := mock.ExpectExec(testCompactIdentitySet).WithArgs("owner", "owner@example.com", "2026-10-05T10:00:00Z")
			restoreErr := errors.New("restore failed")
			if tc.failRestore {
				restore.WillReturnError(restoreErr)
			} else {
				restore.WillReturnResult(sqlmock.NewResult(0, 0))
			}
			err = Compact(t.Context(), db, "initial", "boundary", 2, []string{"recent"})
			if failReplay && !errors.Is(err, want) {
				t.Fatalf("lost replay cause: %v", err)
			}
			if tc.failRestore && !errors.Is(err, restoreErr) {
				t.Fatalf("lost restoration error: %v", err)
			}
			if !failReplay && !tc.failRestore && err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Retain the contributor's refusal before any branch or session mutation.
func TestCompactRefusesUnknownRecentCommit(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	expectCompactPreflight(mock, "missing")
	mock.ExpectQuery(testCompactMetadataQuery).WillReturnRows(compactMetaRows().AddRow("boundary", "Ann", "ann@example.com", "2026-09-01T07:00:00.000000Z", "original author", "author@example.com", "first authored", "base message"))
	if err := Compact(t.Context(), db, "initial", "boundary", 1, []string{"missing"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing commit refusal, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompactRefusesUnreadableMetadata(t *testing.T) {
	for _, tc := range []string{"query_error", "bad_row", "unknown_boundary"} {
		t.Run(tc, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			expectCompactPreflight(mock, "boundary")
			q := mock.ExpectQuery(testCompactMetadataQuery)
			switch tc {
			case "query_error":
				q.WillReturnError(errors.New("unavailable"))
			case "bad_row":
				q.WillReturnRows(compactMetaRows().AddRow("boundary", nil, "email", "date", "author", "author@example.com", "authored", "message"))
			case "unknown_boundary":
				q.WillReturnRows(compactMetaRows())
			}
			if err := Compact(t.Context(), db, "initial", "boundary", 2, nil); err == nil {
				t.Fatal("unreadable metadata reached branch mutation")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// The replay is built on an isolated branch, but only a table-equivalent
// replay may replace main. A failed comparison must also restore the exact
// caller committer settings.
func TestCompactDivergentReplayRefusesBeforePublishing(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const hash = "boundary"
	const date = "2026-09-01T07:00:00.000000Z"
	const savedDate = "2026-10-05T10:00:00Z"
	ok := sqlmock.NewResult(0, 0)
	expectCompactPreflight(mock, hash)
	mock.ExpectQuery(testCompactMetadataQuery).WillReturnRows(compactMetaRows().
		AddRow(hash, "base", "base@example.com", date, "author", "author@example.com", "authored", "message"))
	mock.ExpectQuery(testCompactIdentityQuery).WillReturnRows(sqlmock.NewRows([]string{"name", "email", "date"}).
		AddRow("owner", "owner@example.com", savedDate))
	mock.ExpectExec("CALL DOLT_BRANCH('compact-tmp', ?)").WithArgs(hash).WillReturnResult(ok)
	mock.ExpectExec("CALL DOLT_CHECKOUT('compact-tmp')").WillReturnResult(ok)
	mock.ExpectExec("CALL DOLT_RESET('--soft', ?)").WithArgs("initial").WillReturnResult(ok)
	mock.ExpectExec(testCompactIdentitySet).WithArgs("owner", "owner@example.com", date).WillReturnResult(ok)
	mock.ExpectExec("CALL DOLT_COMMIT('-Am', ?, '--date', ?)").
		WithArgs("compact: squash 2 commits into base snapshot", date).WillReturnResult(ok)
	mock.ExpectQuery(testCompactMetadataQuery + " ORDER BY commit_order ASC").
		WillReturnRows(compactMetaRows().
			AddRow("initial", "base", "base@example.com", "old", "author", "author@example.com", "old", "init").
			AddRow("rewritten-base", "owner", "owner@example.com", date, "owner", "owner@example.com", "date", "squashed"))

	mock.ExpectExec("CALL DOLT_CHECKOUT('main')").WillReturnResult(ok)
	expectCompactPreflight(mock, hash)
	mock.ExpectQuery("SELECT COUNT(*) FROM dolt_diff_summary('main', 'compact-tmp')").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectExec("CALL DOLT_CHECKOUT('main')").WillReturnResult(ok)
	mock.ExpectExec("CALL DOLT_BRANCH('-D', 'compact-tmp')").WillReturnResult(ok)
	mock.ExpectExec(testCompactIdentitySet).WithArgs("owner", "owner@example.com", savedDate).WillReturnResult(ok)

	err = CompactAtHead(t.Context(), db, hash, "initial", hash, 2, nil)
	if err == nil || !strings.Contains(err.Error(), "replayed history differs") {
		t.Errorf("CompactAtHead error=%v, want a replay divergence refusal", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

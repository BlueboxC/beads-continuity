package issueops

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
)

func TestBatchHierarchySnapshotPreservesAncestryAndReadsOnce(t *testing.T) {
	db, mock, tx := beginMockTx(t)
	defer db.Close()
	mock.ExpectQuery("SELECT issue_id, COALESCE").WillReturnRows(
		sqlmock.NewRows([]string{"issue_id", "parent_id"}).
			AddRow("bd-child", "bd-parent").AddRow("bd-parent", "bd-grand").
			AddRow("bd-sibling", "bd-parent").AddRow("bd-wisp", "bd-child").
			AddRow("bd-loop-a", "bd-loop-b").AddRow("bd-loop-b", "bd-loop-a"))
	check := batchHierarchyChecker(t.Context(), tx)
	for _, tc := range []struct {
		source, target     string
		conflict, ancestor bool
	}{
		{"bd-child", "bd-grand", true, true}, {"bd-grand", "bd-child", true, false},
		{"bd-child", "bd-sibling", false, false}, {"bd-wisp", "bd-grand", true, true},
		{"bd-loop-a", "bd-other", false, false}, {"bd-child", "bd-child", false, false},
	} {
		err := check(&types.Dependency{IssueID: tc.source, DependsOnID: tc.target, Type: types.DepConditionalBlocks})
		var conflict *domain.DependencyHierarchyConflictError
		if errors.As(err, &conflict) != tc.conflict || (conflict != nil && conflict.BlockerIsAncestor != tc.ancestor) {
			t.Fatalf("%s -> %s: %v", tc.source, tc.target, err)
		}
	}
	for range 1000 {
		if err := check(&types.Dependency{IssueID: "bd-child", DependsOnID: "bd-other", Type: types.DepBlocks}); err != nil {
			t.Fatal(err)
		}
	}
	mock.ExpectRollback()
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchHierarchySnapshotErrorsAndCancellation(t *testing.T) {
	for _, mode := range []string{"query", "row", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, tx := beginMockTx(t)
			defer db.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := errors.New("catalog read failed")
			e := mock.ExpectQuery("SELECT issue_id, COALESCE")
			switch mode {
			case "query":
				e.WillReturnError(want)
			case "row":
				e.WillReturnRows(sqlmock.NewRows([]string{"issue_id", "parent_id"}).AddRow("a", "b").RowError(0, want))
			default:
				e.WillReturnRows(sqlmock.NewRows([]string{"issue_id", "parent_id"}))
			}
			check := batchHierarchyChecker(ctx, tx)
			dep := &types.Dependency{IssueID: "bd-a", DependsOnID: "bd-b", Type: types.DepBlocks}
			if mode == "cancel" {
				if err := check(dep); err != nil {
					t.Fatal(err)
				}
				cancel()
				want = context.Canceled
			}
			if err := check(dep); !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			mock.ExpectRollback()
			_ = tx.Rollback()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

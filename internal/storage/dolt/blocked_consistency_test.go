package dolt

import (
	"context"
	"database/sql"
	"testing"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// countInconsistencies wraps the read-only detection used by the bd doctor
// Blocked State check.
func countInconsistencies(ctx context.Context, t *testing.T, db *sql.DB) int64 {
	t.Helper()
	n, err := issueops.CountIsBlockedInconsistenciesInTx(ctx, db)
	if err != nil {
		t.Fatalf("CountIsBlockedInconsistenciesInTx: %v", err)
	}
	return n
}

// recomputeAll wraps the full repair used by 'bd doctor --fix' and returns the
// number of rows it corrected.
func recomputeAll(ctx context.Context, t *testing.T, db *sql.DB) int64 {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin recompute-all tx: %v", err)
	}
	changed, err := issueops.RecomputeAllIsBlockedInTx(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("RecomputeAllIsBlockedInTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit recompute-all tx: %v", err)
	}
	return changed
}

// TestRecomputeAllIsBlocked_RepairsStaleClearedFlag is the bd-6dnrw.37 repair
// path: a row that SHOULD be blocked but whose is_blocked was left at 0 (the
// shape a skipped post-pull recompute leaves behind). Detection must see it,
// the full recompute must fix it, and detection and repair must then agree the
// database is consistent — the lockstep that keeps the COUNT predicate from
// drifting from the recompute SQL.
func TestRecomputeAllIsBlocked_RepairsStaleClearedFlag(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// Correct graph via the normal write path: bm-w blocked on open bm-x.
	seedBlockedPair(ctx, t, store, true)
	if !isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("precondition: bm-w should be blocked by open bm-x")
	}
	// A correctly-maintained graph has zero inconsistencies.
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("consistent graph: want 0 inconsistencies, got %d", n)
	}

	// Corrupt: clear bm-w's flag directly, with no recompute — exactly what a
	// merge that bypassed the recompute hook leaves behind.
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET is_blocked = 0 WHERE id = 'bm-w'"); err != nil {
		t.Fatalf("corrupt is_blocked: %v", err)
	}
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after corruption: want 1 inconsistency, got %d", n)
	}

	// Repair via the full recompute (the always-available path that does not
	// need a pull to advance HEAD).
	if changed := recomputeAll(ctx, t, store.db); changed != 1 {
		t.Fatalf("repair: want 1 row corrected, got %d", changed)
	}
	if !isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("after repair: bm-w must read blocked again")
	}
	// Detection and repair now agree, and the repair is idempotent.
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after repair: want 0 inconsistencies, got %d", n)
	}
	if again := recomputeAll(ctx, t, store.db); again != 0 {
		t.Fatalf("repair must be idempotent: want 0 on second run, got %d", again)
	}
}

// TestRecomputeAllIsBlocked_ClearsStuckBlockedFlag is the mirror case: a row
// left is_blocked = 1 after its only blocker was closed remotely (a merge that
// bypassed the recompute hook). `bd ready` would keep hiding it; the full
// recompute must clear the flag.
func TestRecomputeAllIsBlocked_ClearsStuckBlockedFlag(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	seedBlockedPair(ctx, t, store, true)
	if !isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("precondition: bm-w should be blocked by open bm-x")
	}

	// "Merge": the remote closed the blocker; no local recompute ran, so bm-w
	// is stuck is_blocked = 1 with a closed blocker.
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET status = 'closed' WHERE id = 'bm-x'"); err != nil {
		t.Fatalf("simulate merged close: %v", err)
	}
	if !isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("setup: bm-w must still read blocked before the recompute (the stale flag is the bug)")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after merged close: want 1 inconsistency, got %d", n)
	}

	if changed := recomputeAll(ctx, t, store.db); changed != 1 {
		t.Fatalf("repair: want 1 row corrected, got %d", changed)
	}
	if isBlocked(ctx, t, store.db, "bm-w") {
		t.Fatal("after repair: bm-w must be unblocked (its only blocker is closed)")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after repair: want 0 inconsistencies, got %d", n)
	}
}

// TestRecomputeAllIsBlocked_CascadesThroughParentChild verifies the fixpoint:
// is_blocked propagates from a blocked parent to its child across passes. The
// single-pass detection COUNT is a documented lower bound here — it sees only
// the parent on the first pass — but the recompute corrects the whole chain and
// detection reaches 0 once it converges.
func TestRecomputeAllIsBlocked_CascadesThroughParentChild(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx, cancel := testContext(t)
	defer cancel()

	// bm-w blocked on open bm-x; bm-y is a child of bm-w, so bm-y inherits
	// blocked. All maintained by the normal write path.
	seedBlockedPair(ctx, t, store, true)
	child := &types.Issue{ID: "bm-y", Title: "bm-y", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := store.CreateIssue(ctx, child, "tester"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{IssueID: "bm-y", DependsOnID: "bm-w", Type: types.DepParentChild}, "tester"); err != nil {
		t.Fatalf("add parent-child: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', 'seed parent-child chain')"); err != nil && !isDoltNothingToCommit(err) {
		t.Fatalf("commit chain: %v", err)
	}
	if !isBlocked(ctx, t, store.db, "bm-y") {
		t.Fatal("precondition: child bm-y should inherit blocked from parent bm-w")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("consistent chain: want 0 inconsistencies, got %d", n)
	}

	// Corrupt both the parent and the child to is_blocked = 0.
	if _, err := store.db.ExecContext(ctx, "UPDATE issues SET is_blocked = 0 WHERE id IN ('bm-w', 'bm-y')"); err != nil {
		t.Fatalf("corrupt chain: %v", err)
	}
	// Single-pass detection sees only the parent (the child's parent-child
	// reason depends on the parent's still-corrupted flag) — a lower bound.
	if n := countInconsistencies(ctx, t, store.db); n != 1 {
		t.Fatalf("after corruption: want 1 (single-pass lower bound), got %d", n)
	}

	// The fixpoint corrects the whole chain across passes.
	if changed := recomputeAll(ctx, t, store.db); changed != 2 {
		t.Fatalf("repair: want 2 rows corrected across passes, got %d", changed)
	}
	if !isBlocked(ctx, t, store.db, "bm-w") || !isBlocked(ctx, t, store.db, "bm-y") {
		t.Fatal("after repair: both bm-w and bm-y must read blocked")
	}
	if n := countInconsistencies(ctx, t, store.db); n != 0 {
		t.Fatalf("after repair: want 0 inconsistencies, got %d", n)
	}
}

// staleParentArms are the parent kinds and statuses of the #7626 tests: a
// closed or pinned row is never blocked, but a merge that closes or pins it
// without a recompute may leave its stored is_blocked at 1.
var staleParentArms = []struct {
	name   string
	table  string // the parent's table
	wisp   bool
	status types.Status
}{
	{"issue_closed", "issues", false, types.StatusClosed},
	{"issue_pinned", "issues", false, types.StatusPinned},
	{"wisp_closed", "wisps", true, types.StatusClosed},
	{"wisp_pinned", "wisps", true, types.StatusPinned},
}

// plantStaleParent sets a parent's status and is_blocked = 1 directly, standing
// in for a merge that closed or pinned it without recomputing it.
func plantStaleParent(ctx context.Context, t *testing.T, store *DoltStore, table, id string, status types.Status) {
	t.Helper()
	//nolint:gosec // G201: table is a hardcoded "issues" or "wisps" from callers.
	if _, err := store.db.ExecContext(ctx,
		"UPDATE "+table+" SET status = ?, is_blocked = 1 WHERE id = ?", string(status), id); err != nil {
		t.Fatalf("plant stale %s parent %s: %v", status, id, err)
	}
}

// A closed or pinned parent with a stale is_blocked = 1 must not darken its
// open children when a write path recomputes them (gastownhall/beads#7626):
// adding a new child under it, or closing and reopening an existing one. The
// parent's own bit is outside every write's scope, so it stays stale; the
// children must not read it.
func TestParentChildCascade_StaleBitOnClosedOrPinnedParent(t *testing.T) {
	for _, arm := range staleParentArms {
		t.Run(arm.name, func(t *testing.T) {
			store, cleanup := setupTestStore(t)
			defer cleanup()
			ctx, cancel := testContext(t)
			defer cancel()

			p, c1, c2 := "sp-p", "sp-c1", "sp-c2"
			if arm.wisp {
				createWisp(t, ctx, store, p)
			} else {
				createPerm(t, ctx, store, p)
			}
			createPerm(t, ctx, store, c1)
			addDependencyWithMeta(t, ctx, store, c1, p, types.DepParentChild, "")
			plantStaleParent(ctx, t, store, arm.table, p, arm.status)
			if getIsBlocked(t, ctx, store, "issues", c1) {
				t.Fatalf("precondition: %s is_blocked = 1 before any write reached it", c1)
			}

			createPerm(t, ctx, store, c2)
			addDependencyWithMeta(t, ctx, store, c2, p, types.DepParentChild, "")
			if err := store.CloseIssue(ctx, c1, "done", "tester", ""); err != nil {
				t.Fatalf("close %s: %v", c1, err)
			}
			if err := store.ReopenIssue(ctx, c1, "", "tester"); err != nil {
				t.Fatalf("reopen %s: %v", c1, err)
			}

			for _, id := range []string{c1, c2} {
				if getIsBlocked(t, ctx, store, "issues", id) {
					t.Errorf("%s is_blocked = 1 under a %s parent with a stale bit", id, arm.status)
				}
			}
			// The writes never reach the parent, so its bit is still stale:
			// the children did not come bright by the parent being repaired.
			if !getIsBlocked(t, ctx, store, arm.table, p) {
				t.Errorf("%s is_blocked = 0; the write paths were not expected to reach it", p)
			}

			ready, err := store.GetReadyWork(ctx, types.WorkFilter{Status: types.StatusOpen})
			if err != nil {
				t.Fatalf("GetReadyWork: %v", err)
			}
			seen := map[string]bool{}
			for _, issue := range ready {
				seen[issue.ID] = true
			}
			for _, id := range []string{c1, c2} {
				if !seen[id] {
					t.Errorf("%s missing from ready work under a %s parent with a stale bit", id, arm.status)
				}
			}

			if arm.wisp {
				// A wisp child reaches the wisp parent through wisp_dependencies.
				createWisp(t, ctx, store, "sp-w")
				addDependencyWithMeta(t, ctx, store, "sp-w", p, types.DepParentChild, "")
				if getIsBlocked(t, ctx, store, "wisps", "sp-w") {
					t.Errorf("wisp child sp-w is_blocked = 1 under a %s wisp parent with a stale bit", arm.status)
				}
			}
		})
	}
}

// The doctor's count and the full repair over the same #7626 state, planted
// with no write in between: the children read 0, which is right, so the only
// inconsistent row is the parent's own stale bit. The count must name just
// that row, and the repair must correct just that row rather than mark the
// children in one pass and unmark them in the next.
func TestRecomputeAllIsBlocked_StaleBitOnClosedOrPinnedParent(t *testing.T) {
	for _, arm := range staleParentArms {
		t.Run(arm.name, func(t *testing.T) {
			store, cleanup := setupTestStore(t)
			defer cleanup()
			ctx, cancel := testContext(t)
			defer cancel()

			p, children := "rp-p", []string{"rp-c1", "rp-c2"}
			if arm.wisp {
				createWisp(t, ctx, store, p)
			} else {
				createPerm(t, ctx, store, p)
			}
			for _, c := range children {
				createPerm(t, ctx, store, c)
				addDependencyWithMeta(t, ctx, store, c, p, types.DepParentChild, "")
			}
			plantStaleParent(ctx, t, store, arm.table, p, arm.status)

			if n := countInconsistencies(ctx, t, store.db); n != 1 {
				t.Errorf("doctor count = %d, want 1 (the %s parent's own stale bit)", n, arm.status)
			}
			if changed := recomputeAll(ctx, t, store.db); changed != 1 {
				t.Errorf("full repair corrected %d rows, want 1 (the %s parent's own stale bit)", changed, arm.status)
			}
			if getIsBlocked(t, ctx, store, arm.table, p) {
				t.Errorf("after repair: %s is_blocked = 1, want 0", p)
			}
			for _, c := range children {
				if getIsBlocked(t, ctx, store, "issues", c) {
					t.Errorf("after repair: %s is_blocked = 1, want 0", c)
				}
			}
			if n := countInconsistencies(ctx, t, store.db); n != 0 {
				t.Errorf("after repair: %d inconsistent rows remain", n)
			}
			if n := recomputeAll(ctx, t, store.db); n != 0 {
				t.Errorf("second repair changed %d rows, want zero", n)
			}
		})
	}
}

package versioncontrolops

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/storage/issueops"
)

// Compact squashes old Dolt commits into a single base commit while preserving
// recent commits via cherry-pick. The recipe:
//  1. Create temp branch at the boundary commit (last old commit)
//  2. Checkout temp branch
//  3. Soft-reset to initial commit (collapses old history into working set)
//  4. Commit as single squashed base, dated at the boundary
//  5. Cherry-pick each recent commit with its original committer and date
//  6. Checkout main, hard-reset to temp branch
//  7. Delete temp branch
//
// Callers should run PruneRemoteRefs and then DoltGC afterward to reclaim disk
// space — remote-tracking refs still anchor the pre-compact chain, and GC
// alone reclaims nothing while they exist (bd-agctw).
//
// conn must be a single database connection (not a pooled *sql.DB) since the
// stored procedures rely on session-scoped state (current branch, working set).
func Compact(ctx context.Context, conn DBConn, initialHash, boundaryHash string, oldCommits int, recentHashes []string) error {
	return CompactAtHead(ctx, conn, planNewest(boundaryHash, recentHashes), initialHash, boundaryHash, oldCommits, recentHashes)
}

// CompactAtHead requires exclusive writer access for the entire operation.
// Head checks detect stale plans; they are not an atomic compare-and-reset.
// Server adapters refuse this operation. The embedded caller owns the engine lease.
func CompactAtHead(ctx context.Context, conn DBConn, plannedHead, initialHash, boundaryHash string, oldCommits int, recentHashes []string) (retErr error) {
	if err := requireMainAt(ctx, conn, plannedHead, "before building the squashed branch"); err != nil {
		return err
	}
	if newest := planNewest(boundaryHash, recentHashes); newest != plannedHead {
		return fmt.Errorf("%w: main is at %s, but the plan ends at %s; main was left unchanged", ErrPlanNotAtHead, plannedHead, newest)
	}
	if err := requireCleanCompactWorkingSet(ctx, conn); err != nil {
		return err
	}

	// Capture the timeline before rewriting it (#7294). Cherry-pick preserves
	// authorship itself; committer overrides avoid amend overwriting the author.
	meta, err := compactCommitMetadata(ctx, conn)
	if err != nil {
		return fmt.Errorf("compact step %q: %w", "read commit metadata", err)
	}
	boundary, ok := meta[boundaryHash]
	if !ok {
		return fmt.Errorf("compact: boundary commit %s not found in dolt_log", boundaryHash)
	}
	for _, hash := range recentHashes {
		if _, ok := meta[hash]; !ok {
			return fmt.Errorf("compact: recent commit %s not found in dolt_log", hash)
		}
	}
	var identity compactCommit
	if err := conn.QueryRowContext(ctx, "SELECT @@dolt_committer_name, @@dolt_committer_email, @@dolt_committer_date").Scan(&identity.name, &identity.email, &identity.date); err != nil {
		return fmt.Errorf("compact: read session committer: %w", err)
	}
	setIdentity := func(ctx context.Context, m compactCommit) error {
		_, err := conn.ExecContext(ctx, "SET @@dolt_committer_name = ?, @@dolt_committer_email = ?, @@dolt_committer_date = ?", m.name, m.email, m.date)
		return err
	}
	// Restore the caller's overrides, including a nonempty date, on every exit.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := setIdentity(cleanupCtx, identity); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("compact: restore session committer: %w", err))
		}
	}()
	branchCreated := false

	// Best-effort cleanup: if any step fails after creating the temp branch,
	// try to return to main and delete the temp branch so future compactions
	// aren't blocked by a leftover branch.
	defer func() {
		if retErr != nil && branchCreated {
			_, _ = conn.ExecContext(ctx, "CALL DOLT_CHECKOUT('main')")
			_, _ = conn.ExecContext(ctx, "CALL DOLT_BRANCH('-D', 'compact-tmp')")
		}
	}()

	execSQL := func(name, query string, args ...interface{}) error {
		if _, err := conn.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("compact step %q: %w", name, err)
		}
		return nil
	}

	if err := execSQL("create temp branch", "CALL DOLT_BRANCH('compact-tmp', ?)", boundaryHash); err != nil {
		return err
	}
	branchCreated = true

	if err := execSQL("checkout temp", "CALL DOLT_CHECKOUT('compact-tmp')"); err != nil {
		return err
	}
	if err := execSQL("soft reset to initial", "CALL DOLT_RESET('--soft', ?)", initialHash); err != nil {
		return err
	}
	msg := fmt.Sprintf("compact: squash %d commits into base snapshot", oldCommits)
	baseIdentity := identity
	baseIdentity.date = boundary.date
	if err := setIdentity(ctx, baseIdentity); err != nil {
		return fmt.Errorf("compact: date squashed base: %w", err)
	}
	if err := execSQL("commit squashed base", "CALL DOLT_COMMIT('-Am', ?, '--date', ?)", msg, boundary.date); err != nil {
		return err
	}

	// --allow-empty: the preserved window can contain empty commits (a Dolt
	// auto-commit with no table change, or a bd create double-commit whose
	// leading member has 0 changed tables). Without this flag DOLT_CHERRY_PICK
	// aborts the entire replay at the first empty commit with Error 1105
	// ("The previous cherry-pick commit is empty. Use --allow-empty ..."),
	// leaving compaction permanently blocked on active databases. See #3815.
	for _, hash := range recentHashes {
		if err := setIdentity(ctx, meta[hash]); err != nil {
			return fmt.Errorf("compact: restore committer for %s: %w", hash, err)
		}
		if err := execSQL(fmt.Sprintf("cherry-pick %s", hash[:min(8, len(hash))]), "CALL DOLT_CHERRY_PICK('--allow-empty', ?)", hash); err != nil {
			return err
		}
	}

	if err := verifyCompactReplay(ctx, conn, meta, recentHashes, initialHash, boundary.date); err != nil {
		return err
	}

	if err := execSQL("checkout main", "CALL DOLT_CHECKOUT('main')"); err != nil {
		return err
	}
	if err := requireMainAt(ctx, conn, plannedHead, "before resetting main"); err != nil {
		return err
	}
	if err := requireCleanCompactWorkingSet(ctx, conn); err != nil {
		return err
	}
	// Verify against main only after confirming it still names plannedHead.
	// The pinned Dolt table function requires literal reference arguments.
	var changedTables int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM dolt_diff_summary('main', 'compact-tmp')").Scan(&changedTables); err != nil {
		return fmt.Errorf("compact: verify replayed history: %w", err)
	}
	if changedTables != 0 {
		return fmt.Errorf("compact: replayed history differs from main in %d table(s)", changedTables)
	}
	if err := execSQL("reset main to compacted", "CALL DOLT_RESET('--hard', 'compact-tmp')"); err != nil {
		return err
	}
	if err := execSQL("delete temp branch", "CALL DOLT_BRANCH('-D', 'compact-tmp')"); err != nil {
		return err
	}

	return nil
}

// Adapted from uschtwill's #7296 metadata snapshot and bee-ghosttrack's
// committer-variable review. Formatting in SQL retains UTC millisecond dates
// across both driver paths without imposing the connection's local time zone.
type compactCommit struct {
	name, email, date                        string
	author, authorEmail, authorDate, message string
}

const compactLogQuery = "SELECT commit_hash, committer, email, DATE_FORMAT(date, '%Y-%m-%dT%H:%i:%s.%fZ'), author, author_email, DATE_FORMAT(author_date, '%Y-%m-%dT%H:%i:%s.%fZ'), message FROM dolt_log"

func compactCommitMetadata(ctx context.Context, conn DBConn) (map[string]compactCommit, error) {
	log, err := readCompactLog(ctx, conn, compactLogQuery)
	if err != nil {
		return nil, err
	}
	meta := make(map[string]compactCommit, len(log))
	for _, row := range log {
		meta[row.hash] = row.commit
	}
	return meta, nil
}

type compactLogRow struct {
	hash   string
	commit compactCommit
}

func readCompactLog(ctx context.Context, conn DBConn, query string) ([]compactLogRow, error) {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []compactLogRow
	for rows.Next() {
		var row compactLogRow
		m := &row.commit
		if err := rows.Scan(&row.hash, &m.name, &m.email, &m.date, &m.author, &m.authorEmail, &m.authorDate, &m.message); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// Verify both identities, dates and message before replacing main.
func verifyCompactReplay(ctx context.Context, conn DBConn, originals map[string]compactCommit, kept []string, initial, boundaryDate string) error {
	replay, err := readCompactLog(ctx, conn, compactLogQuery+" ORDER BY commit_order ASC")
	if err != nil {
		return fmt.Errorf("compact: read replayed commit metadata: %w", err)
	}
	if len(replay) != len(kept)+2 || replay[0].hash != initial || replay[1].commit.date != boundaryDate {
		return errors.New("compact: replayed history has an unexpected initial commit, boundary date or commit count")
	}
	for i, hash := range kept {
		if replay[i+2].commit != originals[hash] {
			return fmt.Errorf("compact: replayed metadata differs from original commit %s", hash)
		}
	}
	return nil
}

// Head guards adapted from uschtwill's gastownhall/beads#7502.
// ErrMainMoved reports that main's head is no longer the commit a compaction
// plan was built from, so the compaction stopped without changing main.
var ErrMainMoved = errors.New("main moved during compaction")

// ErrPlanNotAtHead reports that main is at the head read before the plan,
// but the plan's newest commit is a different commit, so the compaction
// stopped without changing main.
var ErrPlanNotAtHead = errors.New("compaction plan does not end at main's head")

// planNewest is the newest commit a plan names: the last commit it replays
// or, when it replays none, the boundary commit itself.
func planNewest(boundaryHash string, recentHashes []string) string {
	if len(recentHashes) > 0 {
		return recentHashes[len(recentHashes)-1]
	}
	return boundaryHash
}

// MainHead returns the commit at the head of main.
func MainHead(ctx context.Context, conn DBConn) (string, error) {
	var head string
	if err := conn.QueryRowContext(ctx, "SELECT DOLT_HASHOF('main')").Scan(&head); err != nil {
		return "", err
	}
	return head, nil
}

// requireMainAt returns ErrMainMoved, in words, unless main's head is want.
func requireMainAt(ctx context.Context, conn DBConn, want, when string) error {
	head, err := MainHead(ctx, conn)
	if err != nil {
		return fmt.Errorf("compact: reading the head of main %s: %w", when, err)
	}
	if head != want {
		return fmt.Errorf("%w: the plan was built from main at %s, but main is at %s (checked %s), "+
			"so main was left unchanged; run compact again",
			ErrMainMoved, want, head, when)
	}
	return nil
}

// ErrOnlineCompaction prevents an unguarded history rewrite on a multi-writer server.
var ErrOnlineCompaction = errors.New("Dolt history compaction is unavailable on server/proxy backends: the engine has no atomic compare-and-reset; use an exclusively opened embedded workspace")

var ErrUncommittedCompactChanges = errors.New("history compaction refuses uncommitted tracked changes; commit or discard them explicitly before retrying")

func requireCleanCompactWorkingSet(ctx context.Context, conn DBConn) error {
	pending, err := issueops.HasPendingChanges(ctx, conn)
	if err != nil {
		return fmt.Errorf("compact: read working-set status: %w", err)
	}
	if pending {
		return ErrUncommittedCompactChanges
	}
	return nil
}

package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
)

func runCompactProxiedServer(ctx context.Context) error {
	evt := metrics.NewCommandEvent("compact")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	if !compactDoltDryRun {
		CheckReadonly("compact")
	}

	if compactDoltDays < 0 {
		return HandleError("--days must be non-negative")
	}

	var logEntries []storage.CommitInfo
	if err := runProxiedNonTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var err error
		logEntries, err = versioncontrolops.Log(ctx, conn, 0)
		return err
	}); err != nil {
		return HandleError("failed to read commit log: %v", err)
	}

	totalCommits := len(logEntries)
	if totalCommits <= 1 {
		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"success":       true,
				"message":       "nothing to compact",
				"total_commits": totalCommits,
			})
		}
		fmt.Printf("Only %d commit(s). Nothing to compact.\n", totalCommits)
		return nil
	}

	cutoff := time.Now().AddDate(0, 0, -compactDoltDays)

	var oldCommits int
	var recentHashes []string
	for _, entry := range logEntries {
		if entry.Date.Before(cutoff) {
			oldCommits++
		} else {
			recentHashes = append(recentHashes, entry.Hash)
		}
	}
	initialHash := logEntries[totalCommits-1].Hash
	boundaryHash := ""
	for _, entry := range logEntries {
		if entry.Date.Before(cutoff) {
			boundaryHash = entry.Hash
			break
		}
	}

	for i, j := 0, len(recentHashes)-1; i < j; i, j = i+1, j-1 {
		recentHashes[i], recentHashes[j] = recentHashes[j], recentHashes[i]
	}
	recentCommits := len(recentHashes)

	if compactDoltDryRun {
		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"dry_run":        true,
				"total_commits":  totalCommits,
				"old_commits":    oldCommits,
				"recent_commits": recentCommits,
				"cutoff_days":    compactDoltDays,
				"cutoff_date":    cutoff.Format("2006-01-02"),
				"initial_hash":   initialHash,
				"boundary_hash":  boundaryHash,
			})
		}
		fmt.Printf("DRY RUN — Compact preview\n\n")
		fmt.Printf("  Total commits:  %d\n", totalCommits)
		fmt.Printf("  Old (>%d days): %d (would be squashed into 1)\n", compactDoltDays, oldCommits)
		fmt.Printf("  Recent:         %d (preserved)\n", recentCommits)
		fmt.Printf("  Cutoff date:    %s\n", cutoff.Format("2006-01-02"))
		if oldCommits <= 1 {
			fmt.Printf("\n  Nothing to compact (0-1 old commits).\n")
		} else {
			fmt.Printf("\n  Result: %d commits → %d commits\n", totalCommits, recentCommits+1)
			fmt.Printf("  Run with --force to proceed.\n")
		}
		return nil
	}

	if oldCommits <= 1 {
		if jsonOutput {
			return outputJSON(map[string]interface{}{
				"success":       true,
				"message":       "nothing to compact",
				"total_commits": totalCommits,
				"old_commits":   oldCommits,
			})
		}
		fmt.Printf("Only %d old commit(s). Nothing to compact.\n", oldCommits)
		return nil
	}

	if boundaryHash == "" {
		return HandleError("could not find boundary commit for compaction")
	}

	if !compactDoltForce {
		return HandleErrorWithHint(
			fmt.Sprintf("would squash %d old commits into 1, preserving %d recent commits",
				oldCommits, recentCommits),
			"Use --force to confirm or --dry-run to preview.")
	}

	return HandleErrorWithHint(versioncontrolops.ErrOnlineCompaction.Error(),
		"Keep --dry-run for previews. Back up and stop all writers before using a local embedded workspace for history compaction.")
}

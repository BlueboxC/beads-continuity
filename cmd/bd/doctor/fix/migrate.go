package fix

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
)

// DatabaseVersion fixes database version mismatches by updating metadata in-process.
// For fresh clones (no database), it creates a new Dolt store.
// For existing databases, it updates version metadata directly.
//
// This runs in-process to avoid Dolt lock contention that occurs when spawning
// bd subcommands while the parent process holds database connections. (GH#1805)
func DatabaseVersion(path string) error {
	return DatabaseVersionWithBdVersion(path, "")
}

// DatabaseVersionWithBdVersion is like DatabaseVersion but accepts an explicit
// bd version string for setting the bd_version metadata field.
func DatabaseVersionWithBdVersion(path string, bdVersion string) error {
	beadsDir, err := resolvedWorkspaceBeadsDir(path)
	if err != nil {
		return err
	}

	// Load or create config
	cfg, err := configfile.LoadNativeForDiscovery(beadsDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = configfile.DefaultConfig()
	}

	// Determine database path
	dbPath := cfg.DatabasePath(beadsDir)

	ctx := context.Background()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// No database - create a new Dolt store. Creation is this branch's
		// explicit purpose, so opt out of the dolt.New create-guard
		// (bd-kjfsq: without CreateIfMissing the guard fails the fix with
		// "database not found" on exactly the fresh clones it exists for).
		fmt.Println("  → No database found, creating Dolt store...")

		// Bead-mutating: the branch below imports every issue in the JSONL, so
		// it opens through the activating factory and those creates journal.
		store, err := openBeadMutatingStoreCreating(ctx, beadsDir)
		if err != nil {
			return fmt.Errorf("failed to create database: %w", err)
		}
		defer func() { _ = store.Close() }()

		// Set version metadata if provided
		if bdVersion != "" {
			if err := store.SetLocalMetadata(ctx, "bd_version", bdVersion); err != nil {
				fmt.Printf("  Warning: failed to set bd_version: %v\n", err)
			}
		}

		// A missing local marker does not mean the configured SQL store
		// is new. Do not replay a tracked export over its existing rows.
		jsonlPath := filepath.Join(beadsDir, "issues.jsonl")
		if _, statErr := os.Stat(jsonlPath); statErr == nil {
			count, checkErr := countDatabaseIssuesForRecovery(ctx, store)
			if checkErr != nil {
				return checkErr
			}
			if count == 0 {
				imported, importErr := importJSONLIntoStore(ctx, store, jsonlPath)
				if importErr != nil {
					return fmt.Errorf("failed to import from JSONL: %w", importErr)
				}
				fmt.Printf("  → Imported %d issues from issues.jsonl\n", imported)
			}
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect JSONL export: %w", statErr)
		}

		// Publish the local marker only when the attempted import has
		// finished, so a failed import can still reach this route on repair.
		if err := os.MkdirAll(dbPath, 0o750); err != nil {
			return fmt.Errorf("create Dolt workspace marker: %w", err)
		}
		return nil
	}

	// Database exists - update metadata in-process
	fmt.Println("  → Updating database metadata...")

	store, err := dolt.NewFromConfig(ctx, beadsDir)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = store.Close() }()

	// Update bd_version if provided (clone-local, dolt-ignored)
	if bdVersion != "" {
		if err := store.SetLocalMetadata(ctx, "bd_version", bdVersion); err != nil {
			return fmt.Errorf("failed to set bd_version: %w", err)
		}
	}

	// Detect and set issue_prefix if missing
	prefix, err := store.GetConfig(ctx, "issue_prefix")
	if err != nil || prefix == "" {
		issues, err := store.SearchIssues(ctx, "", types.IssueFilter{})
		if err == nil && len(issues) > 0 {
			detectedPrefix := utils.ExtractIssuePrefix(issues[0].ID)
			if detectedPrefix != "" {
				if err := store.SetConfig(ctx, "issue_prefix", detectedPrefix); err != nil {
					fmt.Printf("  Warning: failed to set issue prefix: %v\n", err)
				} else {
					fmt.Printf("  → Detected and set issue prefix: %s\n", detectedPrefix)
				}
			}
		}
	}

	fmt.Println("  → Metadata updated")
	return nil
}

// SchemaCompatibility fixes schema compatibility issues by updating database metadata
func SchemaCompatibility(path string) error {
	return DatabaseVersion(path)
}

// FreshCloneImport handles the "Fresh Clone" fix: imports JSONL issues into an
// existing (possibly empty) Dolt store. This covers the case where the Database
// fix already created the store but a prior version didn't import.
func FreshCloneImport(path string, bdVersion string) error {
	beadsDir, err := resolvedWorkspaceBeadsDir(path)
	if err != nil {
		return err
	}

	// Check for JSONL file
	jsonlPath := filepath.Join(beadsDir, "issues.jsonl")
	if _, err := os.Stat(jsonlPath); os.IsNotExist(err) {
		return fmt.Errorf("no issues.jsonl found")
	}

	// Check if Dolt store exists
	doltDir := getDatabasePath(beadsDir)
	if _, err := os.Stat(doltDir); os.IsNotExist(err) {
		// No Dolt store — delegate to Database fix which creates store + imports
		return DatabaseVersionWithBdVersion(path, bdVersion)
	}

	// Dolt store exists — check if it already has issues. Bead-mutating: an
	// empty store is imported into below, so it opens through the activating
	// factory and those creates journal.
	ctx := context.Background()
	store, err := openBeadMutatingStore(ctx, beadsDir)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = store.Close() }()

	issueCount, err := countDatabaseIssuesForRecovery(ctx, store)
	if err != nil {
		return err
	}
	if issueCount > 0 {
		fmt.Printf("  → Database already has %d issues, skipping import\n", issueCount)
		return nil
	}

	// Empty store — import from JSONL
	count, importErr := importJSONLIntoStore(ctx, store, jsonlPath)
	if importErr != nil {
		return fmt.Errorf("failed to import from JSONL: %w", importErr)
	}
	fmt.Printf("  → Imported %d issues from issues.jsonl\n", count)
	return nil
}

// importJSONLIntoStore reads a JSONL file and imports all issues into the Dolt store.
// Used by both the Database fix (new store creation) and Fresh Clone fix (empty store).
func importJSONLIntoStore(ctx context.Context, store storage.DoltStorage, jsonlPath string) (int, error) {
	f, err := os.Open(jsonlPath) // #nosec G304 - workspace-controlled path
	if err != nil {
		return 0, fmt.Errorf("failed to open JSONL file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024) // 64MB max line
	var issues []*types.Issue

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var issue types.Issue
		if err := json.Unmarshal([]byte(line), &issue); err != nil {
			return 0, fmt.Errorf("failed to parse issue: %w", err)
		}
		issue.SetDefaults()
		issues = append(issues, &issue)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("failed to scan JSONL: %w", err)
	}

	if len(issues) == 0 {
		return 0, nil
	}

	// Auto-detect and set prefix from first issue
	configuredPrefix, _ := store.GetConfig(ctx, "issue_prefix")
	if strings.TrimSpace(configuredPrefix) == "" {
		firstPrefix := utils.ExtractIssuePrefix(issues[0].ID)
		if firstPrefix != "" {
			if err := store.SetConfig(ctx, "issue_prefix", firstPrefix); err != nil {
				fmt.Printf("  Warning: failed to set issue_prefix: %v\n", err)
			} else {
				fmt.Printf("  → Detected issue prefix: %s\n", firstPrefix)
			}
		}
	}

	// Determine actor for the import
	actor := detectActor()

	err = store.CreateIssuesWithFullOptions(ctx, issues, actor, storage.BatchCreateOptions{
		SkipPrefixValidation: true,
	})
	if err != nil {
		return 0, err
	}

	return len(issues), nil
}

// detectActor returns the best available actor name for automated operations.
func detectActor() string {
	if beadsActor := os.Getenv("BEADS_ACTOR"); beadsActor != "" {
		return beadsActor
	}
	if bdActor := os.Getenv("BD_ACTOR"); bdActor != "" {
		return bdActor
	}
	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		if gitUser := strings.TrimSpace(string(out)); gitUser != "" {
			return gitUser
		}
	}
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	return "bd-doctor"
}

// countDatabaseIssuesForRecovery refuses import when the existing project's
// state cannot be established.
func countDatabaseIssuesForRecovery(ctx context.Context, store *dolt.DoltStore) (int, error) {
	var issueCount int
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM issues").Scan(&issueCount); err != nil {
		return 0, fmt.Errorf("failed to count existing issues before importing issues.jsonl: %w", err)
	}
	return issueCount, nil
}

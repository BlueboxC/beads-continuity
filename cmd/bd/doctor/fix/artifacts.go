package fix

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/configfile"
)

// ClassicArtifacts removes beads classic artifacts found by scanning the path.
// Only removes artifacts that are safe to delete:
// - JSONL export artifacts (not issues.jsonl itself)
// - SQLite WAL/SHM files and backup databases in positively identified Dolt workspaces
// - Positively identified runtime leftovers in redirect-only stubs
//
// It never migrates a .beads directory's metadata, and leaves a .beads
// directory in a format this build cannot open untouched (see
// cleanBeadsDirArtifacts).
func ClassicArtifacts(path string, out io.Writer) error {
	var removed, skipped, errCount int

	// Walk the directory tree looking for .beads/ directories
	err := filepath.Walk(path, func(walkPath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip directories we can't read
		}

		// Skip heavy directories
		base := filepath.Base(walkPath)
		if info.IsDir() && (base == "node_modules" || base == "vendor" || base == "__pycache__") {
			return filepath.SkipDir
		}

		// We only care about directories named ".beads"
		if !info.IsDir() || base != ".beads" {
			return nil
		}

		r, s, e := cleanBeadsDirArtifacts(walkPath, out)
		removed += r
		skipped += s
		errCount += e

		return filepath.SkipDir
	})
	if err != nil {
		return fmt.Errorf("failed to walk directory tree: %w", err)
	}

	// Report summary
	fmt.Fprintf(out, "  Artifact cleanup: %d removed, %d skipped, %d errors\n", removed, skipped, errCount)

	if skipped > 0 {
		fmt.Fprintln(out, "  Skipped items need manual review: retained project material or storage")
	}

	if errCount > 0 {
		return fmt.Errorf("%d artifact(s) could not be removed", errCount)
	}

	return nil
}

// cleanBeadsDirArtifacts cleans artifacts from a single .beads directory.
// Returns counts of removed, skipped, and errored items.
//
// The walk reaches the .beads directories of nested repositories, sub-projects
// and worktrees, and command admission checked only the workspace doctor runs
// in. So beadsDir's metadata is read as discovery reads it, without migrating
// a legacy config.json into metadata.json, and a workspace in a format this
// build cannot open is left byte-identical: configfile.Load would rewrite that
// config.json, dropping keys it does not know, and the cleanup below would
// remove files, the format marker among them, whose meaning this build does
// not know.
func cleanBeadsDirArtifacts(beadsDir string, out io.Writer) (removed, skipped, errCount int) {
	cfg, err := configfile.LoadNativeForDiscovery(beadsDir)
	if err != nil {
		return
	}

	isRedirectExpected := isRedirectExpectedLocation(beadsDir)
	if isRedirectExpected && !CanCleanRedirectDirectory(beadsDir, cfg) {
		return
	}
	hasDolt := hasDoltDir(beadsDir, cfg)

	// 1. Clean JSONL artifacts in dolt-native directories
	if hasDolt {
		r, s, e := cleanJSONLArtifacts(beadsDir, out)
		removed += r
		skipped += s
		errCount += e
	}

	// 2. Clean SQLite artifacts
	r, s, e := cleanSQLiteArtifacts(beadsDir, cfg, out)
	removed += r
	skipped += s
	errCount += e

	// Preserve project material; delete only individually planned runtime files.
	if isRedirectExpected {
		r, k, e := cleanCruftBeadsDirFiles(beadsDir, out)
		removed += r
		skipped += k
		errCount += e
	}

	return
}

// hasDoltDir returns true if the Dolt database directory of the .beads
// directory exists: the one cfg, beadsDir's metadata, resolves (respecting
// dolt_data_dir), or beadsDir/dolt when there is no metadata or it does not
// load (cfg nil).
func hasDoltDir(beadsDir string, cfg *configfile.Config) bool {
	doltPath := filepath.Join(beadsDir, "dolt")
	if cfg != nil {
		doltPath = cfg.DatabasePath(beadsDir)
	}
	info, err := os.Stat(doltPath)
	return err == nil && info.IsDir()
}

// isRedirectExpectedLocation returns true if this .beads directory should contain
// only a redirect file.
func isRedirectExpectedLocation(beadsDir string) bool {
	parent := filepath.Dir(beadsDir)
	grandparent := filepath.Dir(parent)
	grandparentName := filepath.Base(grandparent)
	parentName := filepath.Base(parent)

	// Pattern: */polecats/*/.beads/ (orchestrator worker — backwards compat)
	if grandparentName == "polecats" {
		return true
	}
	// Pattern: */crew/*/.beads/ (orchestrator assistant — backwards compat)
	if grandparentName == "crew" {
		return true
	}
	// Pattern: */refinery/rig/.beads/ (orchestrator processor — backwards compat)
	if parentName == "rig" && grandparentName == "refinery" {
		return true
	}
	// Pattern: .git/beads-worktrees/*/.beads/
	if grandparentName == "beads-worktrees" {
		return true
	}
	// Rig-root .beads/ with a mayor/rig/.beads/ canonical location
	canonicalDir := filepath.Join(parent, "mayor", "rig", ".beads")
	if _, err := os.Stat(canonicalDir); err == nil {
		// Also check it has polecats/ or mayor/ siblings
		for _, sibling := range []string{"mayor", "polecats"} {
			if info, err := os.Stat(filepath.Join(parent, sibling)); err == nil && info.IsDir() {
				return true
			}
		}
	}

	return false
}

// cleanJSONLArtifacts removes stale JSONL files from a dolt-native .beads directory.
func cleanJSONLArtifacts(beadsDir string, out io.Writer) (removed, skipped, errCount int) {
	// Safe to delete (not the primary data source)
	safeFiles := []string{
		"issues.jsonl.new",
	}

	for _, name := range safeFiles {
		path := filepath.Join(beadsDir, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(out, "  Error removing %s: %v\n", path, err)
			errCount++
			continue
		}
		fmt.Fprintf(out, "  Removed: %s (JSONL artifact)\n", path)
		removed++
	}

	// interactions.jsonl - only remove if empty
	interPath := filepath.Join(beadsDir, "interactions.jsonl")
	if info, err := os.Stat(interPath); err == nil {
		if info.Size() == 0 {
			if err := os.Remove(interPath); err != nil {
				fmt.Fprintf(out, "  Error removing %s: %v\n", interPath, err)
				errCount++
			} else {
				fmt.Fprintf(out, "  Removed: %s (empty interactions log)\n", interPath)
				removed++
			}
		} else {
			fmt.Fprintf(out, "  Skip (not empty): %s\n", interPath)
			skipped++
		}
	}

	// issues.jsonl in dolt-native directory - skip (needs manual review)
	issuesPath := filepath.Join(beadsDir, "issues.jsonl")
	if _, err := os.Stat(issuesPath); err == nil {
		fmt.Fprintf(out, "  Skip (needs review): %s (issues.jsonl in dolt-native dir)\n", issuesPath)
		skipped++
	}

	return
}

// SelectsDolt requires an explicit Dolt backend or an existing Dolt repository.
// An empty or unrecognized selector is not proof that SQLite files are obsolete.
// Scanning and cleanup use this same predicate for every nested workspace.
func SelectsDolt(beadsDir string, cfg *configfile.Config) bool {
	if cfg.GetBackend() != configfile.BackendDolt || (cfg != nil && !configfile.IsSupportedBackend(cfg.Backend)) {
		return false
	}
	return (cfg != nil && cfg.Backend == configfile.BackendDolt) || configfile.ExistingDoltDataDir(beadsDir, cfg) != ""
}

// CanCleanRedirectDirectory refuses cleanup when the directory holds storage
// or foreign backend metadata. Its location alone does not make that data cruft.
func CanCleanRedirectDirectory(beadsDir string, cfg *configfile.Config) bool {
	if cfg != nil && cfg.Backend != "" && cfg.Backend != configfile.BackendDolt {
		return false
	}
	if configfile.ExistingDoltDataDir(beadsDir, cfg) != "" {
		return false
	}
	if cfg != nil {
		for _, name := range []string{cfg.Database, cfg.SQLitePath} {
			if name == "" {
				continue
			}
			path := name
			if !filepath.IsAbs(path) {
				path = filepath.Join(beadsDir, path)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				return false
			}
		}
	}
	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if name == "dolt" || name == "embeddeddolt" || name == ".dolt" {
			return false
		}
		for _, ext := range []string{".db", ".sqlite", ".sqlite3"} {
			if strings.HasSuffix(name, ext) || strings.HasSuffix(name, ext+"-wal") || strings.HasSuffix(name, ext+"-shm") {
				return false
			}
		}
	}
	return true
}

// cleanSQLiteArtifacts removes only files in a positively identified Dolt workspace.
func cleanSQLiteArtifacts(beadsDir string, cfg *configfile.Config, out io.Writer) (removed, skipped, errCount int) {
	if !SelectsDolt(beadsDir, cfg) {
		return
	}

	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// This workspace positively selects Dolt; retain beads.db for manual review.
		if name == "beads.db-shm" || name == "beads.db-wal" {
			path := filepath.Join(beadsDir, name)
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(out, "  Error removing %s: %v\n", path, err)
				errCount++
				continue
			}
			fmt.Fprintf(out, "  Removed: %s (SQLite WAL/SHM)\n", path)
			removed++
			continue
		}

		// beads.db - skip (needs manual review, could be active)
		if name == "beads.db" {
			path := filepath.Join(beadsDir, name)
			fmt.Fprintf(out, "  Skip (needs review): %s\n", path)
			skipped++
			continue
		}

		// Backup databases are safe to delete
		if strings.HasPrefix(name, "beads.backup-") && strings.HasSuffix(name, ".db") {
			path := filepath.Join(beadsDir, name)
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(out, "  Error removing %s: %v\n", path, err)
				errCount++
				continue
			}
			fmt.Fprintf(out, "  Removed: %s (pre-migration backup)\n", path)
			removed++
		}
	}

	return
}

// cleanCruftBeadsDirFiles never recursively removes a directory or follows a
// symlink. Scan and cleanup use the same positive runtime-file plan.
func cleanCruftBeadsDirFiles(beadsDir string, out io.Writer) (removed, skipped, errCount int) {
	remove, kept, err := PlanRedirectStub(beadsDir)
	if err != nil {
		return 0, 0, 1
	}
	if len(kept) > 0 {
		fmt.Fprintf(out, "  Skip (needs review): %s: %s\n", beadsDir, strings.Join(kept, ", "))
		skipped++
	}
	for _, path := range remove {
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(out, "  Error removing %s: %v\n", path, err)
			errCount++
			continue
		}
		fmt.Fprintf(out, "  Removed: %s (runtime leftover in redirect-only dir)\n", path)
		removed++
	}
	return
}

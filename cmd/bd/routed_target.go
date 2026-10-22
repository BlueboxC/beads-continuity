package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

// Reject ambiguous or non-native selectors before provisioning a routed target.
func refuseUninitializableTarget(beadsDir string) error {
	if _, err := configfile.LoadNativeForDiscovery(beadsDir); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(beadsDir, "metadata.json")); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	mode, _, err := config.WorkspaceYamlSettingWithLocal(beadsDir, "dolt.mode")
	if err != nil {
		return fmt.Errorf("cannot inspect target dolt.mode: %w", err)
	}
	host, _, err := config.WorkspaceYamlSettingWithLocal(beadsDir, "dolt.host")
	if err != nil {
		return fmt.Errorf("cannot inspect target dolt.host: %w", err)
	}
	if (mode != "" && !strings.EqualFold(mode, configfile.DoltModeEmbedded)) || (mode == "" && !configfile.IsLocalHostString(host)) {
		return fmt.Errorf("target %s has no metadata.json and selects non-embedded Dolt mode or a remote host; initialize it explicitly with its intended server database before routing writes", filepath.Dir(beadsDir))
	}
	if err := guardLegacyUpgradeWorkspace(beadsDir); err != nil {
		return err
	}
	if databases := embeddeddolt.Databases(beadsDir); len(databases) > 1 {
		return fmt.Errorf("target %s has no metadata.json and holds several databases (%s); name the intended database in metadata.json before routing writes", filepath.Dir(beadsDir), strings.Join(databases, ", "))
	}
	return nil
}

func checkRepoTargetInitializable(repoPath string, allowCreate bool) error {
	if repoPath == "." || !allowCreate {
		return nil
	}
	return refuseUninitializableTarget(filepath.Join(repoPath, ".beads"))
}

// The callback runs before prefix or selector writes, preserving dirty targets.
func adoptTargetDatabase(ctx context.Context, beadsDir, database string, source storage.DoltStorage, check func(storage.DoltStorage) error) error {
	target, err := newDoltStore(ctx, &dolt.Config{BeadsDir: beadsDir, Database: database})
	if err != nil {
		return fmt.Errorf("open target database %q: %w", database, err)
	}
	if check != nil {
		err = check(target)
	}
	var projectID string
	if err == nil {
		projectID, err = planningDatabaseSettings(ctx, target, source)
	}
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	cfg := configfile.DefaultConfig()
	cfg.Backend, cfg.DoltDatabase, cfg.DoltMode, cfg.ProjectID = configfile.BackendDolt, database, configfile.DoltModeEmbedded, projectID
	return cfg.Save(beadsDir)
}

func refuseUncommittedTargetChanges(ctx context.Context, target storage.DoltStorage, beadsDir, database string) error {
	if commit, err := writesCommitNow(); err != nil || !commit {
		return err
	}
	status, err := target.Status(ctx)
	if err != nil {
		return err
	}
	var tables []string
	for _, entry := range slices.Concat(status.Staged, status.Unstaged) {
		tables = append(tables, entry.Table)
	}
	if len(tables) == 0 {
		return nil
	}
	slices.Sort(tables)
	return fmt.Errorf("target %s database %q has uncommitted changes (%s); name this database in metadata.json and commit those changes explicitly before retrying; nothing was created", filepath.Dir(beadsDir), database, strings.Join(slices.Compact(tables), ", "))
}

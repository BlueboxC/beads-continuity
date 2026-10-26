package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/routing"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/storage/doltutil"
	dbidentifier "github.com/steveyegge/beads/internal/storage/domain/db"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

func openMigrationPlanningStore(ctx context.Context, planningBeadsDir string, projectStore storage.DoltStorage, actor string) (storage.DoltStorage, error) {
	if err := refuseUninitializablePlanningRepo(planningBeadsDir); err != nil {
		var refusal *planningRepoRefusal
		if errors.As(err, &refusal) {
			return nil, err
		}
		return nil, &planningRepoRefusal{msg: fmt.Sprintf("planning repo %s: %v", filepath.Dir(planningBeadsDir), err), cause: err}
	}
	initialized, err := initializePlanningRepo(ctx, planningBeadsDir, projectStore)
	if err != nil {
		return nil, fmt.Errorf("initialize planning repo: %w", err)
	}
	database, err := migrationTargetDatabase(planningBeadsDir, true)
	if err != nil {
		return nil, err
	}
	planningStore, err := openMigrationTargetStore(ctx, planningBeadsDir, database, true)
	if err != nil {
		return nil, err
	}
	if err := settlePlanningWorkingSet(ctx, planningStore, filepath.Dir(planningBeadsDir), initialized, actor); err != nil {
		_ = planningStore.Close()
		return nil, err
	}
	return planningStore, nil
}

func settlePlanningWorkingSet(ctx context.Context, planningStore storage.DoltStorage, planningRepo string, initialized bool, actor string) error {
	if initialized {
		message := formatDoltAutoCommitMessage("migrate-personal: initialize planning repo", actor, nil)
		if err := commitPlanningWrites(ctx, planningStore, message); err != nil {
			return fmt.Errorf("failed to commit the planning repo's initialization: %w", err)
		}
	}
	return refuseUncommittedPlanningChanges(ctx, planningStore, planningRepo, "")
}

func refuseUncommittedPlanningChanges(ctx context.Context, planningStore storage.DoltStorage, planningRepo, unnamedDatabase string) error {
	commits, err := commitsPlanningWrites(planningStore)
	if err != nil || !commits {
		return err
	}
	status, err := planningStore.Status(ctx)
	if err != nil {
		return fmt.Errorf("failed to read the planning repo's uncommitted changes: %w", err)
	}
	var tables []string
	for _, entry := range slices.Concat(status.Staged, status.Unstaged) {
		tables = append(tables, entry.Table)
	}
	if len(tables) == 0 {
		return nil
	}
	slices.Sort(tables)
	remedy := "commit them there with `bd dolt commit`, then rerun"
	if unnamedDatabase != "" {
		remedy = fmt.Sprintf("bd run there opens the database %[1]q only once a metadata.json names it, so give it a .beads/metadata.json whose dolt_database is %[1]q, commit them there with `bd dolt commit`, then rerun", unnamedDatabase)
	}
	return refusePlanningRepo("planning repo %s has uncommitted changes (%s), which migrate-personal's commit would publish; %s; nothing was moved", planningRepo, strings.Join(slices.Compact(tables), ", "), remedy)
}

func refuseUninitializablePlanningRepo(planningBeadsDir string) error {
	if _, err := os.Lstat(filepath.Join(planningBeadsDir, graphPreviewMarker)); !os.IsNotExist(err) {
		return fmt.Errorf("personal migration requires a native planning workspace; graph-format marker found or unreadable")
	}
	cfg, err := configfile.LoadForDiscovery(planningBeadsDir)
	if err != nil {
		return err
	}
	if err := validateConfiguredBackend(cfg, planningBeadsDir); err != nil {
		return err
	}
	mode, err := cfg.GetGraphMode()
	if err != nil {
		return err
	}
	if mode != configfile.GraphModeDependency {
		return fmt.Errorf("personal migration requires a native planning workspace; graph mode %q is not admitted", mode)
	}

	if redirect := beads.ResolveRedirect(planningBeadsDir); utils.CanonicalizePath(redirect.TargetDir) != utils.CanonicalizePath(planningBeadsDir) {
		return redirectedPlanningRepoError(planningBeadsDir, redirect.TargetDir, redirect.SourceDatabase)
	}
	if _, err := os.Stat(filepath.Join(planningBeadsDir, "metadata.json")); err == nil {
		return nil
	}
	if err := refuseUninitializableTarget(planningBeadsDir); err != nil {
		return err
	}

	if err := guardLegacyUpgradeWorkspace(planningBeadsDir); err != nil {
		return err
	}
	if databases := embeddeddolt.Databases(planningBeadsDir); len(databases) > 1 {
		return refusePlanningRepo("planning repo %s has no metadata.json and holds several databases (%s); give it a metadata.json whose dolt_database names the one to use, then rerun", filepath.Dir(planningBeadsDir), strings.Join(databases, ", "))
	}
	return nil
}

func redirectedPlanningRepoError(planningBeadsDir, target, stubDatabase string) error {
	planningRepo, targetRepo := filepath.Dir(planningBeadsDir), filepath.Dir(target)
	if stubDatabase != "" {
		if targetDatabase, err := migrationTargetDatabase(target, true); err != nil || targetDatabase != stubDatabase {
			return refusePlanningRepo("planning repo %s redirects to %s, where bd uses the database %q that the planning repo names; routing.contributor cannot select that database (set to %s, it selects that repository's own); set routing.contributor to a separate planning repository (e.g. ~/.beads-planning)", planningRepo, targetRepo, stubDatabase, targetRepo)
		}
	}
	return refusePlanningRepo("planning repo %s redirects to %s; set routing.contributor to that repository instead", planningRepo, targetRepo)
}

func initializePlanningRepo(ctx context.Context, planningBeadsDir string, projectStore storage.DoltStorage) (bool, error) {
	_, statErr := os.Stat(filepath.Join(planningBeadsDir, "metadata.json"))
	if statErr != nil {
		if databases := embeddeddolt.Databases(planningBeadsDir); len(databases) == 1 {
			return true, adoptPlanningDatabase(ctx, planningBeadsDir, databases[0], projectStore)
		}
	}
	return statErr != nil, ensureBeadsDirForPath(ctx, filepath.Dir(planningBeadsDir), projectStore, true)
}

func adoptPlanningDatabase(ctx context.Context, planningBeadsDir, database string, projectStore storage.DoltStorage) error {
	return adoptTargetDatabase(ctx, planningBeadsDir, database, projectStore, func(target storage.DoltStorage) error {
		return refuseUncommittedPlanningChanges(ctx, target, filepath.Dir(planningBeadsDir), unnamedPlanningDatabase(database))
	})
}

func planningDatabaseSettings(ctx context.Context, planningStore, projectStore storage.DoltStorage) (string, error) {
	prefix, err := planningStore.GetConfig(ctx, "issue_prefix")
	if err != nil {
		return "", fmt.Errorf("failed to read the planning database's issue_prefix: %w", err)
	}
	if prefix == "" && projectStore != nil {
		if projectPrefix, err := projectStore.GetConfig(ctx, "issue_prefix"); err == nil && projectPrefix != "" {
			if err := planningStore.SetConfig(ctx, "issue_prefix", projectPrefix); err != nil {
				return "", fmt.Errorf("failed to set prefix in planning store: %w", err)
			}
		}
	}
	projectID, err := planningStore.GetMetadata(ctx, "_project_id")
	if err != nil {
		return "", fmt.Errorf("failed to read the planning database's project identity: %w", err)
	}
	if projectID == "" {
		projectID = configfile.GenerateProjectID()
	}
	return projectID, nil
}

func refuseSelfPlanningRepo(planningPath, planningBeadsDir, projectBeadsDir string) error {
	if projectBeadsDir == "" {
		return nil
	}
	redirect := beads.ResolveRedirect(planningBeadsDir)
	planning := utils.CanonicalizePath(redirect.TargetDir)
	project := utils.CanonicalizePath(beads.FollowRedirect(projectBeadsDir))
	// LoadForDiscovery, not Load: Load migrates a legacy config.json to
	// metadata.json, and this check runs before the confirmation prompt and
	// before any format admission, so it must not rewrite either workspace.
	projectCfg, qerr := configfile.LoadForDiscovery(project)
	if planning == project {
		// The database the project's store opened: BEADS_DOLT_SERVER_DATABASE,
		// which preserveRedirectSourceDatabase sets for a launch through a
		// redirect, outranks the shared metadata.json (GetDoltDatabase).
		projectDatabase := configfile.DefaultConfig().GetDoltDatabase()
		if qerr == nil && projectCfg != nil {
			projectDatabase = projectCfg.GetDoltDatabase()
		}
		if redirect.WasRedirected && redirect.SourceDatabase != "" && redirect.SourceDatabase != projectDatabase {
			// Refused here, before the confirmation prompt, rather than by
			// refuseUninitializablePlanningRepo after it.
			return redirectedPlanningRepoError(planningBeadsDir, redirect.TargetDir, redirect.SourceDatabase)
		}
		return fmt.Errorf("planning repo %s is this project's own workspace; set routing.contributor to a separate planning repository (e.g. ~/.beads-planning)", planningPath)
	}
	planningCfg, perr := configfile.LoadForDiscovery(planning)
	if perr == nil && qerr == nil && planningCfg != nil && projectCfg != nil &&
		planningCfg.ProjectID != "" && planningCfg.ProjectID == projectCfg.ProjectID {
		return fmt.Errorf("planning repo %s has this project's project_id (%s), so it is a clone of this project's workspace, not a separate planning repository; set routing.contributor to a separate planning repository (e.g. ~/.beads-planning)", planningPath, planningCfg.ProjectID)
	}
	return nil
}

var migratePersonalCmd = &cobra.Command{
	Use:   "migrate-personal",
	Short: "Move personal planning issues from the project database to your planning repo",
	Long: `Identify issues you created in the project database and move them to your
personal planning repository (~/.beads-planning by default).

This is a one-time migration for contributors who created personal planning
issues before contributor routing was configured.

The command:
  1. Finds all issues in the project database created by your git identity
  2. Shows you the list and asks for confirmation
  3. Moves them to the planning repo configured in routing.contributor

EXAMPLES:
  bd migrate-personal        # Interactive: show list and prompt
  bd migrate-personal -y     # Non-interactive: skip confirmation`,
	GroupID:       "setup",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMigratePersonal,
}

var migratePersonalYes bool

func init() {
	migratePersonalCmd.Flags().BoolVarP(&migratePersonalYes, "yes", "y", false, "Skip confirmation prompt")
	rootCmd.AddCommand(migratePersonalCmd)
}

func runMigratePersonal(cmd *cobra.Command, args []string) error {
	if usesProxiedServer() {
		return HandleErrorRespectJSON("migrate-personal is not supported in proxied-server mode")
	}
	ctx := rootCtx

	identity := getActorWithGit()

	filter := types.IssueFilter{Limit: 0}
	allIssues, err := store.SearchIssues(ctx, "", filter)
	if err != nil {
		return fmt.Errorf("failed to search issues: %w", err)
	}

	var personal []*types.Issue
	for _, issue := range allIssues {
		if issue.CreatedBy == identity {
			personal = append(personal, issue)
		}
	}

	if len(personal) == 0 {
		fmt.Printf("Nothing to migrate — no issues in the project database were created by %s\n", ui.RenderAccent(identity))
		return nil
	}

	fmt.Printf("\n%s personal issues created by %s found in project database:\n\n",
		ui.RenderBold(fmt.Sprintf("%d", len(personal))), ui.RenderAccent(identity))
	for _, issue := range personal {
		fmt.Printf("  %s  %s\n", ui.RenderAccent(issue.ID), ui.SanitizeForTerminal(issue.Title))
	}
	fmt.Println()

	planningRaw := getRoutingConfigValue(ctx, store, "routing.contributor")
	if planningRaw == "" {
		planningRaw = getRoutingConfigValue(ctx, store, "contributor.planning_repo")
	}
	if planningRaw == "" {
		return fmt.Errorf("no planning repository configured; run 'bd init --contributor' or 'bd init' in a fork repo first")
	}
	planningPath := routing.ExpandPath(planningRaw)

	planningBeadsDir := filepath.Join(planningPath, ".beads")
	if _, err := os.Stat(planningBeadsDir); os.IsNotExist(err) {
		return fmt.Errorf("planning repo at %s has no .beads directory; initialize it first with 'bd init'", planningPath)
	}
	if err := refuseSelfPlanningRepo(planningPath, planningBeadsDir, resolveCommandBeadsDir(dbPath)); err != nil {
		return err
	}

	fmt.Printf("Move to planning repo: %s\n\n", ui.RenderAccent(planningPath))

	if !migratePersonalYes && !confirmMigratePersonal(ctx, len(personal)) {
		return nil
	}

	planningStore, err := openMigrationPlanningStore(ctx, planningBeadsDir, store, identity)
	if err != nil {
		return planningOpenError(planningPath, err)
	}
	defer func() { _ = planningStore.Close() }()

	if err := refuseIssuesAlreadyInPlanning(ctx, planningStore, personal, planningPath); err != nil {
		return err
	}

	ids, err := copyIssuesToPlanning(ctx, store, planningStore, personal, identity)
	if err != nil {
		return err
	}

	if err := deleteMovedIssues(ctx, store, ids, planningPath); err != nil {
		return err
	}
	commandDidWrite.Store(true)

	fmt.Printf("\n%s Moved %d %s to %s\n",
		ui.RenderPass("✓"),
		len(personal),
		pluralIssue(len(personal)),
		ui.RenderAccent(planningPath))
	return nil
}

func confirmMigratePersonal(ctx context.Context, n int) bool {
	fmt.Printf("Move %d issues? [Y/n]: ", n)
	reader := bufio.NewReader(os.Stdin)
	response, err := readLineWithContext(ctx, reader, os.Stdin)
	if err != nil {
		if isCanceled(err) {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return false
		}
		response = ""
	}
	response = strings.TrimSpace(strings.ToLower(response))
	if response == "n" || response == "no" {
		fmt.Println("Aborted — no changes made.")
		return false
	}
	return true
}

func refuseIssuesAlreadyInPlanning(ctx context.Context, planningStore storage.DoltStorage, personal []*types.Issue, planningPath string) error {
	for _, issue := range personal {
		_, err := planningStore.GetIssue(ctx, issue.ID)
		switch {
		case err == nil:
			if isPlanningRepoMirror(issue, planningPath) {
				return repoSyncMirrorRefusal(issue, planningPath, runtime.GOOS)
			}
			return fmt.Errorf("issue %s already exists in the planning repo %s; nothing was moved", issue.ID, planningPath)
		case !errors.Is(err, storage.ErrNotFound):
			return fmt.Errorf("failed to check the planning DB for %s: %w", issue.ID, err)
		}
	}
	return nil
}

func isPlanningRepoMirror(issue *types.Issue, planningPath string) bool {
	if issue.SourceRepo == "" || issue.SourceRepo == "." {
		return false
	}
	return utils.CanonicalizePath(routing.ExpandPath(issue.SourceRepo)) == utils.CanonicalizePath(planningPath)
}

func repoSyncMirrorRefusal(issue *types.Issue, planningPath, goos string) error {
	repo, ok := shellQuotedFor(goos, issue.SourceRepo)
	sourceRepo, pathNote := issue.SourceRepo, ""
	if !ok {
		sourceRepo, repo, pathNote = escapedValue(issue.SourceRepo), "<path>", ", with that path for <path>"
	}
	return fmt.Errorf("issue %s already exists in the planning repo %s; the project copy was imported from %s by `bd repo sync` (remove those copies with `bd repo remove %s`, then rerun, and run `bd repo add %s` again if you want them hydrated%s); nothing was moved",
		printedValue(issue.ID), printedValue(planningPath), sourceRepo, repo, repo, pathNote)
}

// Local wrappers retain the existing command-facing printer contract.
func printedValue(value string) string                 { return doltutil.PrintedValue(value) }
func escapedValue(value string) string                 { return doltutil.EscapedValue(value) }
func printsAsItself(value string) bool                 { return doltutil.PrintsAsItself(value) }
func shellQuotedFor(goos, value string) (string, bool) { return doltutil.ShellQuotedFor(goos, value) }

func copyIssuesToPlanning(ctx context.Context, src, dst storage.DoltStorage, personal []*types.Issue, actor string) ([]string, error) {
	var created []string
	rollback := func(err error) ([]string, error) {
		if len(created) == 0 {
			return nil, err
		}
		if _, rollbackErr := dst.DeleteIssues(ctx, created, false, false, false); rollbackErr != nil {
			return nil, fmt.Errorf("%w; rolling the copy back failed too, so the planning DB still holds %s: %w", err, strings.Join(created, ", "), rollbackErr)
		}
		return nil, err
	}
	for _, issue := range personal {
		if err := dst.CreateIssue(ctx, issue, actor); err != nil {
			return rollback(fmt.Errorf("failed to copy issue %s to planning DB: %w", issue.ID, err))
		}
		created = append(created, issue.ID)
	}
	for _, issue := range personal {
		if err := copyIssueRelations(ctx, src, dst, issue, actor); err != nil {
			return rollback(fmt.Errorf("failed to copy %s to planning DB: %w", issue.ID, err))
		}
	}
	err := commitPlanningWrites(ctx, dst, formatDoltAutoCommitMessage("migrate-personal", actor, created))
	switch {
	case errors.Is(err, storage.ErrCommitIndeterminate):
		return nil, fmt.Errorf("committing the copy in the planning repo may have failed: %w; the copy of %s is left there and nothing was deleted from the project DB, so run `bd dolt commit` in the planning repo, then resolve the duplicates manually", err, strings.Join(created, ", "))
	case err != nil:
		return rollback(fmt.Errorf("failed to commit the copy in the planning repo: %w", err))
	}
	return created, nil
}

func commitsPlanningWrites(dst storage.DoltStorage) (bool, error) {
	if !isEmbeddedStore(dst) {
		return false, nil
	}
	mode, err := getDoltAutoCommitMode()
	return mode == doltAutoCommitOn, err
}

func commitPlanningWrites(ctx context.Context, dst storage.DoltStorage, message string) error {
	commits, err := commitsPlanningWrites(dst)
	if err != nil || !commits {
		return err
	}
	return maybeAutoCommitStore(ctx, dst, doltAutoCommitParams{MessageOverride: message})
}

func deleteMovedIssues(ctx context.Context, projectStore storage.DoltStorage, ids []string, planningPath string) error {
	opsCtx, err := issueOpsContext(ctx)
	if err == nil {
		_, err = projectStore.DeleteIssues(opsCtx, ids, false, false, false)
	}
	if err != nil {
		return fmt.Errorf("issues copied to planning repo %s but deleting them from the project DB failed; resolve the duplicate manually: %w", planningPath, err)
	}
	return nil
}

func copyIssueRelations(ctx context.Context, src, dst storage.DoltStorage, issue *types.Issue, actor string) error {
	labels, err := src.GetLabels(ctx, issue.ID)
	if err != nil {
		return fmt.Errorf("read labels for %s: %w", issue.ID, err)
	}
	for _, label := range labels {
		if err := dst.AddLabel(ctx, issue.ID, label, actor); err != nil {
			return fmt.Errorf("copy label %q for %s: %w", label, issue.ID, err)
		}
	}

	deps, err := src.GetDependencyRecords(ctx, issue.ID)
	if err != nil {
		return fmt.Errorf("read dependencies for %s: %w", issue.ID, err)
	}
	for _, dep := range deps {
		if err := dst.AddDependency(ctx, dep, actor); err != nil {
			return fmt.Errorf("copy dependency %s→%s: %w", dep.IssueID, dep.DependsOnID, err)
		}
	}

	comments, err := src.GetIssueComments(ctx, issue.ID)
	if err != nil {
		return fmt.Errorf("read comments for %s: %w", issue.ID, err)
	}
	for _, c := range comments {
		if _, err := dst.ImportIssueComment(ctx, issue.ID, c.Author, c.Text, c.CreatedAt); err != nil {
			return fmt.Errorf("copy comment for %s: %w", issue.ID, err)
		}
	}

	return nil
}

func pluralIssue(n int) string {
	if n == 1 {
		return "issue"
	}
	return "issues"
}

// migrationTargetDatabase pins the destination against launcher overrides.
func migrationTargetDatabase(beadsDir string, _ bool) (string, error) {
	cfg, err := configfile.LoadForDiscovery(beadsDir)
	if err != nil {
		return "", err
	}
	if cfg == nil || strings.TrimSpace(cfg.DoltDatabase) == "" {
		return "", fmt.Errorf("planning repo %s has no dolt_database; bootstrap it there before migrating", filepath.Dir(beadsDir))
	}
	if err := dbidentifier.ValidateIdentifier(cfg.DoltDatabase); err != nil {
		return "", err
	}
	return cfg.DoltDatabase, nil
}

// openMigrationTargetStore opens only the planning workspace's selected database.
func openMigrationTargetStore(ctx context.Context, beadsDir, database string, _ bool) (s storage.DoltStorage, err error) {
	defer func() { s, err = activateEventsJournalStore(beadsDir, s, err) }()
	cfg, err := configfile.LoadForDiscovery(beadsDir)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("planning repository has no metadata")
	}
	if cfg.IsDoltProxiedServerMode() {
		return nil, errProxiedStoreUnrouted()
	}
	if cfg.IsDoltServerMode() {
		return dolt.NewFromConfigWithOptions(ctx, beadsDir, &dolt.Config{Database: database})
	}
	if !slices.Contains(embeddeddolt.Databases(beadsDir), database) {
		return nil, fmt.Errorf("planning database %q is missing in %s; run bd bootstrap there first", database, beadsDir)
	}
	return newDoltStore(ctx, &dolt.Config{BeadsDir: beadsDir, Database: database})
}

func unnamedPlanningDatabase(database string) string {
	if database == configfile.DefaultDoltDatabase {
		return ""
	}
	return database
}

type planningRepoRefusal struct {
	msg   string
	cause error
}

func (r *planningRepoRefusal) Unwrap() error { return r.cause }

func (r *planningRepoRefusal) Error() string { return r.msg }
func refusePlanningRepo(format string, args ...any) error {
	return &planningRepoRefusal{msg: fmt.Sprintf(format, args...)}
}
func planningOpenError(planningPath string, err error) error {
	var refusal *planningRepoRefusal
	if errors.As(err, &refusal) {
		return refusal
	}
	return fmt.Errorf("failed to open planning store at %s: %w", planningPath, err)
}

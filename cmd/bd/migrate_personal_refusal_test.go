//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

// Adapted from Julian Knutsen's Beads PR #7770; retain real embedded destinations.
func TestEmbeddedCopyIssuesToPlanning_commitsEmbeddedPlanningInServerMode(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	// Not parallel: the command's mode is process state.
	saveStorageMode(t)
	serverMode = true

	bd := buildEmbeddedBD(t)
	_, srcBeads, _ := bdInit(t, bd, "--prefix", "mp")
	_, dstBeads, _ := bdInit(t, bd, "--prefix", "mp")
	src := openStore(t, srcBeads, "mp")
	dst := openStore(t, dstBeads, "mp")
	ctx := t.Context()

	issue := &types.Issue{
		ID:        "mp-200",
		Title:     "Personal issue moved by a server-mode project",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		CreatedBy: "alice",
	}
	if err := src.CreateIssue(ctx, issue, "alice"); err != nil {
		t.Fatalf("seed src CreateIssue: %v", err)
	}
	if err := src.AddLabel(ctx, issue.ID, "personal", "alice"); err != nil {
		t.Fatalf("seed src AddLabel: %v", err)
	}

	// Code under test.
	if _, err := copyIssuesToPlanning(ctx, src, dst, []*types.Issue{issue}, "alice"); err != nil {
		t.Fatalf("copyIssuesToPlanning: %v", err)
	}

	status, err := dst.Status(ctx)
	if err != nil {
		t.Fatalf("planning store Status: %v", err)
	}
	if dirty := slices.Concat(status.Staged, status.Unstaged); len(dirty) != 0 {
		t.Errorf("planning store has uncommitted changes after the copy in server mode: %v", dirty)
	}
	commits, err := dst.Log(ctx, 0)
	if err != nil {
		t.Fatalf("planning store Log: %v", err)
	}
	want := formatDoltAutoCommitMessage("migrate-personal", "alice", []string{issue.ID})
	if !slices.ContainsFunc(commits, func(c storage.CommitInfo) bool { return c.Message == want }) {
		t.Errorf("planning store history has no %q commit", want)
	}
}

// TestCommitPlanningWritesSkipsStoresThatAreNotEmbedded verifies that phase 1
// leaves a planning store that is not embedded to commit its own writes, even
// when bd runs embedded: a server-mode store commits each write itself, and a
// commit of its whole working set would sweep another writer's changes in.
func TestCommitPlanningWritesSkipsStoresThatAreNotEmbedded(t *testing.T) {
	saveStorageMode(t)
	serverMode = false

	fake := &fakeCommitPendingStore{}
	if err := commitPlanningWrites(context.Background(), fake, formatDoltAutoCommitMessage("migrate-personal", "tester", []string{"pl-1"})); err != nil {
		t.Fatalf("commitPlanningWrites: %v", err)
	}
	if fake.commitCalls != 0 {
		t.Errorf("Commit calls = %d, want 0 for a planning store that is not embedded", fake.commitCalls)
	}
}

// indeterminateCommitStore is an embedded planning store whose Commit fails as
// an embedded commit does when it cannot tell whether DOLT_COMMIT landed
// (storage.ErrCommitIndeterminate). It counts the DeleteIssues calls a
// rollback would make. Unwrap lets isEmbeddedStore see the store it wraps.
type indeterminateCommitStore struct {
	storage.DoltStorage
	deleteCalls int
}

func (s *indeterminateCommitStore) Unwrap() storage.DoltStorage { return s.DoltStorage }

func (s *indeterminateCommitStore) Commit(context.Context, string) error {
	return fmt.Errorf("dolt commit: driver: bad connection: %w", storage.ErrCommitIndeterminate)
}

func (s *indeterminateCommitStore) DeleteIssues(ctx context.Context, ids []string, cascade, force, dryRun bool) (*types.DeleteIssuesResult, error) {
	s.deleteCalls++
	return s.DoltStorage.DeleteIssues(ctx, ids, cascade, force, dryRun)
}

// TestEmbeddedCopyIssuesToPlanning_keepsCopyAfterIndeterminateCommit verifies
// that phase 1 does not roll back a copy whose commit may have landed
// (storage.ErrCommitIndeterminate): an uncommitted delete of its rows would
// undo that commit at the planning repo's next one. The error names bd dolt
// commit and the issues now in both repos, and the copy stays in the planning
// store for it to settle.
func TestEmbeddedCopyIssuesToPlanning_keepsCopyAfterIndeterminateCommit(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	// Not parallel: the command's dolt.auto-commit policy is process state.
	saveStorageMode(t)

	bd := buildEmbeddedBD(t)
	_, srcBeads, _ := bdInit(t, bd, "--prefix", "mp")
	_, dstBeads, _ := bdInit(t, bd, "--prefix", "mp")
	src := openStore(t, srcBeads, "mp")
	dst := &indeterminateCommitStore{DoltStorage: openStore(t, dstBeads, "mp")}
	ctx := t.Context()

	issue := &types.Issue{
		ID:        "mp-300",
		Title:     "Personal issue whose planning commit may have landed",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		CreatedBy: "alice",
	}
	if err := src.CreateIssue(ctx, issue, "alice"); err != nil {
		t.Fatalf("seed src CreateIssue: %v", err)
	}

	// Code under test.
	_, err := copyIssuesToPlanning(ctx, src, dst, []*types.Issue{issue}, "alice")
	if !errors.Is(err, storage.ErrCommitIndeterminate) {
		t.Fatalf("copyIssuesToPlanning err = %v, want storage.ErrCommitIndeterminate", err)
	}
	for _, want := range []string{"the copy of " + issue.ID + " is left there", "run `bd dolt commit` in the planning repo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("copyIssuesToPlanning err = %v, want it to contain %q", err, want)
		}
	}
	if dst.deleteCalls != 0 {
		t.Errorf("DeleteIssues calls = %d after an indeterminate commit, want 0", dst.deleteCalls)
	}
	if _, err := dst.GetIssue(ctx, issue.ID); err != nil {
		t.Errorf("planning copy %s gone after an indeterminate commit: %v", issue.ID, err)
	}
}

func requireMigratePersonalRefused(t *testing.T, bd, dir, want string) string {
	t.Helper()
	out, err := bdMigratePersonal(t, bd, dir, "--yes")
	if err == nil {
		t.Fatalf("bd migrate-personal --yes succeeded, want it refused:\n%s", out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("bd migrate-personal --yes output does not contain %q:\n%s", want, out)
	}
	if strings.Contains(out, "Moved") {
		t.Errorf("bd migrate-personal --yes reported a move while refusing:\n%s", out)
	}
	if strings.Contains(out, "Usage:") {
		t.Errorf("bd migrate-personal --yes printed cobra's usage block after a refusal:\n%s", out)
	}
	return out
}

// requireCleanPlanningRefusal asserts that out carries the refusal of
// planningDir as its own Error line, not prefixed as a failed planning-store
// open: nothing failed to open.
func requireCleanPlanningRefusal(t *testing.T, out, planningDir string) {
	t.Helper()
	if strings.Contains(out, "failed to open planning store") || !strings.Contains(out, "Error: planning repo "+planningDir) {
		t.Errorf("bd migrate-personal --yes output does not refuse with an Error line starting %q:\n%s", "Error: planning repo "+planningDir, out)
	}
}

// requireStillInProject asserts issueID is still in the project database.
func requireStillInProject(t *testing.T, projectBeadsDir, projectDB, issueID string) {
	t.Helper()
	if _, err := openStore(t, projectBeadsDir, projectDB).GetIssue(t.Context(), issueID); err != nil {
		t.Errorf("issue %s left the project database %q after a refused migrate-personal: %v", issueID, projectDB, err)
	}
}

// TestEmbeddedMigratePersonal_refusesPlanningRepoThatIsTheProject verifies that
// a planning repo resolving to the project's own workspace is refused before
// anything moves. Copying an issue onto itself and then deleting it from the
// project deletes the only copy while reporting it moved.
func TestEmbeddedMigratePersonal_refusesSharedRootRigAsARedirect(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	_, townBeadsDir, _ := bdInit(t, bd, "--prefix", "hq")
	// The project rig's database, beside the shared root's own.
	s, err := embeddeddolt.Open(t.Context(), townBeadsDir, "rga", "main")
	if err != nil {
		t.Fatalf("create the project rig's database: %v", err)
	}
	err = s.SetConfig(t.Context(), "issue_prefix", "rga")
	if closeErr := s.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("set the project rig's issue_prefix: %v", err)
	}
	// rig returns a repository whose .beads redirects to the shared root and
	// names database there.
	rig := func(database string) string {
		dir := t.TempDir()
		initGitRepoAt(t, dir)
		beadsDir := filepath.Join(dir, ".beads")
		if err := os.MkdirAll(beadsDir, 0o750); err != nil {
			t.Fatalf("create rig .beads: %v", err)
		}
		cfg := configfile.DefaultConfig()
		cfg.Backend = configfile.BackendDolt
		cfg.DoltMode = configfile.DoltModeEmbedded
		cfg.DoltDatabase = database
		if err := cfg.Save(beadsDir); err != nil {
			t.Fatalf("write rig metadata.json: %v", err)
		}
		if err := os.WriteFile(filepath.Join(beadsDir, "redirect"), []byte(townBeadsDir+"\n"), 0o600); err != nil {
			t.Fatalf("write rig redirect: %v", err)
		}
		return dir
	}
	project, planning := rig("rga"), rig("rgb")
	if out, err := bdRunWithFlockRetry(t, bd, project, "config", "set", "routing.contributor", planning); err != nil {
		t.Fatalf("bd config set routing.contributor: %v\n%s", err, out)
	}
	personal := bdCreate(t, bd, project, "Personal issue in a shared-root rig")

	out := requireMigratePersonalRefused(t, bd, project, `, where bd uses the database "rgb" that the planning repo names; routing.contributor cannot select that database`)
	if strings.Contains(out, "this project's own workspace") {
		t.Errorf("a rig beside the project under a shared root was refused as the project's own workspace:\n%s", out)
	}
	requireCleanPlanningRefusal(t, out, planning)
	// Refused before the confirmation prompt, which follows this line.
	if strings.Contains(out, "Move to planning repo") {
		t.Errorf("a rig beside the project under a shared root was refused after the confirmation prompt:\n%s", out)
	}
	requireStillInProject(t, townBeadsDir, "rga", personal.ID)
}

// TestRefuseSelfPlanningRepo pins which planning repos refuseSelfPlanningRepo
// refuses. One whose .beads, after any redirect, is the project's is the
// project's own workspace, unless it is a redirect stub naming a database
// other than the one the project's store uses there: a rig beside the
// project under a shared root, which it refuses as a redirect instead. A
// separate directory carrying the project's
// project_id is a clone of the project, and is refused as one.
func TestRefuseSelfPlanningRepo(t *testing.T) {
	root := t.TempDir()
	// workspace writes a .beads under root/name whose metadata.json names
	// database and projectID, redirecting to target when it is set.
	workspace := func(name, database, projectID, target string) string {
		beadsDir := filepath.Join(root, name, ".beads")
		if err := os.MkdirAll(beadsDir, 0o750); err != nil {
			t.Fatalf("create %s: %v", beadsDir, err)
		}
		cfg := configfile.DefaultConfig()
		cfg.Backend = configfile.BackendDolt
		cfg.DoltMode = configfile.DoltModeServer
		cfg.DoltDatabase = database
		cfg.ProjectID = projectID
		if err := cfg.Save(beadsDir); err != nil {
			t.Fatalf("write %s metadata.json: %v", name, err)
		}
		if target != "" {
			if err := os.WriteFile(filepath.Join(beadsDir, "redirect"), []byte(target+"\n"), 0o600); err != nil {
				t.Fatalf("write %s redirect: %v", name, err)
			}
		}
		return beadsDir
	}
	// A shared root whose rigs redirect to its .beads and each name a
	// database of their own (preserveRedirectSourceDatabase), and a plain
	// project with stubs redirecting to it.
	town := workspace("town", "hq", "town-id", "")
	project := workspace("project", "proj", "project-id", "")
	for _, tc := range []struct {
		name string
		// projectBeadsDir is the project's .beads as the command resolved
		// it, and launchDatabase the BEADS_DOLT_SERVER_DATABASE it opened
		// its store under ("" for none).
		projectBeadsDir, launchDatabase string
		planningBeadsDir                string
		want                            string
	}{
		{name: "the project itself", projectBeadsDir: project, planningBeadsDir: project,
			want: "is this project's own workspace"},
		{name: "a stub naming no database", projectBeadsDir: project,
			planningBeadsDir: workspace("stub", "", "", project), want: "is this project's own workspace"},
		{name: "a stub naming the project's database", projectBeadsDir: project,
			planningBeadsDir: workspace("worktree", "proj", "", project), want: "is this project's own workspace"},
		{name: "a stub naming a database of its own", projectBeadsDir: project,
			planningBeadsDir: workspace("rig", "rig", "", project),
			want:             `where bd uses the database "rig" that the planning repo names; routing.contributor cannot select that database`},
		{name: "the project's own rig under a shared root", projectBeadsDir: town, launchDatabase: "rga",
			planningBeadsDir: workspace("rigA", "rga", "", town), want: "is this project's own workspace"},
		{name: "a rig beside the project under a shared root", projectBeadsDir: town, launchDatabase: "rga",
			planningBeadsDir: workspace("rigB", "rgb", "", town),
			want:             `where bd uses the database "rgb" that the planning repo names; routing.contributor cannot select that database`},
		// "." run at the shared root while BEADS_DOLT_SERVER_DATABASE selects
		// a rig's database: a planning repo that is no stub is where the
		// project's store is, so it stays refused.
		{name: "the shared root itself", projectBeadsDir: town, launchDatabase: "rga",
			planningBeadsDir: town, want: "is this project's own workspace"},
		{name: "a clone carrying the project's project_id", projectBeadsDir: project,
			planningBeadsDir: workspace("clone", "proj", "project-id", ""),
			want:             "has this project's project_id (project-id), so it is a clone of this project's workspace"},
		{name: "a separate planning repository", projectBeadsDir: project,
			planningBeadsDir: workspace("planning", "pl", "planning-id", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BEADS_DOLT_SERVER_DATABASE", tc.launchDatabase)
			err := refuseSelfPlanningRepo(filepath.Dir(tc.planningBeadsDir), tc.planningBeadsDir, tc.projectBeadsDir)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refuseSelfPlanningRepo = %v, want nil", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("refuseSelfPlanningRepo = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestEmbeddedMigratePersonal_refusesMetadataWithoutDoltDatabase verifies that
// a planning repo whose metadata.json names no dolt_database is refused before
// anything moves, and that no database is created in it: the launcher's
// database name would otherwise choose one.
func createForkSetupPlanningDatabase(t *testing.T, planningBeadsDir string, fill func(s *embeddeddolt.EmbeddedDoltStore) error) {
	t.Helper()
	s, err := embeddeddolt.Open(t.Context(), planningBeadsDir, "beads", "main")
	if err != nil {
		t.Fatalf("create the fork-setup planning database: %v", err)
	}
	if fill != nil {
		if err := fill(s); err != nil {
			_ = s.Close()
			t.Fatalf("fill the fork-setup planning database: %v", err)
		}
		if err := s.Commit(t.Context(), "fill the fork-setup planning database"); err != nil {
			_ = s.Close()
			t.Fatalf("commit the fork-setup planning database: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close the fork-setup planning database: %v", err)
	}
}

// TestEmbeddedMigratePersonal_initializesContributorPlanningRepos verifies that
// the planning repos contributor setup leaves behind are given a metadata.json
// and then migrated into. The wizard's empty .beads (runContributorWizard) is
// initialized the way a routed bd create initializes it, with a database named
// after the project prefix. Fork setup's database, which has no metadata.json
// and no issue_prefix (autoConfigureForkContributor), is adopted rather than
// hidden behind a new one: it takes the project's prefix, and when it already
// holds issues they stay visible, under its own prefix and project identity.
// An initialization that writes to the database is committed on its own,
// before the copy's commit.
func TestEmbeddedMigratePersonal_refusesUncommittedPlanningChanges(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	requireNoMetadata := func(t *testing.T, planningBeadsDir string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(planningBeadsDir, "metadata.json")); !os.IsNotExist(err) {
			t.Errorf("planning metadata.json written by a refused migrate-personal: stat err = %v, want not exist", err)
		}
	}
	const commitRemedy = "commit them there with `bd dolt commit`, then rerun; nothing was moved"
	bd := buildEmbeddedBD(t)
	for _, tc := range []struct {
		name   string
		prefix string
		// planningRepo returns a planning repo whose working set holds a
		// change, and the database that holds it.
		planningRepo func(t *testing.T, dir string) (planningDir, database string)
		// remedy is the end of the refusal; nameDatabase is whether it first
		// asks for a metadata.json naming the database, which the test then
		// writes before running bd dolt commit.
		remedy       string
		nameDatabase bool
		// check, when set, makes the case's own assertions after the refusal.
		check func(t *testing.T, planningBeadsDir string)
	}{
		{
			name: "initialized planning repo", prefix: "mpu", remedy: commitRemedy,
			planningRepo: func(t *testing.T, dir string) (string, string) {
				planningDir := setupContributorRouting(t, bd, dir)
				// A write the planning repo's own dolt.auto-commit left
				// uncommitted.
				bdCreate(t, bd, planningDir, "Planning issue left uncommitted", "--dolt-auto-commit", "off")
				return planningDir, "pl"
			},
		},
		{
			// The shape an ensureBeadsDirForPath interrupted before it saved
			// metadata.json leaves, or a hand-removed metadata.json.
			name: "database not named beads without metadata", prefix: "mpn", nameDatabase: true,
			remedy: `bd run there opens the database "pl" only once a metadata.json names it, so give it a .beads/metadata.json whose dolt_database is "pl", ` + commitRemedy,
			planningRepo: func(t *testing.T, dir string) (string, string) {
				planningDir := setupContributorRouting(t, bd, dir)
				bdCreate(t, bd, planningDir, "Planning issue left uncommitted", "--dolt-auto-commit", "off")
				if err := os.Remove(filepath.Join(planningDir, ".beads", "metadata.json")); err != nil {
					t.Fatalf("remove planning metadata.json: %v", err)
				}
				return planningDir, "pl"
			},
			check: requireNoMetadata,
		},
		{
			name: "fork setup database", prefix: "mpe", remedy: commitRemedy,
			planningRepo: func(t *testing.T, dir string) (string, string) {
				planningDir := t.TempDir()
				planningBeadsDir := filepath.Join(planningDir, ".beads")
				if err := os.MkdirAll(planningBeadsDir, 0o750); err != nil {
					t.Fatalf("create planning .beads: %v", err)
				}
				createForkSetupPlanningDatabase(t, planningBeadsDir, nil)
				s, err := embeddeddolt.Open(t.Context(), planningBeadsDir, "beads", "main")
				if err != nil {
					t.Fatalf("open the fork-setup planning database: %v", err)
				}
				err = s.SetMetadata(t.Context(), "_project_id", "uncommitted-fork-setup-project")
				if closeErr := s.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					t.Fatalf("leave a change uncommitted in the fork-setup planning database: %v", err)
				}
				if out, err := bdRunWithFlockRetry(t, bd, dir, "config", "set", "routing.contributor", planningDir); err != nil {
					t.Fatalf("bd config set routing.contributor: %v\n%s", err, out)
				}
				return planningDir, "beads"
			},
			check: func(t *testing.T, planningBeadsDir string) {
				requireNoMetadata(t, planningBeadsDir)
				if prefix, err := openStore(t, planningBeadsDir, "beads").GetConfig(t.Context(), "issue_prefix"); err != nil || prefix != "" {
					t.Errorf("planning database issue_prefix = %q (err %v) after a refused migrate-personal, want it unset", prefix, err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, beadsDir, _ := bdInit(t, bd, "--prefix", tc.prefix)
			planningDir, database := tc.planningRepo(t, dir)
			planningBeadsDir := filepath.Join(planningDir, ".beads")
			commitsBefore, dirtyBefore := planningLogCountAndDirtyTables(t, planningBeadsDir, database)
			if len(dirtyBefore) == 0 {
				t.Fatal("the planning repo has no uncommitted changes to refuse")
			}
			const title = "Personal issue for a planning repo with uncommitted changes"
			personal := bdCreate(t, bd, dir, title)

			// The refusal is the Error line alone: no "failed to open planning
			// store" prefix, since nothing failed to open.
			out := requireMigratePersonalRefused(t, bd, dir, "Error: planning repo "+planningDir+" has uncommitted changes (")
			if !strings.Contains(out, tc.remedy) {
				t.Errorf("bd migrate-personal --yes output does not name the remedy %q:\n%s", tc.remedy, out)
			}
			requireStillInProject(t, beadsDir, tc.prefix, personal.ID)
			requireInNoPlanningDatabase(t, planningBeadsDir, personal.ID)
			if commits, dirty := planningLogCountAndDirtyTables(t, planningBeadsDir, database); commits != commitsBefore || !slices.Equal(dirty, dirtyBefore) {
				t.Errorf("planning repo after a refused migrate-personal: %d commits, uncommitted %v; want unchanged %d, %v", commits, dirty, commitsBefore, dirtyBefore)
			}
			if tc.check != nil {
				tc.check(t, planningBeadsDir)
			}

			// The remedy, as the refusal prints it.
			if tc.nameDatabase {
				raw, err := json.Marshal(map[string]string{"dolt_database": database})
				if err != nil {
					t.Fatalf("encode planning metadata.json: %v", err)
				}
				if err := os.WriteFile(filepath.Join(planningBeadsDir, "metadata.json"), raw, 0o600); err != nil {
					t.Fatalf("write planning metadata.json: %v", err)
				}
			}
			if out, err := bdRunWithFlockRetry(t, bd, planningDir, "dolt", "commit"); err != nil {
				t.Fatalf("bd dolt commit in the planning repo: %v\n%s", err, out)
			}
			out = requireMigratePersonal(t, bd, dir)
			if !strings.Contains(out, "Moved 1 issue") {
				t.Errorf("migrate-personal after bd dolt commit does not report moving 1 issue:\n%s", out)
			}
			moved, err := openStore(t, planningBeadsDir, database).GetIssue(t.Context(), personal.ID)
			if err != nil || moved.Title != title {
				t.Errorf("planning issue %s after the rerun = %v (err %v), want %q", personal.ID, moved, err, title)
			}
		})
	}
}

// TestEmbeddedMigratePersonal_batchLeavesTheMoveUncommitted verifies that with
// dolt.auto-commit batch the move makes no Dolt commit in either repository:
// the copy and the delete both wait in their working sets for bd dolt commit,
// as every other batch write does. A planning repo that already holds
// uncommitted changes is not refused then, since no commit of the migration's
// publishes them.
func requireInNoPlanningDatabase(t *testing.T, beadsDir, issueID string) {
	t.Helper()
	for _, database := range embeddeddolt.Databases(beadsDir) {
		db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), filepath.Join(beadsDir, "embeddeddolt"), database, "main")
		if err != nil {
			t.Fatalf("OpenSQL %s: %v", database, err)
		}
		var count int
		err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM issues WHERE id = ?", issueID).Scan(&count)
		_ = cleanup()
		if err != nil {
			t.Fatalf("query issues in %s: %v", database, err)
		}
		if count != 0 {
			t.Errorf("planning database %q holds %s after a refused migrate-personal", database, issueID)
		}
	}
}

// migratePersonalStderr runs "bd migrate-personal --yes" in dir and returns its
// stderr alone and its exit code.
func requireMigratePersonal(t *testing.T, bd, dir string, extraEnv ...string) string {
	t.Helper()
	cmd := exec.Command(bd, "migrate-personal", "--yes")
	cmd.Dir = dir
	cmd.Env = append(bdEnv(dir), extraEnv...)
	outBytes, err := cmd.CombinedOutput()
	out := string(outBytes)
	if err != nil {
		t.Fatalf("bd migrate-personal --yes failed: %v\noutput:\n%s", err, out)
	}
	for _, symptom := range []string{"Dolt server unreachable", "no database name configured"} {
		if strings.Contains(out, symptom) {
			t.Errorf("bd migrate-personal --yes output contains %q:\n%s", symptom, out)
		}
	}
	return out
}

// requireMovedToPlanning asserts issueID now lives in the planning repo's own
// database (and that no other database was created there) and is gone from
// the project database.
func planningLogCountAndDirtyTables(t *testing.T, beadsDir, database string) (commits int, dirty []string) {
	t.Helper()
	db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), filepath.Join(beadsDir, "embeddeddolt"), database, "main")
	if err != nil {
		t.Fatalf("OpenSQL %s: %v", database, err)
	}
	defer func() { _ = cleanup() }()
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM dolt_log").Scan(&commits); err != nil {
		t.Fatalf("query dolt_log: %v", err)
	}
	rows, err := db.QueryContext(t.Context(), "SELECT table_name, status FROM dolt_status ORDER BY table_name")
	if err != nil {
		t.Fatalf("query dolt_status: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var table, status string
		if err := rows.Scan(&table, &status); err != nil {
			t.Fatalf("scan dolt_status: %v", err)
		}
		dirty = append(dirty, table+":"+status)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate dolt_status: %v", err)
	}
	return commits, dirty
}

// TestEmbeddedMigratePersonal_refusesPlanningRepoWithoutDatabase verifies that
// a planning repo whose metadata.json names a database that has not been
// bootstrapped yet (a clone, say) is refused before anything moves, and is not
// given a database: a migration is not the place to create one.

func TestEmbeddedMigratePersonalAmbiguousTargetRefusal(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("embedded integration opt-in")
	}
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "source")
	planning := setupContributorRouting(t, bd, dir)
	issue := bdCreate(t, bd, dir, "retain source before ambiguous destination", "--actor", "migration-test", "--repo", dir)
	beadsDir := filepath.Join(planning, ".beads")
	other, err := embeddeddolt.Open(t.Context(), beadsDir, "other", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(beadsDir, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bd, "migrate-personal", "--yes", "--actor", "migration-test")
	cmd.Dir, cmd.Env = dir, bdEnv(dir)
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	if err == nil {
		t.Fatalf("ambiguous target admitted: %s", out)
	}
	for _, want := range []string{"Error: planning repo " + planning, "several databases"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "failed to open planning store") || strings.Contains(out, "Usage:") {
		t.Fatalf("unclean preflight refusal: %s", out)
	}
	if !bdShowSucceeds(t, bd, dir, issue.ID) {
		t.Fatal("source lost on preflight refusal")
	}
	if _, err := os.Stat(filepath.Join(beadsDir, "metadata.json")); !os.IsNotExist(err) {
		t.Fatalf("selector changed: %v", err)
	}
}

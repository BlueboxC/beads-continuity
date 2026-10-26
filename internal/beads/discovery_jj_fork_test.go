package beads

import (
	"github.com/steveyegge/beads/internal/git"
	"github.com/steveyegge/beads/internal/utils"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestForkExplicitWorktreeRedirectKeepsPriority(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")
	mainRepo, worktree := setupRegularWorktreeRepo(t)
	mainStore := filepath.Join(mainRepo, ".beads")
	if err := os.MkdirAll(filepath.Join(mainStore, "dolt"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "metadata.json"), []byte(`{"database":"dolt"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(worktree, ".beads")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "redirect"), []byte(target+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(worktree)
	git.ResetCaches()
	t.Cleanup(git.ResetCaches)
	if got := FindBeadsDir(); !utils.PathsEqual(got, target) {
		t.Fatalf("cwd reference: %q, want %q", got, target)
	}
	t.Chdir(t.TempDir())
	git.ResetCaches()
	if got := FindBeadsDirFrom(worktree); !utils.PathsEqual(got, target) {
		t.Fatalf("explicit discovery: %q, want redirect %q", got, target)
	}
}

// TestFindBeadsDirFromJJSecondaryBesidePrimary pins the jj sibling analogue of
// GH#7782: a jj secondary workspace created beside (not inside) its primary,
// under a parent folder that owns its own store, must resolve from an explicit
// start dir to the primary's .beads — the same store cwd discovery binds — not
// to the parent folder's store. That holds whether or not the primary's store
// has a local database.
func TestFindBeadsDirFromJJSecondaryBesidePrimary(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")

	for _, tc := range []struct {
		name         string
		primaryHasDB bool
	}{
		{name: "primary store with a database", primaryHasDB: true},
		// The shared-server shape: the committed metadata.json, with the
		// data on the shared server rather than in a local database.
		{name: "primary store without a database", primaryHasDB: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpDir, _ = filepath.EvalSymlinks(tmpDir)

			// Parent folder with its own store above both workspaces.
			parentBeadsDir := filepath.Join(tmpDir, ".beads")
			if err := os.MkdirAll(parentBeadsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(parentBeadsDir, "metadata.json"), []byte(`{"database":"dolt"}`), 0o644); err != nil {
				t.Fatal(err)
			}

			// Primary workspace: colocated jj+git with its store in .beads/.
			primaryDir := filepath.Join(tmpDir, "primary")
			if err := os.MkdirAll(filepath.Join(primaryDir, ".jj", "repo"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := func() error {
				cmd := exec.Command("git", "init")
				cmd.Dir = primaryDir
				return cmd.Run()
			}(); err != nil {
				t.Skipf("git not available: %v", err)
			}
			runGit := func(dir string, args ...string) {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
				}
			}
			runGit(primaryDir, "config", "user.email", "test@example.com")
			runGit(primaryDir, "config", "user.name", "Test User")

			primaryBeadsDir := filepath.Join(primaryDir, ".beads")
			if err := os.MkdirAll(primaryBeadsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(primaryBeadsDir, "metadata.json"), []byte(`{"database":"dolt"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.primaryHasDB {
				if err := os.WriteFile(filepath.Join(primaryBeadsDir, "beads.db"), []byte{}, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// Secondary workspace beside the primary, like `jj workspace add ../secondary`:
			// .jj/repo is a file pointing at the primary's .jj/repo directory.
			secondaryDir := filepath.Join(tmpDir, "secondary")
			if err := os.MkdirAll(filepath.Join(secondaryDir, ".jj"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(secondaryDir, ".jj", "repo"), []byte(filepath.Join(primaryDir, ".jj", "repo")+"\n"), 0o640); err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { git.ResetCaches() })

			// Reference: cd into the secondary binds the primary's store.
			t.Chdir(secondaryDir)
			git.ResetCaches()
			if got := FindBeadsDir(); !utils.PathsEqual(got, primaryBeadsDir) {
				t.Fatalf("reference: FindBeadsDir() from secondary = %q, want primary .beads %q", got, primaryBeadsDir)
			}

			// Under test: explicit discovery, as bd -C resolves it from an
			// unrelated cwd.
			t.Chdir(t.TempDir())
			git.ResetCaches()
			if got := FindBeadsDirFrom(secondaryDir); !utils.PathsEqual(got, primaryBeadsDir) {
				t.Errorf("FindBeadsDirFrom(secondary) = %q, want primary .beads %q (what cd binds)", got, primaryBeadsDir)
			}
		})
	}
}

// TestFindBeadsDirFromJJSecondaryInsideLinkedWorktree pins that explicit
// discovery ignores a jj secondary workspace nested inside a linked git
// worktree, as cwd discovery does (FindBeadsDir looks for a jj secondary only
// outside a linked worktree). The worktree root still bounds the walk, so -C
// binds what cd binds there instead of the jj primary's store.
func TestFindBeadsDirFromJJSecondaryInsideLinkedWorktree(t *testing.T) {
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_DB", "")

	// newStore creates a .beads holding metadata.json and, withDB, a
	// database (dolt/).
	newStore := func(t *testing.T, beadsDir string, withDB bool) {
		t.Helper()
		if err := os.MkdirAll(beadsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"database":"dolt"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if withDB {
			if err := os.Mkdir(filepath.Join(beadsDir, "dolt"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	for _, tc := range []struct {
		name              string
		mainStore         bool   // the main checkout has a store with a database
		secondaryMetadata bool   // the jj secondary carries tracked metadata only
		want              string // "main", "secondary" or "parent"
	}{
		{
			name:      "secondary without .beads binds the main checkout store",
			mainStore: true,
			want:      "main",
		},
		{
			name:              "metadata-only secondary .beads binds itself",
			mainStore:         true,
			secondaryMetadata: true,
			want:              "secondary",
		},
		{
			name: "no main checkout store keeps walking to the parent store",
			want: "parent",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mainRepo, worktree := setupRegularWorktreeRepo(t)
			parent := filepath.Dir(mainRepo)
			parentStore := filepath.Join(parent, ".beads")
			newStore(t, parentStore, true)
			mainStore := filepath.Join(mainRepo, ".beads")
			if tc.mainStore {
				newStore(t, mainStore, true)
			}

			// A jj primary with a database beside the git checkouts, and a
			// secondary workspace of it inside the linked worktree.
			primary := filepath.Join(parent, "jj-primary")
			if err := os.MkdirAll(filepath.Join(primary, ".jj", "repo"), 0o755); err != nil {
				t.Fatal(err)
			}
			newStore(t, filepath.Join(primary, ".beads"), true)
			secondary := filepath.Join(worktree, "jj-secondary")
			if err := os.MkdirAll(filepath.Join(secondary, ".jj"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(secondary, ".jj", "repo"), []byte(filepath.Join(primary, ".jj", "repo")+"\n"), 0o640); err != nil {
				t.Fatal(err)
			}
			secondaryStore := filepath.Join(secondary, ".beads")
			if tc.secondaryMetadata {
				newStore(t, secondaryStore, false)
			}
			if root, ok := git.JJSecondaryWorkspaceRootFrom(secondary); !ok || !utils.PathsEqual(root, secondary) {
				t.Fatalf("fixture precondition: %q must be a jj secondary workspace root, got %q (ok=%v)", secondary, root, ok)
			}
			want := map[string]string{"main": mainStore, "secondary": secondaryStore, "parent": parentStore}[tc.want]

			// Reference: what `cd <secondary> && bd` binds.
			t.Cleanup(func() { git.ResetCaches() })
			t.Chdir(secondary)
			git.ResetCaches()
			if got := FindBeadsDir(); !utils.PathsEqual(got, want) {
				t.Fatalf("reference: FindBeadsDir() from %q = %q, want %q", secondary, got, want)
			}

			// Under test: explicit discovery, as bd -C resolves it from an
			// unrelated cwd.
			t.Chdir(t.TempDir())
			git.ResetCaches()
			if got := FindBeadsDirFrom(secondary); !utils.PathsEqual(got, want) {
				t.Errorf("FindBeadsDirFrom(%q) = %q, want %q (what cd binds)", secondary, got, want)
			}
		})
	}
}

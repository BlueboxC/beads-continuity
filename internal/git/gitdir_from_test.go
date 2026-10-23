package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGetMainRepoRootFrom pins that the path-aware root resolves the
// repository containing the directory it is given, not the working
// directory's: the checkout's root from a subdirectory or a symlink into it,
// the main checkout's root from a linked worktree, and a nested repository's
// own root. Outside a work tree it errors, and it neither reads nor changes
// the cached context, whether that context holds a repository or a failure.
func TestGetMainRepoRootFrom(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME"} {
		t.Setenv(key, home)
	}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			t.Cleanup(func() { _ = os.Setenv(key, value) })
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mkdir := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(path, 0750); err != nil {
			t.Fatal(err)
		}
		return path
	}
	canonical := func(path string) string {
		t.Helper()
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return NormalizePath(resolved)
	}
	root := canonical(t.TempDir())
	mainRepo, decoy := mkdir(filepath.Join(root, "main repo")), mkdir(filepath.Join(root, "decoy repo"))
	for _, repo := range []string{mainRepo, decoy} {
		git(repo, "init")
		git(repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=", "commit", "--allow-empty", "-m", "seed")
	}
	sub := mkdir(filepath.Join(mainRepo, "sub dir"))
	linked := filepath.Join(root, "linked repo")
	git(mainRepo, "worktree", "add", "-b", "linked", linked)
	linkedSub := mkdir(filepath.Join(linked, "sub dir"))
	nested := mkdir(filepath.Join(mainRepo, "nested repo"))
	git(nested, "init")
	nonrepo := mkdir(filepath.Join(root, "nonrepo"))
	bare := mkdir(filepath.Join(root, "bare.git"))
	git(bare, "init", "--bare")
	t.Cleanup(ResetCaches)

	for _, cachedIn := range []string{decoy, nonrepo} {
		t.Run("cached in "+filepath.Base(cachedIn), func(t *testing.T) {
			t.Chdir(cachedIn)
			ResetCaches()
			if _, err := getGitContext(); (err != nil) != (cachedIn == nonrepo) {
				t.Fatalf("cache precondition: %v", err)
			}
			before := gitCtx
			relativeSub, err := filepath.Rel(cachedIn, sub)
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct{ name, dir, want string }{
				{"repository root", mainRepo, mainRepo},
				{"subdirectory", sub, mainRepo},
				{"subdirectory relative to the working directory", relativeSub, mainRepo},
				{"linked worktree", linked, mainRepo},
				{"subdirectory of a linked worktree", linkedSub, mainRepo},
				{"nested repository", nested, nested},
			} {
				got, err := GetMainRepoRootFrom(tc.dir)
				if err != nil || got != canonical(tc.want) {
					t.Errorf("%s: GetMainRepoRootFrom(%q) = %q, %v; want %q", tc.name, tc.dir, got, err, canonical(tc.want))
				}
			}
			for name, dir := range map[string]string{"empty": "", "nonrepo": nonrepo, "bare": bare, "missing": filepath.Join(root, "missing")} {
				if got, err := GetMainRepoRootFrom(dir); err == nil || got != "" {
					t.Errorf("%s: GetMainRepoRootFrom(%q) = %q, %v; want an error", name, dir, got, err)
				}
			}
			if gitCtx != before {
				t.Error("GetMainRepoRootFrom changed the cached context")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(root, "sub alias")
		if err := os.Symlink(sub, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink capability unavailable: %v", err)
			}
			t.Fatal(err)
		}
		if got, err := GetMainRepoRootFrom(link); err != nil || got != canonical(mainRepo) {
			t.Errorf("GetMainRepoRootFrom(%q) = %q, %v; want %q", link, got, err, canonical(mainRepo))
		}
	})
}

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/git"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/stretchr/testify/require"
)

func setUninstallFixtureMode(t *testing.T, mode string) {
	t.Helper()
	for _, name := range []string{"beads", "shared", "force", "chain"} {
		flag := hooksInstallCmd.Flags().Lookup(name)
		old := flag.Value.String()
		t.Cleanup(func() { require.NoError(t, flag.Value.Set(old)) })
		require.NoError(t, flag.Value.Set("false"))
		if name == mode {
			require.NoError(t, flag.Value.Set("true"))
		}
	}
}

func installUninstallFixture(t *testing.T, root, mode string) {
	t.Helper()
	beadsDir := filepath.Join(root, ".beads")
	require.NoError(t, os.MkdirAll(beadsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte("{}\n"), 0600))
	t.Setenv("BEADS_DIR", beadsDir)
	setUninstallFixtureMode(t, mode)
	require.NoError(t, hooksInstallCmd.RunE(hooksInstallCmd, nil))
}

// Adapted from Rongjun GENG's gastownhall/beads#7541 regression.
func TestStandaloneUninstallRemovesBothHookLayouts(t *testing.T) {
	root := newGitRepo(t)
	runInDir(t, root, func() {
		installUninstallFixture(t, root, "")
		gitShim := filepath.Join(root, ".git", "hooks", "pre-commit")
		content, err := os.ReadFile(gitShim)
		require.NoError(t, err)
		require.Contains(t, string(content), hookSectionBeginPrefix)

		installUninstallFixture(t, root, "beads")
		beadsShim := filepath.Join(root, ".beads", "hooks", "pre-commit")
		content, err = os.ReadFile(beadsShim)
		require.NoError(t, err)
		require.Contains(t, string(content), hookSectionBeginPrefix)
		out, err := exec.Command("git", "config", "--local", "--get", "core.hooksPath").Output()
		require.NoError(t, err)
		require.True(t, utils.PathsEqual(filepath.Join(root, ".beads", "hooks"), strings.TrimSpace(string(out))))

		require.NoError(t, hooksUninstallCmd.RunE(hooksUninstallCmd, nil))
		_, err = os.Stat(beadsShim)
		require.ErrorIs(t, err, os.ErrNotExist)
		_, err = os.Stat(gitShim)
		require.ErrorIs(t, err, os.ErrNotExist, "the earlier default shim must be removed too")
		require.Error(t, exec.Command("git", "config", "--local", "--get", "core.hooksPath").Run())
	})
}

func TestUninstallHooksLayoutsPreserveForeignContent(t *testing.T) {
	for _, modes := range [][]string{{""}, {"beads"}, {"", "shared"}, {"", "beads"}} {
		t.Run(strings.Join(modes, "+")+"layout", func(t *testing.T) {
			root := newGitRepo(t)
			runInDir(t, root, func() {
				original := "#!/bin/sh\nprintf 'owner-hook\\n'\n"
				defaultDir := filepath.Join(root, ".git", "hooks")
				hook := writeHook(t, defaultDir, "pre-commit", original)
				foreign := writeHook(t, defaultDir, "post-merge", composedForeignHook)
				for _, mode := range modes {
					installUninstallFixture(t, root, mode)
				}
				require.NoError(t, hooksUninstallCmd.RunE(hooksUninstallCmd, nil))
				for _, path := range []string{hook, foreign} {
					content, err := os.ReadFile(path)
					require.NoError(t, err)
					require.NotContains(t, string(content), hookSectionBeginPrefix)
				}
				content, err := os.ReadFile(hook)
				require.NoError(t, err)
				require.Equal(t, original, string(content))
				backupPath := hook + ".backup"
				if modes[0] == "beads" {
					backupPath = filepath.Join(root, ".beads", "hooks", "pre-commit.backup")
				}
				backup, err := os.ReadFile(backupPath)
				require.NoError(t, err)
				require.Equal(t, original, string(backup))
				content, err = os.ReadFile(foreign)
				require.NoError(t, err)
				require.Equal(t, composedForeignHook, string(content))
				for _, dir := range []string{defaultDir, filepath.Join(root, ".beads", "hooks"), filepath.Join(root, ".beads-hooks")} {
					for _, name := range managedHookNames {
						content, err := os.ReadFile(filepath.Join(dir, name))
						if os.IsNotExist(err) {
							continue
						}
						require.NoError(t, err)
						require.NotContains(t, string(content), hookSectionBeginPrefix)
					}
				}
			})
		})
	}
}

func TestUninstallHooksCustomPathKeepsInactiveHooks(t *testing.T) {
	root := newGitRepo(t)
	runInDir(t, root, func() {
		inactive := writeHook(t, filepath.Join(root, ".git", "hooks"), "pre-commit", "#!/bin/sh\n"+generateHookSection("pre-commit"))
		before, err := os.ReadFile(inactive)
		require.NoError(t, err)
		own := "#!/bin/sh\nprintf 'custom\\n'\n"
		active := writeHook(t, filepath.Join(root, "owner-hooks"), "pre-commit", injectHookSection(own, generateHookSection("pre-commit")))
		require.NoError(t, exec.Command("git", "config", "core.hooksPath", "owner-hooks").Run())
		require.NoError(t, hooksUninstallCmd.RunE(hooksUninstallCmd, nil))
		after, err := os.ReadFile(inactive)
		require.NoError(t, err)
		require.Equal(t, before, after)
		after, err = os.ReadFile(active)
		require.NoError(t, err)
		require.Equal(t, own, string(after))
		out, err := exec.Command("git", "config", "--get", "core.hooksPath").Output()
		require.NoError(t, err)
		require.Equal(t, "owner-hooks", strings.TrimSpace(string(out)))
	})
}

func TestUninstallHooksRestoresLegacyFallbackBackup(t *testing.T) {
	root := newGitRepo(t)
	runInDir(t, root, func() {
		installUninstallFixture(t, root, "beads")
		dir := filepath.Join(root, ".git", "hooks")
		hook := writeHook(t, dir, "pre-commit", "#!/bin/sh\n# bd-shim 1.0\nexec bd hooks run pre-commit\n")
		original := "#!/bin/sh\nprintf 'backup\\n'\n"
		writeHook(t, dir, "pre-commit.backup", original)
		sidecar := writeHook(t, dir, "pre-commit.old", "#!/bin/sh\nprintf 'sidecar\\n'\n")
		before, err := os.ReadFile(sidecar)
		require.NoError(t, err)
		require.NoError(t, hooksUninstallCmd.RunE(hooksUninstallCmd, nil))
		content, err := os.ReadFile(hook)
		require.NoError(t, err)
		require.Equal(t, original, string(content))
		content, err = os.ReadFile(sidecar)
		require.NoError(t, err)
		require.Equal(t, before, content)
	})
}

func TestUninstallHooksIgnoresStaleGitCache(t *testing.T) {
	root := newGitRepo(t)
	decoy := newGitRepo(t)
	runInDir(t, root, func() {
		installUninstallFixture(t, root, "")
		installUninstallFixture(t, root, "beads")
		canary := writeHook(t, filepath.Join(decoy, ".git", "hooks"), "pre-commit", "#!/bin/sh\n"+generateHookSection("pre-commit"))
		before, err := os.ReadFile(canary)
		require.NoError(t, err)
		git.ResetCaches()
		require.NoError(t, os.Chdir(decoy))
		_, err = git.GetGitCommonDir()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(root))
		require.NoError(t, hooksUninstallCmd.RunE(hooksUninstallCmd, nil))
		after, err := os.ReadFile(canary)
		require.NoError(t, err)
		require.Equal(t, before, after)
		for _, dir := range []string{filepath.Join(root, ".git", "hooks"), filepath.Join(root, ".beads", "hooks")} {
			for _, name := range managedHookNames {
				_, err = os.Stat(filepath.Join(dir, name))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		}
	})
}

func TestUninstallHooksLinkedWorktree(t *testing.T) {
	root := newGitRepo(t)
	worktree := filepath.Join(t.TempDir(), "linked")
	runInDir(t, root, func() {
		require.NoError(t, exec.Command("git", "-c", "core.hooksPath=", "-c", "maintenance.auto=false", "-c", "gc.auto=0", "commit", "--allow-empty", "-m", "fixture").Run())
		require.NoError(t, exec.Command("git", "worktree", "add", "--detach", worktree, "HEAD").Run())
		installUninstallFixture(t, root, "")
		installUninstallFixture(t, root, "beads")
	})
	runInDir(t, worktree, func() {
		require.NoError(t, hooksUninstallCmd.RunE(hooksUninstallCmd, nil))
		for _, dir := range []string{filepath.Join(root, ".git", "hooks"), filepath.Join(root, ".beads", "hooks")} {
			for _, name := range managedHookNames {
				_, err := os.Stat(filepath.Join(dir, name))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
		}
		require.Error(t, exec.Command("git", "config", "--local", "--get", "core.hooksPath").Run())
	})
}

func TestUninstallHooksGlobalFallbackKeepsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	root := newGitRepo(t)
	globalDir := filepath.Join(home, "global-hooks")
	original := "#!/bin/sh\nprintf 'global-owner\\n'\n"
	hook := writeHook(t, globalDir, "pre-commit", original)
	runInDir(t, root, func() {
		require.NoError(t, exec.Command("git", "config", "--global", "core.hooksPath", globalDir).Run())
		require.NoError(t, exec.Command("git", "config", "--local", "--unset", "core.hooksPath").Run())
		installUninstallFixture(t, root, "")
		installUninstallFixture(t, root, "beads")
		require.NoError(t, hooksUninstallCmd.RunE(hooksUninstallCmd, nil))
		content, err := os.ReadFile(hook)
		require.NoError(t, err)
		require.Equal(t, original, string(content))
		out, err := exec.Command("git", "config", "--global", "--get", "core.hooksPath").Output()
		require.NoError(t, err)
		require.Equal(t, globalDir, strings.TrimSpace(string(out)))
	})
}

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestIsBareGitRepo(t *testing.T) {
	t.Run("returns true in bare repository", func(t *testing.T) {
		repoDir := filepath.Join(t.TempDir(), "bare.git")
		runGitForSyncTest(t, "", "init", "--bare", repoDir)

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get cwd: %v", err)
		}
		defer func() { _ = os.Chdir(oldWd) }()
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}

		if !isBareGitRepo() {
			t.Fatal("isBareGitRepo() = false, want true")
		}
	})

	t.Run("returns false in non-bare repository", func(t *testing.T) {
		repoDir := t.TempDir()
		runGitForSyncTest(t, repoDir, "init")

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get cwd: %v", err)
		}
		defer func() { _ = os.Chdir(oldWd) }()
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}

		if isBareGitRepo() {
			t.Fatal("isBareGitRepo() = true, want false")
		}
	})

	t.Run("returns false outside git repository", func(t *testing.T) {
		nonRepoDir := t.TempDir()

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get cwd: %v", err)
		}
		defer func() { _ = os.Chdir(oldWd) }()
		if err := os.Chdir(nonRepoDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}

		if isBareGitRepo() {
			t.Fatal("isBareGitRepo() = true, want false")
		}
	})
}

func TestGitOriginGetURL(t *testing.T) {
	t.Run("returns origin URL", func(t *testing.T) {
		repoDir := t.TempDir()
		bareDir := filepath.Join(t.TempDir(), "bare.git")
		runGitForSyncTest(t, "", "init", "--bare", bareDir)
		runGitForSyncTest(t, repoDir, "init")
		runGitForSyncTest(t, repoDir, "remote", "add", "origin", bareDir)

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get cwd: %v", err)
		}
		defer func() { _ = os.Chdir(oldWd) }()
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}

		url, err := gitOriginGetURL()
		if err != nil {
			t.Fatalf("gitOriginGetURL failed: %v", err)
		}
		if url != bareDir {
			t.Errorf("gitOriginGetURL = %q, want %q", url, bareDir)
		}
	})

	t.Run("returns error for nonexistent remote", func(t *testing.T) {
		repoDir := t.TempDir()
		runGitForSyncTest(t, repoDir, "init")

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get cwd: %v", err)
		}
		defer func() { _ = os.Chdir(oldWd) }()
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}

		_, err = gitOriginGetURL()
		if err == nil {
			t.Fatal("expected error for nonexistent remote")
		}
	})
}

func TestGitOriginHasDoltDataRef(t *testing.T) {
	t.Run("returns false for nonexistent ref", func(t *testing.T) {
		bareDir := filepath.Join(t.TempDir(), "bare.git")
		runGitForSyncTest(t, "", "init", "--bare", bareDir)

		repoDir := t.TempDir()
		runGitForSyncTest(t, repoDir, "init")
		runGitForSyncTest(t, repoDir, "remote", "add", "origin", bareDir)

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get cwd: %v", err)
		}
		defer func() { _ = os.Chdir(oldWd) }()
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}

		if gitOriginHasDoltDataRef() {
			t.Fatal("expected false for nonexistent ref")
		}
	})

	t.Run("returns true for existing ref", func(t *testing.T) {
		bareDir := filepath.Join(t.TempDir(), "bare.git")
		runGitForSyncTest(t, "", "init", "--bare", bareDir)

		// Create a repo, commit, push to create refs/dolt/data
		repoDir := t.TempDir()
		runGitForSyncTest(t, repoDir, "init", "-b", "main")
		runGitForSyncTest(t, repoDir, "config", "user.email", "test@test.com")
		runGitForSyncTest(t, repoDir, "config", "user.name", "Test User")
		runGitForSyncTest(t, repoDir, "commit", "--allow-empty", "-m", "init")
		runGitForSyncTest(t, repoDir, "remote", "add", "origin", bareDir)
		runGitForSyncTest(t, repoDir, "push", "origin", "HEAD:refs/dolt/data")

		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get cwd: %v", err)
		}
		defer func() { _ = os.Chdir(oldWd) }()
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}

		if !gitOriginHasDoltDataRef() {
			t.Fatal("expected true for existing ref")
		}
	})
}

func TestGitURLToDoltRemote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://github.com/org/repo.git", "git+https://github.com/org/repo.git"},
		{"http://github.com/org/repo.git", "git+http://github.com/org/repo.git"},
		{"ssh://git@github.com/org/repo.git", "git+ssh://git@github.com/org/repo.git"},
		{"git@github.com:org/repo.git", "git+ssh://git@github.com/org/repo.git"},
		{"git+https://github.com/org/repo.git", "git+https://github.com/org/repo.git"},
		{"git+ssh://git@github.com/org/repo.git", "git+ssh://git@github.com/org/repo.git"},
		{"C:/Users/alice/repos/beads.git", "git+C:/Users/alice/repos/beads.git"},
		{`D:\repos\beads.git`, `git+D:\repos\beads.git`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := gitURLToDoltRemote(tt.input)
			if got != tt.want {
				t.Errorf("gitURLToDoltRemote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func runGitForSyncTest(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
	}
}

// TestGitRemoteHasDoltDataRefStatusSurfacesGitStderr proves the probe failure a
// user actually sees carries git's own diagnosis. exec.ExitError.Error() is
// only "exit status 128"; git's complaint sits unread in ExitError.Stderr, and
// bootstrap's blocked message is built from this error (#5743).
func TestGitRemoteHasDoltDataRefStatusSurfacesGitStderr(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-repo")

	hasData, err := gitRemoteHasDoltDataRefStatus(missing)

	if err == nil {
		t.Fatalf("probe of %q returned hasData=%v and no error; want a failure", missing, hasData)
	}
	msg := err.Error()
	if !strings.Contains(msg, "ls-remote") {
		t.Errorf("error %q does not name the command that failed", msg)
	}
	// Whatever git's exact wording, the message must be more than the bare
	// exit status — that is the whole point of reading ExitError.Stderr.
	if msg == "git ls-remote: exit status 128" {
		t.Errorf("error %q carries no stderr detail; the ExitError.Stderr thread-through regressed", msg)
	}
	if !strings.Contains(msg, "repository") && !strings.Contains(msg, "fatal") {
		t.Errorf("error %q does not look like it carries git's stderr", msg)
	}
}

// Contributor regressions adapted from Vishnu Jayavel's PR #7511.
func TestGitRemoteHasDoltDataRefStatusDisablesAskpass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$GIT_TERMINAL_PROMPT" != 0 ] || [ -n "$GIT_ASKPASS" ] || [ -n "$SSH_ASKPASS" ]; then
 echo 'fatal: inherited credential prompt is enabled' >&2
 exit 128
fi
printf '0123456789012345678901234567890123456789\trefs/dolt/data\n'
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("GIT_ASKPASS", "/inherited/git-askpass")
	t.Setenv("SSH_ASKPASS", "/inherited/ssh-askpass")
	hasData, err := gitRemoteHasDoltDataRefStatus("origin")
	if err != nil || !hasData {
		t.Fatalf("noninteractive probe=(%v,%v), want true,nil", hasData, err)
	}
}
func TestGitRemoteHasDoltDataRefStatusRetainedPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires a POSIX shell")
	}
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "child.pid")
	script := `#!/bin/sh
/bin/sleep 6 &
printf '%s\n' "$!" > "$PROBE_CHILD_PID"
printf '0123456789012345678901234567890123456789\trefs/dolt/data\n'
exit 0
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PROBE_CHILD_PID", pidPath)
	t.Cleanup(func() {
		data, err := os.ReadFile(pidPath)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil {
			if child, err := os.FindProcess(pid); err == nil {
				_ = child.Kill()
			}
		}
	})
	hasData, err := gitRemoteHasDoltDataRefStatus("origin")
	if hasData || !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("probe with retained pipes=(%v,%v), want UNKNOWN/ErrWaitDelay", hasData, err)
	}
}

func TestGitRemoteHasDoltDataRefStatusRootCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires a POSIX shell")
	}
	for _, tc := range []struct {
		name                  string
		deadline, preCanceled bool
		want                  error
	}{
		{"already canceled", false, true, context.Canceled},
		{"canceled with descendant pipes", false, false, context.Canceled},
		{"deadline with descendant pipes", true, false, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshotRootContext(t)
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "child.pid")
			started := filepath.Join(dir, "started")
			script := `#!/bin/sh
/bin/sleep 6 &
printf '%s\n' "$!" > "$PROBE_CHILD_PID"
printf '0123456789012345678901234567890123456789\trefs/dolt/data\n'
printf started > "$PROBE_STARTED"
wait
`
			if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("PROBE_CHILD_PID", pidPath)
			t.Setenv("PROBE_STARTED", started)
			t.Cleanup(func() {
				data, err := os.ReadFile(pidPath)
				if err == nil {
					pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
					if err == nil {
						if p, err := os.FindProcess(pid); err == nil {
							_ = p.Kill()
						}
					}
				}
			})
			var ctx context.Context
			var cancel context.CancelFunc
			if tc.deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 250*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			setRootContext(ctx, cancel)
			if tc.preCanceled {
				cancel()
			} else if !tc.deadline {
				finished := make(chan struct{})
				defer close(finished)
				go func() {
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-finished:
							return
						case <-ticker.C:
							if _, err := os.Stat(started); err == nil {
								cancel()
								return
							}
						}
					}
				}()
			}
			start := time.Now()
			hasData, err := gitRemoteHasDoltDataRefStatus("origin")
			elapsed := time.Since(start)
			if hasData || !errors.Is(err, tc.want) {
				t.Fatalf("probe=(%v,%v), want UNKNOWN/%v", hasData, err, tc.want)
			}
			if !tc.preCanceled {
				if _, err := os.Stat(pidPath); err != nil {
					t.Fatalf("descendant fixture did not start: %v", err)
				}
			}
			if elapsed > 4*time.Second {
				t.Fatalf("canceled pipe wait unbounded: %s", elapsed)
			}
			if tc.preCanceled {
				if _, err := os.Stat(started); !os.IsNotExist(err) {
					t.Fatalf("pre-canceled probe executed git: %v", err)
				}
			}
		})
	}
}

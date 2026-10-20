package git

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGitFixtureDoesNotChangeOuterRepository(t *testing.T) {
	outer := t.TempDir()
	cleanEnv := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "GIT_") {
			cleanEnv = append(cleanEnv, value)
		}
	}
	cleanEnv = append(cleanEnv, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = outer, cleanEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "Outer Owner")
	git("config", "user.email", "outer@example.invalid")
	git("config", "core.hooksPath", os.DevNull)
	if err := os.WriteFile(filepath.Join(outer, "real.txt"), []byte("real work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "real.txt")
	git("-c", "maintenance.auto=false", "-c", "gc.auto=0", "commit", "-qm", "real work")
	snapshot := func() map[string][32]byte {
		t.Helper()
		result := map[string][32]byte{}
		err := filepath.WalkDir(outer, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			body, err := os.ReadFile(path)
			if err == nil {
				relative, relErr := filepath.Rel(outer, path)
				if relErr != nil {
					return relErr
				}
				result[relative] = sha256.Sum256(body)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGitFixtureIsolationChild$")
	cmd.Env = append(cleanEnv, "BEADS_GIT_FIXTURE_CHILD=1", "GIT_DIR="+filepath.Join(outer, ".git"), "GIT_INDEX_FILE="+filepath.Join(outer, ".git", "index"))
	output, childErr := cmd.CombinedOutput()
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Error("fixture changed the outer repository's files, index, config or history")
	}
	if childErr != nil {
		t.Fatalf("fixture subprocess: %v: %s", childErr, output)
	}
}

func TestGitFixtureIsolationChild(t *testing.T) {
	if os.Getenv("BEADS_GIT_FIXTURE_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	repo, cleanup := setupTestRepo(t)
	defer cleanup()
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(fmt.Errorf("fixture Git directory was routed outside its temporary repository: %w", err))
	}
}

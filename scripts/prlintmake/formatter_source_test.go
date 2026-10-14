package prlintmake

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFormatterPreservesIgnoredEvidence(t *testing.T) {
	root := formatterFixture(t)
	writeFormatterFile(t, root, ".gitignore", ".beads/\n")
	evidence := "package historical\nnot valid Go; preserve these bytes\n"
	writeFormatterFile(t, root, ".beads/evidence/original.go", evidence)
	for _, target := range []string{"fmt-check", "fmt", "fmt-check"} {
		if output, err := runFormatterMake(t, root, target); err != nil {
			t.Fatalf("make %s: %v\n%s", target, err, output)
		}
		got, err := os.ReadFile(filepath.Join(root, ".beads/evidence/original.go"))
		if err != nil || string(got) != evidence {
			t.Fatalf("historical evidence changed: %q, %v", got, err)
		}
	}
}

func TestFormatterIncludesTrackedAndNewSource(t *testing.T) {
	root := formatterFixture(t)
	writeFormatterFile(t, root, ".gitignore", ".beads/\n")
	paths := []string{".beads/evidence/tracked.go", "new file.go", "-option.go"}
	if runtime.GOOS != "windows" {
		paths = append(paths, "line\nbreak.go")
	}
	for i := range 70 {
		paths = append(paths, fmt.Sprintf("batch/source-%02d.go", i))
	}
	for _, path := range paths {
		writeFormatterFile(t, root, path, "package fixture\nvar value=1\n")
	}
	formatterGit(t, root, "add", "-f", "--", paths[0])
	writeFormatterFile(t, root, "deleted.go", "package fixture\n")
	formatterGit(t, root, "add", "--", "deleted.go")
	if err := os.Remove(filepath.Join(root, "deleted.go")); err != nil {
		t.Fatal(err)
	}
	output, err := runFormatterMake(t, root, "fmt-check")
	if err == nil {
		t.Fatalf("unformatted source passed:\n%s", output)
	}
	for _, path := range paths {
		if !strings.Contains(output, path) {
			t.Fatalf("missing source %q:\n%s", path, output)
		}
	}
	if output, err := runFormatterMake(t, root, "fmt"); err != nil {
		t.Fatalf("make fmt: %v\n%s", err, output)
	}
	for _, path := range paths {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(got) != "package fixture\n\nvar value = 1\n" {
			t.Fatalf("source %q not formatted: %q, %v", path, got, err)
		}
	}
	if output, err := runFormatterMake(t, root, "fmt-check"); err != nil {
		t.Fatalf("formatted source rejected: %v\n%s", err, output)
	}
}

func TestFormatterRequiresGitInventory(t *testing.T) {
	root := formatterFixture(t)
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	writeFormatterFile(t, root, ".git", "invalid git directory\n")
	if output, err := runFormatterMake(t, root, "fmt-check"); err == nil {
		t.Fatalf("unavailable source inventory passed:\n%s", output)
	}
}

func formatterFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"Makefile", "go.mod", ".buildflags", "scripts/ci/fmt-check.sh"} {
		data, err := os.ReadFile(filepath.Join(sourceRepoRoot(), path))
		if err != nil {
			t.Fatal(err)
		}
		writeFormatterFile(t, root, path, string(data))
	}
	formatterGit(t, root, "init", "--quiet")
	formatterGit(t, root, "config", "core.hooksPath", ".git/hooks")
	return root
}

func writeFormatterFile(t *testing.T, root, path, contents string) {
	t.Helper()
	fullPath := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func formatterGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func runFormatterMake(t *testing.T, root, target string) (string, error) {
	t.Helper()
	cmd := exec.Command(testBash(t), "--noprofile", "--norc", "-c", "make GIT_BUILD=fixture "+target)
	cmd.Dir = root
	cmd.Env = environment(map[string]string{"BASH_ENV": "", "ENV": "", "SHELLOPTS": ""})
	output, err := cmd.CombinedOutput()
	return normalizeNewlines(string(output)), err
}

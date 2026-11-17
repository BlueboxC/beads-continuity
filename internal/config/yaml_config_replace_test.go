package config

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Adapted from the contributor’s YAML reader regression in #8199.
func openedBeforeWrite(t *testing.T, path string) func() string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(path) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return func() string {
		t.Helper()
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			t.Fatalf("seek: %v", err)
		}
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return string(data)
	}
}

func writeConfigFixture(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func assertModeKept(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Errorf("%s mode after the write = %o, want the file's own %o kept", path, got, mode)
	}
}

func TestSetYamlConfigInDirReplacesTheFileInOneStep(t *testing.T) {
	beadsDir := t.TempDir()
	configPath := filepath.Join(beadsDir, "config.yaml")
	const before = "events-journal: true\nevents-journal-auto-prune: false\n"
	// 0640 is a mode no writer defaults to, so keeping it is the file's own.
	writeConfigFixture(t, configPath, before, 0o640)
	reader := openedBeforeWrite(t, configPath)

	if err := SetYamlConfigInDir(beadsDir, "json", "true"); err != nil {
		t.Fatalf("SetYamlConfigInDir: %v", err)
	}

	if reader != nil {
		if got := reader(); got != before {
			t.Errorf("a reader that opened config.yaml before the write saw %q, want the whole old file %q: the write truncated it in place", got, before)
		}
	}
	data, err := os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(data), "json: true") {
		t.Errorf("after the write: %q, %v", data, err)
	}
	assertModeKept(t, configPath, 0o640)
}

func TestSetReposInYAMLReplacesTheFileInOneStep(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	const before = "issue-prefix: bd\nevents-journal: true\n"
	writeConfigFixture(t, configPath, before, 0o640)
	reader := openedBeforeWrite(t, configPath)

	if err := SetReposInYAML(configPath, &ReposConfig{Primary: ".", Additional: []string{"../other"}}); err != nil {
		t.Fatalf("SetReposInYAML: %v", err)
	}

	if reader != nil {
		if got := reader(); got != before {
			t.Errorf("a reader that opened config.yaml before the write saw %q, want the whole old file %q", got, before)
		}
	}
	repos, err := GetReposFromYAML(configPath)
	if err != nil {
		t.Fatalf("GetReposFromYAML: %v", err)
	}
	if repos.Primary != "." || len(repos.Additional) != 1 || repos.Additional[0] != "../other" {
		t.Errorf("after the write repos = %+v, want primary \".\" and additional [../other]", repos)
	}
	assertModeKept(t, configPath, 0o640)
}

func TestSetYamlConfigInDirWritesThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevated privileges on Windows")
	}
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(dir, "shared-config.yaml")
	if err := os.WriteFile(target, []byte("json: false\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(beadsDir, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := SetYamlConfigInDir(beadsDir, "json", "true"); err != nil {
		t.Fatalf("SetYamlConfigInDir: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("config.yaml is no longer a symlink after the write")
	}
	data, err := os.ReadFile(target) //nolint:gosec // test-owned temp path
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !strings.Contains(string(data), "json: true") {
		t.Errorf("symlink target = %q, want the written value", data)
	}
}

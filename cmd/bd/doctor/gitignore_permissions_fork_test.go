package doctor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestForkGitignorePermissionsAndPatterns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode assertions")
	}
	for _, tc := range []struct {
		name       string
		mode, want os.FileMode
		complete   bool
	}{
		{"loose complete", 0644, 0600, true},
		{"readonly complete", 0400, 0400, true},
		{"readonly public complete", 0444, 0400, true},
		{"private executable complete", 0700, 0700, true},
		{"readonly incomplete", 0400, 0600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			dir := filepath.Join(repo, ".beads")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, ".gitignore")
			content := "# custom rule\nmy-local-file\n"
			if tc.complete {
				content += GitignoreTemplate
			}
			if err := os.WriteFile(file, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(file, tc.mode); err != nil {
				t.Fatal(err)
			}
			before := CheckGitignore(repo)
			if tc.complete && tc.mode&0077 != 0 && before.Status != StatusWarning {
				t.Fatalf("loose complete file reported %s", before.Status)
			}
			if err := EnsureGitignoreForBeadsDir(dir); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != tc.want {
				t.Fatalf("mode %04o, want %04o", info.Mode().Perm(), tc.want)
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "my-local-file") || len(missingGitignorePatterns(string(data))) != 0 {
				t.Fatal("local rule or required patterns lost")
			}
			if got := CheckGitignore(repo); got.Status != StatusOK {
				t.Fatalf("post-repair check: %+v", got)
			}
		})
	}
}

func TestForkGitignorePermissionFailureClassification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	original := chmodBeadsGitignore
	chmodBeadsGitignore = func(string, os.FileMode) error { return syscall.EPERM }
	t.Cleanup(func() { chmodBeadsGitignore = original })
	for _, tc := range []struct {
		name           string
		mode           os.FileMode
		complete, soft bool
	}{
		{"tighten complete", 0644, true, true},
		{"append then tighten", 0644, false, true},
		{"cannot enable append", 0400, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, ".gitignore")
			content := "# custom\n"
			if tc.complete {
				content += GitignoreTemplate
			}
			if err := os.WriteFile(file, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(file, tc.mode); err != nil {
				t.Fatal(err)
			}
			err := EnsureGitignoreForBeadsDir(dir)
			if err == nil || errors.Is(err, ErrGitignorePermissions) != tc.soft {
				t.Fatalf("classification soft=%v: %v", tc.soft, err)
			}
			data, readErr := os.ReadFile(file)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.soft && len(missingGitignorePatterns(string(data))) != 0 {
				t.Fatal("soft failure without required patterns")
			}
		})
	}
}

func TestForkGitignoreModePolicy(t *testing.T) {
	for _, tc := range []struct {
		goos string
		mode os.FileMode
		want bool
	}{
		{"darwin", 0644, true}, {"linux", 0400, false}, {"linux", 0700, false}, {"windows", 0666, false}, {"windows", 0444, false},
	} {
		if got := gitignoreModeIsLoose(tc.goos, tc.mode); got != tc.want {
			t.Errorf("%s mode %04o loose=%v", tc.goos, tc.mode, got)
		}
	}
}

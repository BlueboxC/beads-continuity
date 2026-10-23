package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestResolveChangeDirBeadsDirDoesNotChangeCWD(t *testing.T) {
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(origWD)
	})

	startDir := t.TempDir()
	t.Chdir(startDir)

	projectDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		projectDir = resolved
	}
	beadsDir := filepath.Join(projectDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := resolveChangeDirBeadsDir(projectDir)
	if err != nil {
		t.Fatalf("resolveChangeDirBeadsDir: %v", err)
	}
	if got != beadsDir {
		t.Fatalf("resolveChangeDirBeadsDir() = %q, want %q", got, beadsDir)
	}

	afterWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd after resolve: %v", err)
	}
	if afterWD != startDir {
		t.Fatalf("working directory changed to %q, want %q", afterWD, startDir)
	}
}

func TestResolveChangeDirBeadsDirRejectsFile(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := resolveChangeDirBeadsDir(filePath); err == nil {
		t.Fatal("expected non-directory -C target to fail")
	}
}

func TestResolveChangeDirBeadsDirRejectsDirectoryWithoutProject(t *testing.T) {
	if _, err := resolveChangeDirBeadsDir(t.TempDir()); err == nil {
		t.Fatal("expected -C target without a beads project to fail")
	}
}

func TestIsPreviewCommand(t *testing.T) {
	tests := []struct {
		name string
		flag string
		set  string
		want bool
	}{
		{name: "dry run", flag: "dry-run", set: "true", want: true},
		{name: "inspect", flag: "inspect", set: "true", want: true},
		{name: "false preview flag", flag: "dry-run", set: "false", want: false},
		{name: "no preview flag", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			if tt.flag != "" {
				cmd.Flags().Bool(tt.flag, false, "")
				if err := cmd.Flags().Set(tt.flag, tt.set); err != nil {
					t.Fatalf("set %s: %v", tt.flag, err)
				}
			}
			if got := isPreviewCommand(cmd); got != tt.want {
				t.Fatalf("isPreviewCommand() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyChangeDirSelectionPreservesCallerAuthority(t *testing.T) {
	originalDir, originalCaller, originalSnapshot := changeDir, beadsDirFromCaller, changeDirEnvSnapshot
	t.Cleanup(func() {
		changeDir, beadsDirFromCaller, changeDirEnvSnapshot = originalDir, originalCaller, originalSnapshot
	})
	target := t.TempDir()
	beadsDir := filepath.Join(target, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	targetDir, err := filepath.EvalSymlinks(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	alias := t.TempDir()
	if err := os.WriteFile(filepath.Join(alias, "redirect"), []byte(beadsDir), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, caller string
		conflict     bool
	}{
		{name: "empty"}, {name: "same", caller: beadsDir}, {name: "relative", caller: relative},
		{name: "redirect", caller: alias}, {name: "different", caller: other, conflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BEADS_DIR", tc.caller)
			beadsDirFromCaller = tc.caller
			changeDir, changeDirEnvSnapshot = target, nil
			err := applyChangeDirSelection()
			if tc.conflict {
				if err == nil {
					restoreChangeDirSelection()
					t.Fatal("conflicting caller selector was silently replaced")
				}
				if got := os.Getenv("BEADS_DIR"); got != tc.caller {
					t.Fatalf("caller selector changed: %q", got)
				}
				if changeDirEnvSnapshot != nil {
					t.Fatal("refused selection published an environment snapshot")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			selected, err := filepath.EvalSymlinks(os.Getenv("BEADS_DIR"))
			if err != nil || selected != targetDir {
				t.Fatalf("selection=%q err=%v want=%q", selected, err, targetDir)
			}
			restoreChangeDirSelection()
			if got := os.Getenv("BEADS_DIR"); got != tc.caller {
				t.Fatalf("caller restoration=%q want=%q", got, tc.caller)
			}
		})
	}
}

package fix

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRepairTargetRefusesReplacementEntries(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	for _, changed := range []string{"workspace directory", "database parent", "database entry"} {
		t.Run(changed, func(t *testing.T) {
			workspace := filepath.Join(t.TempDir(), ".beads")
			parent := filepath.Join(workspace, "nested")
			database := filepath.Join(parent, "dolt")
			writeRepairFixture(t, database, "original")
			target, err := captureRepairTarget(workspace, database)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(target.close)
			paths := map[string]string{"workspace directory": workspace, "database parent": parent, "database entry": database}
			changedPath := paths[changed]
			retained := changedPath + "-retained"
			moveRepairFixture(t, changedPath, retained)
			writeRepairFixture(t, database, "replacement")

			if err := target.validateOriginal(); err == nil || !strings.Contains(err.Error(), changed) {
				t.Errorf("approval of %s replacement = %v, want identity refusal", changed, err)
			}
			remainder, err := filepath.Rel(changedPath, database)
			if err != nil {
				t.Fatal(err)
			}
			assertRepairFixture(t, filepath.Join(retained, remainder), "original")
			assertRepairFixture(t, database, "replacement")
		})
	}
}

func TestRepairRecoveryRefusesChangedBackupIdentity(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	workspace := t.TempDir()
	database := filepath.Join(workspace, "dolt")
	backup := database + ".fixture.corrupt.backup"
	writeRepairFixture(t, database, "original")
	target, err := captureRepairTarget(workspace, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(target.close)
	moveRepairFixture(t, database, backup)
	if err := revalidateIntegrityRestore(target, backup); err != nil {
		t.Fatalf("matching recovery entry refused: %v", err)
	}

	retained := backup + "-retained"
	moveRepairFixture(t, backup, retained)
	writeRepairFixture(t, backup, "replacement")
	writeRepairFixture(t, database, "current")
	if err := revalidateIntegrityRestore(target, backup); err == nil || !strings.Contains(err.Error(), "original recovery entry") {
		t.Errorf("rollback admission = %v, want backup identity refusal", err)
	}
	assertRepairFixture(t, retained, "original")
	assertRepairFixture(t, backup, "replacement")
	assertRepairFixture(t, database, "current")
}

func TestRepairTargetAcceptsWorkspaceAliasAndExplicitDataDirectory(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	workspace := t.TempDir()
	database := filepath.Join(workspace, "dolt")
	writeRepairFixture(t, database, "original")
	t.Run("workspace alias", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), ".beads")
		if err := os.Symlink(workspace, alias); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		target, err := captureRepairTarget(alias, filepath.Join(alias, "dolt"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(target.close)
		if err := target.validateOriginal(); err != nil {
			t.Errorf("stable workspace alias refused: %v", err)
		}
	})
	t.Run("explicit data directory", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_DATA_DIR", database)
		target, err := captureRepairTarget(t.TempDir(), database)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(target.close)
		if err := target.validateOriginal(); err != nil {
			t.Errorf("stable explicit operator directory refused: %v", err)
		}
	})
}

func writeRepairFixture(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "entry"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func moveRepairFixture(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		if runtime.GOOS == "windows" && os.IsPermission(err) {
			t.Skipf("filesystem denies renaming a directory with an open handle: %v", err)
		}
		t.Fatal(err)
	}
}

func assertRepairFixture(t *testing.T, dir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "entry"))
	if err != nil || string(got) != want {
		t.Errorf("marker in %q = %q, %v, want %q", dir, got, err, want)
	}
}

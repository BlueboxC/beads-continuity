package fix

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestDatabaseIntegrity_ServerModeFailsClosedBeforeBackup(t *testing.T) {
	dir := setupTestWorkspace(t)
	beadsDir := filepath.Join(dir, ".beads")
	doltDir := filepath.Join(beadsDir, "dolt")
	if err := os.MkdirAll(doltDir, 0o755); err != nil {
		t.Fatalf("failed to create dolt dir: %v", err)
	}

	cfg := &configfile.Config{
		Backend:        configfile.BackendDolt,
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltServerPort: 49617,
		DoltDatabase:   "beads_AI",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	err := DatabaseIntegrity(dir)
	if err == nil {
		t.Fatal("expected server-mode integrity recovery to fail closed")
	}
	if strings.Contains(err.Error(), ErrTestBinary.Error()) {
		t.Fatalf("expected server-mode guard before bd binary lookup, got %v", err)
	}
	if !strings.Contains(err.Error(), "automatic integrity recovery is disabled for server-mode repos") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "beads_AI") {
		t.Fatalf("error %q does not mention configured database", err)
	}

	if _, statErr := os.Stat(doltDir); statErr != nil {
		t.Fatalf("expected dolt dir to remain in place, got %v", statErr)
	}
	backupPaths, globErr := filepath.Glob(doltDir + ".*.corrupt.backup")
	if globErr != nil {
		t.Fatalf("glob backup paths: %v", globErr)
	}
	if len(backupPaths) != 0 {
		t.Fatalf("expected no corrupt backup path, got %v", backupPaths)
	}
}

func TestDatabaseIntegrity_NonServerModeRestoresRootOnTestBinaryError(t *testing.T) {
	dir := setupTestWorkspace(t)
	beadsDir := filepath.Join(dir, ".beads")
	doltDir := filepath.Join(beadsDir, "dolt")
	if err := os.MkdirAll(doltDir, 0o755); err != nil {
		t.Fatalf("failed to create dolt dir: %v", err)
	}

	err := DatabaseIntegrity(dir)
	if !errors.Is(err, ErrTestBinary) {
		t.Fatalf("expected ErrTestBinary, got %v", err)
	}

	if _, statErr := os.Stat(doltDir); statErr != nil {
		t.Fatalf("expected dolt dir to be restored, got %v", statErr)
	}
	backupPaths, globErr := filepath.Glob(doltDir + ".*.corrupt.backup")
	if globErr != nil {
		t.Fatalf("glob backup paths: %v", globErr)
	}
	if len(backupPaths) != 0 {
		t.Fatalf("expected no leftover corrupt backup path, got %v", backupPaths)
	}
}

func TestDatabaseIntegrity_SharedWorktreeServerModeFailsClosedBeforeBackup(t *testing.T) {
	mainRepoDir, worktreeDir := setupSharedWorktreeWorkspace(t)
	sharedBeadsDir := filepath.Join(mainRepoDir, ".beads")
	sharedDoltDir := filepath.Join(sharedBeadsDir, "dolt")
	if err := os.MkdirAll(sharedDoltDir, 0o755); err != nil {
		t.Fatalf("failed to create shared dolt dir: %v", err)
	}

	cfg := &configfile.Config{
		Backend:        configfile.BackendDolt,
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltServerPort: 49617,
		DoltDatabase:   "shared_beads",
	}
	if err := cfg.Save(sharedBeadsDir); err != nil {
		t.Fatalf("failed to save shared config: %v", err)
	}

	err := DatabaseIntegrity(worktreeDir)
	if err == nil {
		t.Fatal("expected server-mode integrity recovery to fail closed")
	}
	if !strings.Contains(err.Error(), "shared_beads") {
		t.Fatalf("error %q does not mention shared database", err)
	}

	if _, statErr := os.Stat(sharedDoltDir); statErr != nil {
		t.Fatalf("expected shared dolt dir to remain in place, got %v", statErr)
	}
	if _, err := os.Stat(filepath.Join(worktreeDir, ".beads")); !os.IsNotExist(err) {
		t.Fatalf("expected no worktree-local .beads directory, got err=%v", err)
	}
}

func TestDatabaseIntegrity_SharedWorktreeRestoresSharedRootOnTestBinaryError(t *testing.T) {
	mainRepoDir, worktreeDir := setupSharedWorktreeWorkspace(t)
	sharedBeadsDir := filepath.Join(mainRepoDir, ".beads")
	sharedDoltDir := filepath.Join(sharedBeadsDir, "dolt")
	if err := os.MkdirAll(sharedDoltDir, 0o755); err != nil {
		t.Fatalf("failed to create shared dolt dir: %v", err)
	}

	err := DatabaseIntegrity(worktreeDir)
	if !errors.Is(err, ErrTestBinary) {
		t.Fatalf("expected ErrTestBinary, got %v", err)
	}

	if _, statErr := os.Stat(sharedDoltDir); statErr != nil {
		t.Fatalf("expected shared dolt dir to be restored, got %v", statErr)
	}
	backupPaths, globErr := filepath.Glob(sharedDoltDir + ".*.corrupt.backup")
	if globErr != nil {
		t.Fatalf("glob backup paths: %v", globErr)
	}
	if len(backupPaths) != 0 {
		t.Fatalf("expected no leftover corrupt backup path, got %v", backupPaths)
	}
	if _, err := os.Stat(filepath.Join(worktreeDir, ".beads")); !os.IsNotExist(err) {
		t.Fatalf("expected no worktree-local .beads directory, got err=%v", err)
	}
}

// Adapted from #8077. The entire fixture, including the target of the alias,
// is disposable. The repair must refuse before moving the database entry.
func TestDatabaseIntegrityRefusesSymlinkedDatabase(t *testing.T) {
	dir := setupTestWorkspace(t)
	beadsDir := filepath.Join(dir, ".beads")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	doltDir := filepath.Join(beadsDir, "dolt")
	target := t.TempDir()
	if err := os.Symlink(target, doltDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := DatabaseIntegrity(dir)
	if err == nil || errors.Is(err, ErrTestBinary) || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("repair did not refuse the alias before recovery: %v", err)
	}
	if got, err := os.Readlink(doltDir); err != nil || got != target {
		t.Errorf("database alias = %q, %v, want unchanged %q", got, err, target)
	}
}

func TestIntegrityRollbackAdmissionRequiresConservedRecovery(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	for _, name := range []string{"missing recovery entry", "recovery in a different parent", "symbolic-link recovery entry"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			beadsDir := filepath.Join(root, ".beads")
			doltPath := filepath.Join(beadsDir, "dolt")
			backupPath := doltPath + ".fixture.corrupt.backup"
			held := filepath.Join(root, "retained-recovery")
			markers := map[string]string{
				filepath.Join(doltPath, "entry"): "current",
				filepath.Join(held, "entry"):     "retained recovery",
			}
			for path, content := range markers {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			target, err := captureRepairTarget(beadsDir, doltPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(target.close)
			switch name {
			case "recovery in a different parent":
				backupPath = held
			case "symbolic-link recovery entry":
				if err := os.Symlink(held, backupPath); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}

			// Exercise admission by itself. No database or recovery operation
			// runs, and the fixtures contain only inert marker files.
			if err := revalidateIntegrityRestore(target, backupPath); err == nil {
				t.Error("rollback admission accepted a missing or displaced recovery entry")
			}
			for path, want := range markers {
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Errorf("fixture entry %q changed: got %q, err %v, want %q", path, got, err, want)
				}
			}
		})
	}
}

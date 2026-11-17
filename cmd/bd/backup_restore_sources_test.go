package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/uow"
)

func restoreSourceFixture(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest"), []byte("native manifest fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidateBackupRestoreDirUsesLiteralNativeSourcePath(t *testing.T) {
	badNames := []string{"project%23archive"}
	if runtime.GOOS != "windows" {
		badNames = append(badNames, "project#archive", "project?archive", "project\\archive")
	}
	for _, name := range badNames {
		t.Run(name, func(t *testing.T) {
			if err := validateBackupRestoreDir(restoreSourceFixture(t, name)); err == nil {
				t.Errorf("restore admitted path %q even though Dolt reads a different path", name)
			}
		})
	}
	if err := validateBackupRestoreDir(restoreSourceFixture(t, "copia Ñ con espacios")); err != nil {
		t.Fatalf("restore refused a literal Unicode path with spaces: %v", err)
	}
}

type restoreAttemptStore struct {
	failingBackupStore
	restoreCalls int
}

func (s *restoreAttemptStore) RestoreDatabase(context.Context, string, bool) error {
	s.restoreCalls++
	return errors.New("restore reached backend")
}

func TestBackupRestoreRefusesOffHostBeforeOpeningSource(t *testing.T) {
	t.Setenv("BEADS_DOLT_SERVER_HOST", "192.0.2.10")
	s := &restoreAttemptStore{}
	dir := restoreSourceFixture(t, "valid local backup")
	err := runBackupRestore(t.Context(), s, dir, true)
	if err == nil || !strings.Contains(err.Error(), "filesystem") {
		t.Errorf("off-host restore error = %v, want refusal identifying the unchecked filesystem", err)
	}
	if s.restoreCalls != 0 {
		t.Errorf("unchecked remote filesystem reached RestoreDatabase %d times", s.restoreCalls)
	}
}

type restoreProxiedProbe struct {
	uow.UnitOfWorkProvider
	activeCalls, closeCalls int
}

func (p *restoreProxiedProbe) RunNonTx(context.Context, func(context.Context, *sql.Conn) error) error {
	p.activeCalls++
	return errors.New("restore reached live provider")
}

func (p *restoreProxiedProbe) Close(context.Context) error {
	p.closeCalls++
	return errors.New("restore attempted quiescence")
}

func TestBackupRestoreProxiedRejectsMangledPathBeforeQuiescence(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.Mkdir(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := (&configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeProxiedServer}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DIR", beadsDir)
	beads.ResetCaches()
	t.Cleanup(beads.ResetCaches)
	p := &restoreProxiedProbe{}
	previous := uowProvider
	uowProvider = p
	t.Cleanup(func() { uowProvider = previous })

	err := runBackupRestoreProxied(t.Context(), restoreSourceFixture(t, "project%23archive"), true)
	if err == nil || !strings.Contains(err.Error(), "cannot be restored where it is") {
		t.Errorf("proxied restore preflight error = %v, want the invalid source path", err)
	}
	if p.activeCalls != 0 || p.closeCalls != 0 {
		t.Errorf("invalid source touched the live provider (active=%d, close=%d)", p.activeCalls, p.closeCalls)
	}
}

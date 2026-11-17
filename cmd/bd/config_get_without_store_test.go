package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/git"
)

// Adapted from #7905. The recognized YAML key must finish before trying to
// open the selected workspace's unavailable server store.
func TestConfigGetYamlWithUnavailableSelectedStore(t *testing.T) {
	repo := t.TempDir()
	beadsDir := filepath.Join(repo, ".beads")
	writeTestConfigYAML(t, beadsDir, "dolt:\n  max-conns: 2\n")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{
		Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeServer,
		DoltDatabase: "config_get_fixture", DoltServerHost: "127.0.0.1", DoltServerPort: port,
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	t.Setenv("BEADS_DIR", beadsDir)
	for _, env := range []string{"BEADS_DB", "BD_DB", "BEADS_DOLT_DATA_DIR", "BEADS_DOLT_SERVER_DATABASE",
		"BEADS_DOLT_SERVER_HOST", "BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_SERVER_SOCKET", "BEADS_DOLT_PORT",
		"BEADS_DOLT_SHARED_SERVER", "BEADS_DOLT_SERVER_MODE"} {
		t.Setenv(env, "")
	}
	t.Setenv("BEADS_DOLT_AUTO_START", "0")
	beads.ResetCaches()
	config.ResetForTesting()
	git.ResetCaches()
	t.Cleanup(func() { beads.ResetCaches(); config.ResetForTesting(); git.ResetCaches() })
	savePersistentPreRunState(t)

	oldStore := store
	store = nil
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
		store = oldStore
	})
	if err := rootCmd.PersistentPreRunE(configGetCmd, []string{"dolt.max-conns"}); err != nil {
		t.Errorf("YAML-only config get attempted to open the store: %v", err)
	}
	if store != nil {
		t.Error("YAML-only config get acquired a store")
	}
	if _, err := os.Stat(filepath.Join(beadsDir, localVersionFile)); !os.IsNotExist(err) {
		t.Errorf("YAML-only config get touched the version stamp: %v", err)
	}
}

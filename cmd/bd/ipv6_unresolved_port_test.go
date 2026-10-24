//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// The process boundary verifies doctor exits unsuccessfully and all diagnostic
// commands reach their selected workspace without attempting a replacement server.
// Adapted from julianknutsen's PR #7803 regression matrix.
func TestDoltCommandsAdviseAPortForAServerBdDoesNotRun(t *testing.T) {
	bd := buildBDForInitTests(t)
	for _, host := range []string{"::1", "[::1]", "db.invalid"} {
		t.Run(host, func(t *testing.T) {
			root := t.TempDir()
			beadsDir := filepath.Join(root, ".beads")
			if err := os.MkdirAll(beadsDir, 0700); err != nil {
				t.Fatal(err)
			}
			cfg := &configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeServer, DoltServerHost: host, DoltDatabase: "ipv6_fixture"}
			if err := cfg.Save(beadsDir); err != nil {
				t.Fatal(err)
			}
			if host != "db.invalid" {
				if err := os.WriteFile(filepath.Join(beadsDir, "dolt-server.port"), []byte("13307"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{{"dolt", "status"}, {"dolt", "test"}, {"dolt", "show"}, {"dolt", "clean-databases", "--dry-run"}, {"doctor", "--server"}} {
				run := runBDInWorkspace(t, bd, root, args, []string{"BEADS_DIR=" + beadsDir, "BEADS_DOLT_AUTO_START=1"}, "")
				want := "bd does not manage the Dolt server at " + host + "; pin its port with BEADS_DOLT_SERVER_PORT or dolt.port"
				if !strings.Contains(run.output, want) {
					t.Errorf("%v: missing %q:\n%s", args, want, run.output)
				}
				for _, bad := range []string{":0", "nc -zv", "bd dolt start"} {
					if strings.Contains(run.output, bad) {
						t.Errorf("%v: contains %q:\n%s", args, bad, run.output)
					}
				}
				if args[0] == "doctor" && run.exitCode == 0 {
					t.Errorf("doctor --server succeeded without a configured port:\n%s", run.output)
				}
			}
			assertNoPhantomEmbeddedDatabase(t, beadsDir)
			for _, name := range []string{"dolt-server.pid", "dolt-server.log"} {
				if _, err := os.Stat(filepath.Join(beadsDir, name)); !os.IsNotExist(err) {
					t.Errorf("unexpected managed server state %s: %v", name, err)
				}
			}
		})
	}
}

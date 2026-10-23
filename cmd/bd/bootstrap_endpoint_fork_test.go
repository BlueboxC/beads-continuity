package main

import (
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"os"
	"path/filepath"
	"testing"
)

func TestForkBootstrapClonePortMatchesNextOpen(t *testing.T) {
	t.Setenv("BEADS_DOLT_SERVER_PORT", "43211")
	t.Setenv("BEADS_DOLT_PORT", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	d := t.TempDir()
	t.Setenv("BEADS_DIR", d)
	if err := os.WriteFile(filepath.Join(d, "config.yaml"), []byte("dolt:\n  mode: server\n  port: 43212\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Initialize(); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{DoltMode: configfile.DoltModeServer, DoltServerPort: 43213}
	if got := serverClonePort(d, cfg); got != 43211 {
		t.Fatalf("clone port %d, next open uses environment port 43211", got)
	}
}

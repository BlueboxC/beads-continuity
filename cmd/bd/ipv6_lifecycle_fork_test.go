package main

import (
	"context"
	"github.com/steveyegge/beads/internal/configfile"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestForkIPv6LifecycleRefusesBeforeManagedServer(t *testing.T) {
	bd := buildBDForInitTests(t)
	for _, host := range []string{"::1", "[::1]"} {
		for _, command := range []string{"start", "stop"} {
			t.Run(host+"/"+command, func(t *testing.T) {
				root := t.TempDir()
				dir := filepath.Join(root, ".beads")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				cfg := configfile.DefaultConfig()
				cfg.DoltMode = configfile.DoltModeServer
				cfg.DoltServerHost = host
				cfg.DoltServerPort = 43211
				if err := cfg.Save(dir); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(configfile.ConfigPath(dir))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bd, "dolt", command)
				cmd.Dir = root
				for _, entry := range os.Environ() {
					key, _, _ := strings.Cut(entry, "=")
					if !strings.HasPrefix(key, "BEADS_") && !strings.HasPrefix(key, "BD_") && !strings.HasPrefix(key, "GIT_") && key != "PATH" && key != "HOME" {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				cmd.Env = append(cmd.Env, "HOME="+root, "BEADS_DIR="+dir, "BEADS_DOLT_AUTO_START=0", "PATH="+filepath.Join(root, "empty-path"), "DOLT_DISABLE_EVENT_FLUSH=1")
				out, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(out), "configured Dolt server host is the IPv6 loopback") {
					t.Fatalf("%s failed to refuse before lifecycle: %v\n%s", command, err, out)
				}
				after, err := os.ReadFile(configfile.ConfigPath(dir))
				if err != nil || string(after) != string(before) {
					t.Fatal("selector changed", err)
				}
				for _, name := range []string{"dolt-server.pid", "dolt-server.port"} {
					if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
						t.Fatalf("refused %s created %s", command, name)
					}
				}
			})
		}
	}
}

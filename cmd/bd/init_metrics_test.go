//go:build cgo

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Real child commands must honor the fork policy even with a fresh HOME,
// saved upstream opt-in, and environment variables that used to re-enable it.
func TestForkCLIHasNoUsageMetrics(t *testing.T) {
	var requests atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	cfg := filepath.Join(xdg, "bd", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0700); err != nil {
		t.Fatal(err)
	}
	preimage := []byte("metrics:\n  disabled: false\n  endpoint: " + endpoint.URL + "\n")
	if err := os.WriteFile(cfg, preimage, 0600); err != nil {
		t.Fatal(err)
	}
	bin := buildEmbeddedBD(t)
	work := t.TempDir()
	initGitRepoAt(t, work)
	for _, args := range [][]string{
		{"version"}, {"metrics"}, {"metrics", "off"}, {"metrics", "example"},
		{"send-metrics"}, {"metrics", "on"},
		{"init", "--non-interactive", "--quiet", "--skip-hooks", "--skip-agents"},
		{"ready", "--json"},
	} {
		cmd := exec.Command(bin, args...)
		cmd.Dir = work
		cmd.Env = append(bdEnv(work), "HOME="+home, "XDG_CONFIG_HOME="+xdg,
			"BD_DISABLE_METRICS=0", "BD_DISABLE_EVENT_FLUSH=0", "DO_NOT_TRACK=0",
			"BEADS_TEST_MODE=0", "BEADS_METRICS_ENDPOINT="+endpoint.URL)
		out, err := cmd.CombinedOutput()
		if strings.Join(args, " ") == "metrics on" {
			if err == nil || !strings.Contains(string(out), "cannot be enabled") {
				t.Fatalf("on: %q %v", out, err)
			}
		} else if err != nil {
			t.Fatalf("%v: %q %v", args, out, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("usage HTTP requests: %d", requests.Load())
	}
	if _, err := os.Stat(filepath.Join(home, ".beads", "eventsData")); !os.IsNotExist(err) {
		t.Fatalf("usage queue exists: %v", err)
	}
	b, err := os.ReadFile(cfg)
	if err != nil || string(b) != string(preimage) {
		t.Fatalf("user config changed: %q %v", b, err)
	}
}

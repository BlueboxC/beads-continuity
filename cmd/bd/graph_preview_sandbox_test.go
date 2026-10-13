//go:build cgo

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

func TestGraphPreviewSandboxPreservesReadOnlyAndNoSync(t *testing.T) {
	bd := buildBDUnderTest(t)
	work, home := t.TempDir(), t.TempDir()
	graphPolicyCLI(t, bd, work, home, nil, "", "init", "--graph-mode", "link", "--scope-url", "https://example.invalid/sandbox/", "--skip-hooks", "--skip-agents", "--non-interactive")
	graphPolicyCLI(t, bd, work, home, nil, "", "create", "Preserve this task", "--id", "beads/work", "--json")
	cfg, err := configfile.LoadForDiscovery(filepath.Join(work, ".beads"))
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected sync", http.StatusServiceUnavailable)
	}))
	defer remote.Close()
	graphGenerationExec(t, cfg, "CALL DOLT_REMOTE('add','origin','"+remote.URL+"/repo')")
	writeFile(t, filepath.Join(work, ".beads", "config.yaml"), []byte("dolt.auto-push: true\ndolt.local-only: false\n"))

	bin := t.TempDir()
	log := filepath.Join(bin, "sync.log")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	gitSpy := fmt.Sprintf("#!/bin/sh\nfor arg in \"$@\"; do\n case \"$arg\" in push|pull|fetch|ls-remote) printf 'git:%%s\\n' \"$*\" >> %s; exit 97;; esac\ndone\nexec %s \"$@\"\n", quote(log), quote(git))
	doltSpy := fmt.Sprintf("#!/bin/sh\nprintf 'dolt:%%s\\n' \"$*\" >> %s\nexit 97\n", quote(log))
	for name, body := range map[string]string{"git": gitSpy, "dolt": doltSpy} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "BEADS_ACTOR=sandbox-test"}
	options := graphOptionsFor(cfg.GraphWorkspace, cfg)
	before, err := graphstore.InspectConversionState(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	reads := []struct {
		name string
		args []string
	}{
		{"show", []string{"show", "beads/work", "--json"}},
		{"ready", []string{"ready", "--json"}},
		{"status", []string{"status", "--graph", "--json"}},
		{"versions", []string{"versions", "beads/work", "--json"}},
	}
	for _, read := range reads {
		t.Run(read.name, func(t *testing.T) {
			plain := graphPolicyCLI(t, bd, work, home, env, "", append([]string{"--readonly"}, read.args...)...)
			for _, flag := range []string{"--sandbox", "--sandbox=false"} {
				got := graphPolicyCLI(t, bd, work, home, env, "", append([]string{flag, "--readonly"}, read.args...)...)
				if got != plain {
					t.Fatalf("%s changed the read result", flag)
				}
			}
		})
	}
	graphPolicyCLI(t, bd, work, home, env, "permission_denied", "--sandbox", "--readonly", "create", "Refused", "--id", "beads/refused", "--json")
	graphPolicyCLI(t, bd, work, home, env, "capability_unavailable", "--sandbox", "--ignore-schema-skew", "show", "beads/work", "--json")
	graphPolicyCLI(t, bd, work, home, env, "capability_unavailable", "--sandbox", "dolt", "push", "--json")
	after, err := graphstore.InspectConversionState(t.Context(), options)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("sandbox reads or refusals changed database roots: %v", err)
	}
	graphPolicyCLI(t, bd, work, home, env, "", "--sandbox", "create", "Explicit local write", "--id", "beads/local", "--json")
	graphPolicyCLI(t, bd, work, home, env, "", "--sandbox", "--readonly", "show", "beads/local", "--json")
	if requests.Load() != 0 {
		t.Fatalf("graph commands contacted the configured remote %d times", requests.Load())
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("graph commands invoked external sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, ".beads", "push-state.json")); !os.IsNotExist(err) {
		t.Fatalf("graph commands entered native auto-push housekeeping: %v", err)
	}
}

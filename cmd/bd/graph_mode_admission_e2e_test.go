//go:build cgo

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A refusal in a helper is insufficient: ordinary commands must reach it before
// metadata migration, version tracking, or an attempt to provision/open storage.
func TestGraphModeCLIRefusesLegacyStoreWithoutWorkspaceEffects(t *testing.T) {
	bd := buildBDUnderTest(t)
	for _, filename := range []string{"metadata.json", "config.json"} {
		for _, mode := range []string{"link", "future-format"} {
			for _, args := range [][]string{
				{"show", "beads/plan"},
				{"list", "--json"},
				{"context", "--json"},
				{"init", "--force", "--quiet", "--non-interactive", "--skip-hooks", "--skip-agents"},
			} {
				t.Run(filename+"/"+mode+"/"+args[0], func(t *testing.T) {
					repoDir := t.TempDir()
					initGitRepoAt(t, repoDir)
					beadsDir := filepath.Join(repoDir, ".beads")
					writeFile(t, filepath.Join(beadsDir, filename), []byte(`{"backend":"dolt","dolt_mode":"server","graph_mode":"`+mode+`"}`))
					before := legacyUpgradeTreeDigest(t, beadsDir)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, bd, args...)
					cmd.Dir = repoDir
					cmd.Env = append(os.Environ(),
						"HOME="+t.TempDir(),
						"BD_DISABLE_METRICS=1",
						"BD_DISABLE_EVENT_FLUSH=1",
						"BEADS_DOLT_AUTO_START=0",
					)
					output, err := cmd.CombinedOutput()
					if ctx.Err() != nil {
						t.Fatalf("command failed to refuse promptly: %v\n%s", ctx.Err(), output)
					}
					if err == nil || !strings.Contains(string(output), "graph_mode") {
						t.Fatalf("expected graph-mode refusal, got %v\n%s", err, output)
					}
					if after := legacyUpgradeTreeDigest(t, beadsDir); after != before {
						t.Fatalf("refused command changed workspace: before %s, after %s\n%s", before, after, output)
					}
				})
			}
		}
	}
}

func TestVersionFlagsIgnoreWorkspaceStorage(t *testing.T) {
	bd := buildBDUnderTest(t)
	for _, metadata := range []string{`{"backend":"dolt","dolt_mode":"embedded","graph_mode":"link"}`, `{"backend":"dolt","graph_mode":"future-format"}`, `{broken`} {
		for _, flag := range []string{"--version", "-V"} {
			t.Run(metadata+"/"+flag, func(t *testing.T) {
				dir := t.TempDir()
				beadsDir := filepath.Join(dir, ".beads")
				writeFile(t, filepath.Join(beadsDir, "metadata.json"), []byte(metadata))
				before := legacyUpgradeTreeDigest(t, beadsDir)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, bd, flag)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "BEADS_DIR="+beadsDir, "BD_DISABLE_METRICS=1", "BEADS_DOLT_AUTO_START=0")
				out, err := cmd.CombinedOutput()
				if err != nil || !strings.Contains(string(out), "bd version ") {
					t.Fatalf("version diagnostic failed: %v / %s", err, out)
				}
				if after := legacyUpgradeTreeDigest(t, beadsDir); after != before {
					t.Fatal("version flag changed workspace")
				}
			})
		}
	}
}

//go:build cgo

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestForkRoutedJSONLOnlyTargetRefusesWithoutWrites(t *testing.T) {
	pinLegacyUpgradeGuardEnv(t)
	for _, entry := range []struct {
		name  string
		check func(string) error
	}{
		{"admission", refuseUninitializableTarget},
		{"dry-run", func(dir string) error { return checkRepoTargetInitializable(filepath.Dir(dir), true) }},
		{"planning", refuseUninitializablePlanningRepo},
		{"create initializer", func(dir string) error {
			return ensureBeadsDirForPath(context.Background(), filepath.Dir(dir), nil, true)
		}},
	} {
		t.Run(entry.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "project with ' quote", ".beads")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "issues.jsonl"), []byte(`{"id":"old-1","title":"preserved"}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := legacyUpgradeTreeDigest(t, dir)
			err := entry.check(dir)
			if err == nil || !strings.Contains(err.Error(), "bd init --from-jsonl") {
				t.Errorf("expected explicit import refusal, got %v", err)
			}
			if got := legacyUpgradeTreeDigest(t, dir); got != before {
				t.Fatal("target changed before explicit import")
			}
		})
	}
}

func TestForkRoutedJSONLAdmissionControls(t *testing.T) {
	pinLegacyUpgradeGuardEnv(t)
	for _, tc := range []string{"empty", "directory", "configured", "redirect"} {
		t.Run(tc, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), ".beads")
			if err := os.MkdirAll(dir, 0o750); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "issues.jsonl")
			if tc == "directory" {
				if err := os.Mkdir(path, 0o750); err != nil {
					t.Fatal(err)
				}
			} else {
				body := ""
				if tc != "empty" {
					body = "opaque nonempty export"
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc == "configured" {
				if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("dolt:\n  mode: embedded\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc == "redirect" {
				target := t.TempDir()
				if err := os.WriteFile(filepath.Join(target, "metadata.json"), []byte(`{"backend":"dolt"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "redirect"), []byte(target), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := legacyUpgradeTreeDigest(t, dir)
			if err := refuseUninitializableTarget(dir); err != nil {
				t.Fatalf("valid control refused: %v", err)
			}
			if legacyUpgradeTreeDigest(t, dir) != before {
				t.Fatal("admission control changed target")
			}
		})
	}
}

func TestEmbeddedForkJSONLOnlyRoutesAndRecovery(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("embedded integration opt-in")
	}
	bd := buildEmbeddedBD(t)
	source, _, _ := bdInit(t, bd, "--prefix", "source")
	personal := bdCreate(t, bd, source, "retain personal source", "--actor", "migration-test", "--repo", source)
	target := t.TempDir()
	initGitRepoAt(t, target)
	dir := filepath.Join(target, ".beads")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "issues.jsonl"), []byte(`{"id":"old-1","title":"historical solution","status":"open","priority":2,"issue_type":"task"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"routing.contributor", target}, {"routing.mode", "auto"}} {
		if out, err := bdRunWithFlockRetry(t, bd, source, append([]string{"config", "set"}, args...)...); err != nil {
			t.Fatalf("routing setup: %v %s", err, out)
		}
	}
	cmd := exec.Command("git", "config", "beads.role", "contributor")
	cmd.Dir = source
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("contributor setup: %v %s", err, out)
	}
	for _, args := range [][]string{
		{"create", "explicit", "--repo", target}, {"create", "preview", "--repo", target, "--dry-run"},
		{"create", "automatic"}, {"migrate-personal", "--yes", "--actor", "migration-test"},
	} {
		before := legacyUpgradeTreeDigest(t, dir)
		out, err := bdRunWithFlockRetry(t, bd, source, args...)
		if err == nil || !strings.Contains(string(out), "bd init --from-jsonl") {
			t.Fatalf("%v did not refuse with recovery: %v %s", args, err, out)
		}
		if legacyUpgradeTreeDigest(t, dir) != before {
			t.Fatalf("%v changed target", args)
		}
	}
	if got := bdShow(t, bd, source, personal.ID); got.Title != personal.Title {
		t.Fatal("personal issue changed on refused migration")
	}
	if out, err := bdRunWithFlockRetry(t, bd, target, "init", "--from-jsonl", "--prefix", "old", "--quiet", "--non-interactive", "--skip-hooks"); err != nil {
		t.Fatalf("explicit recovery failed: %v %s", err, out)
	}
	created := bdCreate(t, bd, source, "new beside historical solution", "--repo", target)
	if got := bdShow(t, bd, target, "old-1"); got.Title != "historical solution" {
		t.Fatal("historical solution lost")
	}
	if got := bdShow(t, bd, target, created.ID); got.Title != created.Title {
		t.Fatal("new routed issue lost")
	}
}

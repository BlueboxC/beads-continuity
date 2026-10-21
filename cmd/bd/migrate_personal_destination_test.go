//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// PR #7751: the destination must follow its own configuration and retain the
// only source copy on every preflight refusal. No server is used by this test.
func TestEmbeddedMigratePersonalOwnDestination(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("embedded integration opt-in")
	}
	bd := buildEmbeddedBD(t)
	for _, scenario := range []string{"move", "self", "collision", "missing database", "launcher override", "empty planning", "adopt planning", "unnamed database", "same identity", "dirty planning", "batch"} {
		t.Run(scenario, func(t *testing.T) {
			dir, sourceBeadsDir, _ := bdInit(t, bd, "--prefix", "source")
			planning := setupContributorRouting(t, bd, dir)
			issue := bdCreate(t, bd, dir, "keep source until durable copy", "--actor", "migration-test", "--repo", dir)
			bdComment(t, bd, dir, issue.ID, "preserve original comment")
			beforeHead := ""
			if scenario == "batch" {
				beforeHead = embeddedCurrentCommit(t, filepath.Join(planning, ".beads"), "pl")
			}
			switch scenario {
			case "self":
				if _, err := bdRunWithFlockRetry(t, bd, dir, "config", "set", "routing.contributor", dir); err != nil {
					t.Fatal(err)
				}
			case "collision":
				bdCreate(t, bd, planning, "do not overwrite", "--id", issue.ID, "--force")
			case "empty planning", "adopt planning":
				if err := os.Remove(filepath.Join(planning, ".beads", "metadata.json")); err != nil {
					t.Fatal(err)
				}
				if scenario == "empty planning" {
					if err := os.RemoveAll(filepath.Join(planning, ".beads", "embeddeddolt")); err != nil {
						t.Fatal(err)
					}
				}
			case "unnamed database", "same identity":
				path := filepath.Join(planning, ".beads", "metadata.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var metadata map[string]any
				if err := json.Unmarshal(data, &metadata); err != nil {
					t.Fatal(err)
				}
				if scenario == "unnamed database" {
					delete(metadata, "dolt_database")
				} else {
					source, err := os.ReadFile(filepath.Join(sourceBeadsDir, "metadata.json"))
					if err != nil {
						t.Fatal(err)
					}
					var sourceMetadata map[string]any
					if err := json.Unmarshal(source, &sourceMetadata); err != nil {
						t.Fatal(err)
					}
					metadata["project_id"] = sourceMetadata["project_id"]
				}
				data, err = json.Marshal(metadata)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "dirty planning":
				bdCreate(t, bd, planning, "unrelated pending work", "--dolt-auto-commit", "off")
			case "missing database":
				if err := os.RemoveAll(filepath.Join(planning, ".beads", "embeddeddolt")); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(bd, "migrate-personal", "--yes", "--actor", "migration-test")
			if scenario == "batch" {
				cmd.Args = append(cmd.Args, "--dolt-auto-commit", "batch")
			}
			cmd.Dir, cmd.Env = dir, bdEnv(dir)
			if scenario == "launcher override" {
				cmd.Env = append(cmd.Env, "BEADS_DOLT_SERVER_DATABASE=source")
			}
			out, err := cmd.CombinedOutput()
			if scenario == "move" || scenario == "launcher override" || scenario == "empty planning" || scenario == "adopt planning" || scenario == "batch" {
				if err != nil {
					t.Fatalf("migration failed: %v\n%s", err, out)
				}
				if bdShowSucceeds(t, bd, dir, issue.ID) {
					t.Fatal("source still present after successful migration")
				}
				if got := bdShow(t, bd, planning, issue.ID); got.Title != issue.Title {
					t.Fatalf("destination changed: %+v", got)
				}
				comments := exec.Command(bd, "comments", issue.ID)
				comments.Dir, comments.Env = planning, bdEnv(planning)
				body, err := comments.CombinedOutput()
				if err != nil || !strings.Contains(string(body), "preserve original comment") {
					t.Fatalf("comment lost: %v %s", err, body)
				}
				if scenario == "batch" && embeddedCurrentCommit(t, filepath.Join(planning, ".beads"), "pl") != beforeHead {
					t.Fatal("batch migration committed the destination")
				}
			} else {
				if err == nil {
					t.Fatalf("unsafe migration succeeded: %s", out)
				}
				if !bdShowSucceeds(t, bd, dir, issue.ID) {
					t.Fatalf("source lost: %s", out)
				}
				if scenario == "collision" && bdShow(t, bd, planning, issue.ID).Title != "do not overwrite" {
					t.Fatal("existing destination overwritten")
				}
				if scenario == "missing database" {
					if _, err := os.Stat(filepath.Join(planning, ".beads", "embeddeddolt")); !os.IsNotExist(err) {
						t.Fatal("missing destination was provisioned")
					}
				}
			}
		})
	}
}

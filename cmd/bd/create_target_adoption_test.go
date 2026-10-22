//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

// Exercise the routed create boundary with a real retained target database.
func TestEmbeddedCreateAdoptsExistingTarget(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("embedded integration opt-in")
	}
	bd := buildEmbeddedBD(t)
	for _, scenario := range []string{"adopt", "dirty", "server", "local server", "several", "graph"} {
		t.Run(scenario, func(t *testing.T) {
			source, _, _ := bdInit(t, bd, "--prefix", "source")
			target, beadsDir, _ := bdInit(t, bd, "--prefix", "kept")
			kept := bdCreate(t, bd, target, "retain established target solution")
			if scenario == "dirty" {
				bdCreate(t, bd, target, "keep pending work", "--dolt-auto-commit", "off")
			}
			if err := os.Remove(filepath.Join(beadsDir, "metadata.json")); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "server":
				if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("dolt:\n  mode: server\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "local server":
				for name, body := range map[string]string{"config.yaml": "dolt:\n  mode: embedded\n", "config.local.yaml": "dolt:\n  mode: server\n"} {
					if err := os.WriteFile(filepath.Join(beadsDir, name), []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "several":
				s, err := embeddeddolt.Open(t.Context(), beadsDir, "other", "main")
				if err != nil {
					t.Fatal(err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			case "graph":
				if err := os.WriteFile(filepath.Join(beadsDir, graphPreviewMarker), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := embeddeddolt.Databases(beadsDir)
			if scenario == "adopt" {
				created := bdCreate(t, bd, source, "new routed work", "--repo", target)
				if !strings.HasPrefix(created.ID, "kept-") {
					t.Fatalf("adopted prefix changed: %s", created.ID)
				}
				if got := bdShow(t, bd, target, kept.ID); got.Title != kept.Title {
					t.Fatalf("established issue changed: %+v", got)
				}
			} else {
				out, err := bdRunWithFlockRetry(t, bd, source, "create", "new routed work", "--repo", target)
				if err == nil {
					t.Fatalf("unsafe target accepted: %s", out)
				}
				if _, err := os.Stat(filepath.Join(beadsDir, "metadata.json")); !os.IsNotExist(err) {
					t.Fatalf("refused target acquired metadata: %v", err)
				}
			}
			if after := embeddeddolt.Databases(beadsDir); !slices.Equal(before, after) {
				t.Fatalf("target databases changed: %v -> %v", before, after)
			}
		})
	}
}

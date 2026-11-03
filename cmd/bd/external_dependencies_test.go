//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

func TestConfiguredExternalResolverEmbeddedCapabilities(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	// Config is process-global, so this fixture must remain serial.
	clonePortTestEnv(t)
	ctx := t.Context()
	root := t.TempDir()
	provider := filepath.Join(root, "provider")
	beadsDir := filepath.Join(provider, ".beads")
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := &configfile.Config{
		Database: "dolt", Backend: configfile.BackendDolt,
		DoltMode: configfile.DoltModeEmbedded, DoltDatabase: "external",
	}
	if err := metadata.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	func() {
		s, err := embeddeddolt.Open(ctx, beadsDir, "external", "main")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}()
		if err := s.SetConfig(ctx, "issue_prefix", "ext"); err != nil {
			t.Fatal(err)
		}
		for _, issue := range []*types.Issue{
			{ID: "ext-provides", Title: "Open provider", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2, Labels: []string{"provides:export:api"}},
			{ID: "ext-export", Title: "Export declaration only", Status: types.StatusOpen, IssueType: types.TypeTask, Priority: 2, Labels: []string{"export:api"}},
		} {
			if err := s.CreateIssue(ctx, issue, "fixture"); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CloseIssue(ctx, "ext-export", "done", "fixture", ""); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Mkdir(filepath.Join(root, "offline"), 0o700); err != nil {
		t.Fatal(err)
	}
	consumer := filepath.Join(root, "consumer", ".beads")
	writeClonePortFile(t, filepath.Join(consumer, "config.yaml"), "external_projects:\n  upstream: ../provider\n  offline: ../offline\n")
	loadClonePortConfig(t, consumer)
	resolve := configuredExternalResolver()
	refs := []string{
		"external:upstream:export:api", "external:upstream:missing",
		"external:unmapped:export:api", "external:offline:export:api",
	}
	check := func(t *testing.T, want map[string]bool) {
		t.Helper()
		got, err := resolve(ctx, refs)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("resolved capabilities = %v, %v; want %v", got, err, want)
		}
	}
	t.Run("open-provides-and-closed-export-stay-unsatisfied", func(t *testing.T) {
		check(t, map[string]bool{})
	})
	t.Run("closed-provides-preserves-colon-capability", func(t *testing.T) {
		func() {
			s, err := embeddeddolt.Open(ctx, beadsDir, "external", "main")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
			}()
			if err := s.CloseIssue(ctx, "ext-provides", "delivered", "fixture", ""); err != nil {
				t.Fatal(err)
			}
		}()
		check(t, map[string]bool{"external:upstream:export:api": true})
	})
	t.Run("moved-provider-stays-unsatisfied", func(t *testing.T) {
		if err := os.Rename(provider, filepath.Join(root, "moved-provider")); err != nil {
			t.Fatal(err)
		}
		check(t, map[string]bool{})
	})
}

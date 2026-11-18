//go:build cgo

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/issueops"
)

// Exercise detection together with the real embedded marker role and reopen.
// autoMigrateOnVersionBump is a server-only front door on this fork; this test
// deliberately uses the existing embedded factory rather than claiming that
// server-only function ran against an embedded database.
func TestVersionBump_EmbeddedCandidateMarkers(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	marker := newTrackingWorkspace(t, "1.4.0-rc.3", "1.4.0")
	beadsDir := filepath.Dir(marker)
	if err := (&configfile.Config{
		Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded,
		DoltDatabase: "version_candidate",
	}).Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	seed, err := newDoltStoreFromConfig(ctx, beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"bd_version", "bd_version_max"} {
		if err := seed.SetLocalMetadata(ctx, key, "1.4.0-rc.3"); err != nil {
			_ = seed.Close()
			t.Fatal(err)
		}
	}
	head, err := seed.GetCurrentCommit(ctx)
	if err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		version string
		upgrade bool
	}{
		{"1.4.0", true},
		{"1.4.0-rc.3", false},
		{"1.4.0", true},
	} {
		Version, versionUpgradeDetected, previousVersion = step.version, false, ""
		trackBdVersion()
		if versionUpgradeDetected != step.upgrade {
			t.Errorf("%s upgrade detected=%v, want %v", step.version, versionUpgradeDetected, step.upgrade)
		}
		func() {
			store, err := newDoltStoreFromConfig(ctx, beadsDir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			}()
			reconciler, err := store.VersionReconciler()
			if err != nil {
				t.Fatal(err)
			}
			if versionUpgradeDetected {
				if _, err := reconciler.ReconcileVersion(ctx, issueops.VersionReconcileRequest{CLIVersion: Version}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := reconciler.RecordedVersion(ctx, issueops.RecordedVersionRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if got.Recorded != "1.4.0" || got.HighWaterMark != "1.4.0-rc.3" {
				t.Errorf("after %s markers=%+v, want recorded=1.4.0 and unchanged high-water mark=1.4.0-rc.3", step.version, got)
			}
			if after, err := store.GetCurrentCommit(ctx); err != nil || after != head {
				t.Errorf("version-only reconciliation changed native HEAD: %q -> %q (%v)", head, after, err)
			}
		}()
	}
}

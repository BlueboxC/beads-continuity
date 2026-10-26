package main

import (
	"errors"
	"fmt"
	"github.com/steveyegge/beads/cmd/bd/doctor"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForkBootstrapDowngradesOnlyCompletedGitignorePermissions(t *testing.T) {
	for _, soft := range []bool{true, false} {
		t.Run(fmt.Sprint(soft), func(t *testing.T) {
			snapshotBootstrapEnv(t)
			dir := filepath.Join(t.TempDir(), ".beads")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			original := doctorEnsureGitignoreForBeadsDir
			cause := errors.New("cannot write required patterns")
			if soft {
				cause = fmt.Errorf("%w: permission denied", doctor.ErrGitignorePermissions)
			}
			doctorEnsureGitignoreForBeadsDir = func(string) error { return cause }
			t.Cleanup(func() { doctorEnsureGitignoreForBeadsDir = original })
			var err error
			warning := captureStderrDuring(t, func() {
				err = finalizeSyncedBootstrap(dir, "file:///fixture-remote", configfile.DefaultConfig(), "beads")
			})
			if soft {
				if err != nil || !strings.Contains(warning, "Warning:") {
					t.Fatalf("completed patterns must warn: %v / %s", err, warning)
				}
				if got := config.GetStringFromDir(dir, "sync.remote"); got != "file:///fixture-remote" {
					t.Fatalf("remote not persisted: %q", got)
				}
			} else if !errors.Is(err, cause) {
				t.Fatalf("pattern failure lost: %v", err)
			}
		})
	}
}

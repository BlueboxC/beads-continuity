package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
)

// The fixture is an unsupported placeholder. No test launches a transfer
// program with it.
const optionShapedRemoteFixture = "-fixture-remote"

func isRemoteOptionRefusal(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "start") && (strings.Contains(text, "'-'") || strings.Contains(text, "dash"))
}

func TestResolveSyncRemoteRefusalNamesSelectedSetting(t *testing.T) {
	newForgeSyncRemoteWorkspace(t, "")
	const allowed = "https://example.invalid/project"
	config.Set("sync.git-remote", optionShapedRemoteFixture)
	if remote, err := resolveSyncRemoteChecked(); remote != "" || err == nil || !strings.Contains(err.Error(), "sync.git-remote") {
		t.Errorf("deprecated remote = %q, err = %v, want refusal naming sync.git-remote", remote, err)
	}
	config.Set("sync.remote", allowed)
	if remote, err := resolveSyncRemoteChecked(); remote != allowed || err != nil {
		t.Errorf("primary remote = %q, err = %v, want accepted %q", remote, err, allowed)
	}
	config.Set("sync.remote", "ssh://%2Dfixture-user@example.invalid/project")
	config.Set("sync.git-remote", allowed)
	if remote, err := resolveSyncRemoteChecked(); remote != "" || err == nil ||
		!strings.Contains(err.Error(), "sync.remote") || !strings.Contains(err.Error(), "user name") {
		t.Errorf("primary remote = %q, err = %v, want refusal naming the SSH user and sync.remote", remote, err)
	}
	if err := validateInitRemoteSelection(allowed, true); err != nil {
		t.Errorf("valid explicit --remote could not override the configured remote: %v", err)
	}
	if err := validateInitRemoteSelection(optionShapedRemoteFixture, true); err == nil || !strings.Contains(err.Error(), "--remote") {
		t.Errorf("explicit --remote refusal = %v, want error naming --remote", err)
	}
}

// Adapted from #7881. Configured sync remotes remain authoritative and local
// recovery remains available when that configuration is refused.
func TestBootstrapRefusesOptionShapedSyncRemoteWithoutProbing(t *testing.T) {
	for _, remote := range []string{optionShapedRemoteFixture, "git+" + optionShapedRemoteFixture} {
		t.Run(remote, func(t *testing.T) {
			beadsDir := newForgeSyncRemoteWorkspace(t, remote)
			stubProbeGitRemoteDoltData(t, func(string) (bool, error) {
				t.Error("bootstrap attempted a probe for a refused remote")
				return false, nil
			})
			plan := detectBootstrapAction(beadsDir, configfile.DefaultConfig())
			if plan.Action != "none" || !plan.Blocked || plan.SyncRemote != "" || !isRemoteOptionRefusal(plan.Reason) {
				t.Errorf("plan = %+v, want blocked remote refusal", plan)
			}
			backup := filepath.Join(beadsDir, "backup")
			if err := os.Mkdir(backup, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(backup, "issues.jsonl"), []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			plan = detectBootstrapAction(beadsDir, configfile.DefaultConfig())
			if plan.Action != "restore" || plan.Blocked || plan.SyncRemote != "" {
				t.Errorf("plan = %+v, want local recovery without persisting refused remote", plan)
			}
		})
	}
}

func TestConfigApplyRefusesOptionShapedFederationRemoteBeforeStoreOpen(t *testing.T) {
	beadsDir := newForgeSyncRemoteWorkspace(t, "")
	config.Set("federation.remote", optionShapedRemoteFixture)
	// A baseline without the guard hits a controlled metadata error before
	// store startup. The fixed path reports the remote setting instead.
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{true, false} {
		result := applyRemote(true, dryRun)
		if result.Status != applyStatusError || !strings.Contains(result.Error, "federation.remote") ||
			!isRemoteOptionRefusal(result.Error) {
			t.Errorf("applyRemote(dryRun=%v) = %+v, want configured remote refused before metadata load", dryRun, result)
		}
	}
}

func TestInitRefusesOptionShapedSyncRemoteBeforeArtifacts(t *testing.T) {
	beadsDir := newForgeSyncRemoteWorkspace(t, optionShapedRemoteFixture)
	t.Setenv("BD_NON_INTERACTIVE", "1")
	t.Setenv("BEADS_DOLT_AUTO_START", "0")
	t.Setenv("PATH", t.TempDir())
	setInitFlag(t, "quiet", "true")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	oldCmdCtx, oldRootCtx := initCmd.Context(), rootCtx
	initCmd.SetContext(ctx)
	rootCtx = ctx
	t.Cleanup(func() {
		initCmd.SetContext(oldCmdCtx)
		rootCtx = oldRootCtx
	})
	var runErr error
	stderr := captureStderr(t, func() { runErr = initCmd.RunE(initCmd, nil) })
	if runErr == nil || !strings.Contains(runErr.Error()+stderr, "sync.remote") ||
		!isRemoteOptionRefusal(runErr.Error()+stderr) {
		t.Errorf("init refusal = %v, stderr %q", runErr, stderr)
	}
	for _, path := range []string{filepath.Join(beadsDir, "metadata.json"), filepath.Join(filepath.Dir(beadsDir), ".git")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("init touched %s before remote validation: %v", path, err)
		}
	}
}

func TestProxiedInitRefusesOptionShapedSyncRemoteBeforeArtifacts(t *testing.T) {
	beadsDir := newForgeSyncRemoteWorkspace(t, optionShapedRemoteFixture)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BEADS_DOLT_AUTO_START", "0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runInitProxiedServer(&cobra.Command{}, ctx, initProxiedServerInput{
		externalConfig: &configfile.ExternalDoltConfig{Host: "127.0.0.1", Port: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "sync.remote") || !isRemoteOptionRefusal(err.Error()) {
		t.Errorf("proxied init refusal = %v, want selected remote validation", err)
	}
	for _, path := range []string{filepath.Join(beadsDir, "metadata.json"), filepath.Join(filepath.Dir(beadsDir), ".git")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("proxied init touched %s before remote validation: %v", path, err)
		}
	}
}

func TestGitProbeAndResetRefuseOptionShapedRemotesBeforeToolLookup(t *testing.T) {
	// No executable is available in this fixture. The baseline's lookup
	// error must be replaced by a direct argument validation error.
	t.Setenv("PATH", t.TempDir())
	for _, tt := range []struct {
		name string
		run  func() error
	}{
		{"sync ref probe", func() error {
			_, err := gitRemoteHasDoltDataRefStatus(optionShapedRemoteFixture)
			return err
		}},
		{"reset ref listing", func() error {
			_, err := lsRemoteDoltDataRefs(t.Context(), optionShapedRemoteFixture)
			return err
		}},
		{"reset ref removal", func() error {
			return deleteGitDoltDataRefs(t.Context(), optionShapedRemoteFixture, []string{gitDoltDataRef})
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil || !isRemoteOptionRefusal(err.Error()) {
				t.Errorf("refusal = %v, want option-shaped remote validation", err)
			}
		})
	}
}

// Adapted from #8089. The CLI wrappers should drop transport credentials
// and retain harmless query/fragment context in both output surfaces.
func TestRemoteDiagnosticsPreserveQueryAndFragmentWhileRedactingUserinfo(t *testing.T) {
	for _, tt := range []struct{ name, raw, want string }{
		{
			"HTTPS",
			"https://fixture-user:fixture'password@example.invalid/project?branch=main#selected",
			"https://example.invalid/project?branch=main#selected",
		},
		{
			"SSH",
			"git+ssh://fixture-user:fixture'password@example.invalid/project?branch=main#selected",
			"git+ssh://fixture-user@example.invalid/project?branch=main#selected",
		},
		{
			"credential-free",
			"https://example.invalid/?branch=main#selected",
			"https://example.invalid/?branch=main#selected",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactRemoteURL(tt.raw); got != tt.want {
				t.Errorf("redactRemoteURL returned %q, want %q", got, tt.want)
			}
			before, after := "Git reported: '", "' (connection unavailable)"
			want := before + tt.want + after
			if got := scrubURLCredentials(before + tt.raw + after); got != want {
				t.Errorf("scrubURLCredentials returned %q, want %q", got, want)
			}
		})
	}
}

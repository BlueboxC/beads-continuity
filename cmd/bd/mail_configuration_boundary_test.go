package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
)

type delegateConfigFixture struct {
	storage.DoltStorage
}

func (delegateConfigFixture) GetConfig(_ context.Context, key string) (string, error) {
	if key == "mail.delegate" {
		return "workspace-fixture", nil
	}
	return "", nil
}

// Adapted from #7879. Resolving the delegate must not execute it.
func TestMailDelegateIgnoresWorkspaceAndUsesUserConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("BEADS_MAIL_DELEGATE", "")
	t.Setenv("BD_MAIL_DELEGATE", "")

	oldStore, oldCtx := store, rootCtx
	t.Cleanup(func() { store, rootCtx = oldStore, oldCtx })
	store, rootCtx = delegateConfigFixture{}, context.Background()
	if got := findMailDelegate(); got != "" {
		t.Errorf("workspace setting selected delegate %q", got)
	}

	path, err := config.UserConfigYamlPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mail:\n  delegate: operator-fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findMailDelegate(); got != "operator-fixture" {
		t.Errorf("user-owned config selected delegate %q", got)
	}

	t.Setenv("BEADS_MAIL_DELEGATE", "env-fixture")
	if got := findMailDelegate(); got != "env-fixture" {
		t.Errorf("operator env selected delegate %q", got)
	}
}

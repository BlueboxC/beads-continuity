package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBeadsEnvFile(t *testing.T) {
	t.Run("loads env vars from .env file", func(t *testing.T) {
		dir := t.TempDir()
		envFile := filepath.Join(dir, ".env")
		if err := os.WriteFile(envFile, []byte("BEADS_DOLT_PASSWORD=hello_from_env\n"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BEADS_DOLT_PASSWORD", "") // clear
		os.Unsetenv("BEADS_DOLT_PASSWORD")

		loadBeadsEnvFile(dir)

		if got := os.Getenv("BEADS_DOLT_PASSWORD"); got != "hello_from_env" {
			t.Errorf("expected BEADS_DOLT_PASSWORD=hello_from_env, got %q", got)
		}
		os.Unsetenv("BEADS_DOLT_PASSWORD")
	})

	t.Run("shell env takes precedence over .env", func(t *testing.T) {
		dir := t.TempDir()
		envFile := filepath.Join(dir, ".env")
		if err := os.WriteFile(envFile, []byte("BEADS_DOLT_PASSWORD=from_file\n"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("BEADS_DOLT_PASSWORD", "from_shell")

		loadBeadsEnvFile(dir)

		if got := os.Getenv("BEADS_DOLT_PASSWORD"); got != "from_shell" {
			t.Errorf("expected shell env to win, got %q", got)
		}
	})

	t.Run("no-op when .env does not exist", func(t *testing.T) {
		dir := t.TempDir()
		// Should not panic or error
		loadBeadsEnvFile(dir)
	})

	t.Run("no-op when beadsDir is empty", func(t *testing.T) {
		// Should not panic or error
		loadBeadsEnvFile("")
	})
}

// Adapted from the contributor's #8137 checks for both project dotenv readers.
func TestProjectEnvRejectsInterpolation(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  string
		load func(string)
	}{
		{"full load", "BEADS_DOLT_SERVER_USER", loadBeadsEnvFile},
		{"selection load", "BEADS_DB", loadBeadsSelectionEnvFile},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(tt.key, "")
			if err := os.Unsetenv(tt.key); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BD_LITERAL_FIXTURE", "example")
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tt.key+"=sample-${BD_LITERAL_FIXTURE}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			tt.load(dir)
			if _, present := os.LookupEnv(tt.key); present {
				t.Errorf("%s accepted an interpolated dotenv value", tt.key)
			}
		})
	}
}

func TestProjectEnvPreservesLiteralAndExplicitEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BEADS_DOLT_SERVER_USER", "")
	if err := os.Unsetenv("BEADS_DOLT_SERVER_USER"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEADS_DOLT_SERVER_HOST", "")
	t.Setenv("BD_LITERAL_FIXTURE", "example")
	const contents = "BEADS_DOLT_SERVER_USER='literal$BD_LITERAL_FIXTURE'\nBEADS_DOLT_SERVER_HOST=from-file\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	loadBeadsEnvFile(dir)
	if got := os.Getenv("BEADS_DOLT_SERVER_USER"); got != "literal$BD_LITERAL_FIXTURE" {
		t.Errorf("literal value = %q", got)
	}
	if got, present := os.LookupEnv("BEADS_DOLT_SERVER_HOST"); got != "" || !present {
		t.Errorf("operator's explicit empty value = (%q, %v)", got, present)
	}
}

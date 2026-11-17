package dolt

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
)

func TestResolvedConfigSocketPrecedence(t *testing.T) {
	// Socket precedence: caller, then BEADS_DOLT_SERVER_SOCKET, then
	// metadata.json. Each case pins the env var and clears the credential
	// command, which runs in server mode and could fail the resolve first.
	socketFileCfg := func(t *testing.T, socket string) *configfile.Config {
		t.Helper()
		t.Setenv("BEADS_DOLT_CREDENTIAL_COMMAND", "")
		return &configfile.Config{
			Backend:          configfile.BackendDolt,
			DoltMode:         configfile.DoltModeServer,
			DoltDatabase:     "beads_codex",
			DoltServerSocket: socket,
		}
	}

	t.Run("applies socket from metadata", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "")
		cfg := &Config{}

		if err := applyResolvedConfig(context.Background(), t.TempDir(), socketFileCfg(t, "/metadata/dolt.sock"), cfg); err != nil {
			t.Fatalf("applyResolvedConfig: %v", err)
		}

		if cfg.ServerSocket != "/metadata/dolt.sock" {
			t.Fatalf("ServerSocket = %q, want the metadata socket %q", cfg.ServerSocket, "/metadata/dolt.sock")
		}
	})

	t.Run("caller socket wins over env and metadata", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "/env/dolt.sock")
		cfg := &Config{ServerSocket: "/caller/dolt.sock"}

		if err := applyResolvedConfig(context.Background(), t.TempDir(), socketFileCfg(t, "/metadata/dolt.sock"), cfg); err != nil {
			t.Fatalf("applyResolvedConfig: %v", err)
		}

		if cfg.ServerSocket != "/caller/dolt.sock" {
			t.Fatalf("ServerSocket = %q, want the caller's socket %q", cfg.ServerSocket, "/caller/dolt.sock")
		}
	})

	t.Run("env socket wins over metadata", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "/env/dolt.sock")
		cfg := &Config{}

		if err := applyResolvedConfig(context.Background(), t.TempDir(), socketFileCfg(t, "/metadata/dolt.sock"), cfg); err != nil {
			t.Fatalf("applyResolvedConfig: %v", err)
		}

		if cfg.ServerSocket != "/env/dolt.sock" {
			t.Fatalf("ServerSocket = %q, want the env socket %q", cfg.ServerSocket, "/env/dolt.sock")
		}
	})

	t.Run("no socket anywhere leaves TCP config untouched", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_SOCKET", "")
		t.Setenv("BEADS_DOLT_SERVER_HOST", "")
		t.Setenv("BEADS_DOLT_SERVER_PORT", "")
		t.Setenv("BEADS_DOLT_PORT", "")
		beadsDir := t.TempDir()
		fileCfg := socketFileCfg(t, "")
		cfg := &Config{}

		if err := applyResolvedConfig(context.Background(), beadsDir, fileCfg, cfg); err != nil {
			t.Fatalf("applyResolvedConfig: %v", err)
		}

		if cfg.ServerSocket != "" {
			t.Fatalf("ServerSocket = %q, want empty (TCP)", cfg.ServerSocket)
		}
		if cfg.ServerHost != fileCfg.GetDoltServerHost() {
			t.Fatalf("ServerHost = %q, want %q", cfg.ServerHost, fileCfg.GetDoltServerHost())
		}
		if want := doltserver.DefaultConfig(beadsDir).Port; cfg.ServerPort != want {
			t.Fatalf("ServerPort = %d, want %d", cfg.ServerPort, want)
		}
	})
}

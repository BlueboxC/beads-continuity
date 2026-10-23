package dolt

import (
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/backendnames"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestConfigConstructorsRefuseNonDoltBackendInLegacyConfig pins that every
// metadata-driven constructor refuses a legacy config.json naming a backend it
// cannot open as Dolt (removed, unrecognized, or another binary's registered
// backend) before configfile.Load migrates that file into metadata.json,
// dropping the keys Config does not know. The workspace holds a Dolt database,
// so a removed backend's refusal offers the metadata heal; it must not have
// rewritten the metadata first, and it names config.json, the file that holds
// the backend, rather than a metadata.json that does not exist.
func TestConfigConstructorsRefuseNonDoltBackendInLegacyConfig(t *testing.T) {
	// If the refusal were missing, the open must fail fast rather than reach
	// the suite's shared test server.
	t.Setenv("BEADS_DOLT_SERVER_HOST", "127.0.0.1")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "1")
	const registered = "legacy-config-registered-fixture"
	backendnames.Add(registered)
	t.Cleanup(func() { backendnames.Remove(registered) })
	for _, backend := range []string{configfile.BackendSQLite, configfile.BackendPostgres, configfile.BackendMySQL, "mystery", registered} {
		for _, doltMode := range []string{configfile.DoltModeEmbedded, configfile.DoltModeServer} {
			t.Run(backend+"/"+doltMode, func(t *testing.T) {
				beadsDir := t.TempDir()
				if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", ".dolt"), 0o750); err != nil {
					t.Fatal(err)
				}
				legacy := `{"backend":"` + backend + `","dolt_mode":"` + doltMode + `","dolt_database":"beads_legacy","future_binding":"keep-me"}`
				legacyPath := filepath.Join(beadsDir, "config.json")
				if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
					t.Fatal(err)
				}
				constructors := map[string]func() (*DoltStore, error){
					"default": func() (*DoltStore, error) {
						return NewFromConfig(t.Context(), beadsDir)
					},
					"standard": func() (*DoltStore, error) {
						return NewFromConfigWithOptions(t.Context(), beadsDir, &Config{DisableAutoStart: true, CreateIfMissing: true})
					},
					"cli": func() (*DoltStore, error) {
						return NewFromConfigWithCLIOptions(t.Context(), beadsDir, &Config{DisableAutoStart: true, ReadOnly: true})
					},
				}
				for name, construct := range constructors {
					t.Run(name, func(t *testing.T) {
						store, err := construct()
						if err == nil {
							_ = store.Close()
							t.Fatalf("backend %q opened as Dolt", backend)
						}
						if !strings.Contains(err.Error(), "cannot be opened as Dolt") || !strings.Contains(err.Error(), strconv.Quote(backend)) {
							t.Fatalf("constructor error = %v, want the refusal of backend %q", err, backend)
						}
						// The registered backend's refusal names no file.
						if backend != registered && (!strings.Contains(err.Error(), "config.json") || strings.Contains(err.Error(), "metadata.json")) {
							t.Fatalf("constructor error = %v, want it to name config.json and not metadata.json", err)
						}
						entries, readErr := os.ReadDir(beadsDir)
						names := make([]string, 0, len(entries))
						for _, entry := range entries {
							names = append(names, entry.Name())
						}
						if readErr != nil || !slices.Equal(names, []string{"config.json", "dolt"}) {
							t.Fatalf("refused open changed the workspace: %v, %v", names, readErr)
						}
						if got, readErr := os.ReadFile(legacyPath); readErr != nil || string(got) != legacy {
							t.Fatalf("refused open rewrote config.json: %q, %v", got, readErr)
						}
					})
				}
			})
		}
	}
}

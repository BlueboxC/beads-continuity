package beads_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/steveyegge/beads"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var legacyPublicOpens = map[string]func(context.Context, string) (beads.Storage, error){
	"OpenBestAvailable": beads.OpenBestAvailable,
	"OpenFromConfig":    beads.OpenFromConfig,
	"OpenGated":         func(ctx context.Context, p string) (beads.Storage, error) { return beads.OpenGated(ctx, p, 0) },
}

func legacyWorkspaceDigest(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data := []byte{}
		if !entry.IsDir() {
			data, err = os.ReadFile(p)
			if err != nil {
				return err
			}
		}
		out[rel] = fmt.Sprintf("%v:%x", info.Mode(), sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestPublicOpensRefuseUnsupportedBackendBeforeWriting pins that every public
// open refuses metadata naming a removed or unrecognized backend before it
// writes anything, whichever file holds it. For a legacy config.json,
// configfile.Load would migrate that file into metadata.json, dropping the
// keys Config does not know; for either file, OpenGated's gates create files
// in and beside .beads. The workspace holds a Dolt database, so the refusal
// offers the metadata heal, and it must name the file that actually holds the
// backend rather than one that does not exist.
func TestPublicOpensRefuseUnsupportedBackendBeforeWriting(t *testing.T) {
	// If the refusal were missing, a server-mode open must fail fast rather
	// than reach TestMain's shared test server.
	t.Setenv("BEADS_DOLT_SERVER_HOST", "127.0.0.1")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "1")
	layouts := []struct{ name, file, other string }{
		{"legacy", "config.json", "metadata.json"},
		{"metadata", "metadata.json", "config.json"},
	}
	for _, layout := range layouts {
		for _, backend := range []string{"mystery", "postgres", "mysql", "sqlite"} {
			for _, doltMode := range []string{"embedded", "server"} {
				for name, open := range legacyPublicOpens {
					t.Run(layout.name+"/"+backend+"/"+doltMode+"/"+name, func(t *testing.T) {
						root := t.TempDir()
						beadsDir := filepath.Join(root, ".beads")
						if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", ".dolt"), 0o750); err != nil {
							t.Fatal(err)
						}
						metadata := `{"backend":"` + backend + `","dolt_mode":"` + doltMode + `","dolt_database":"beads_legacy","future_binding":"keep-me"}`
						if err := os.WriteFile(filepath.Join(beadsDir, layout.file), []byte(metadata), 0o600); err != nil {
							t.Fatal(err)
						}
						before := legacyWorkspaceDigest(t, root)

						store, err := open(t.Context(), beadsDir)
						if err == nil {
							_ = store.Close()
							t.Fatalf("%s opened a workspace whose %s names backend %q", name, layout.file, backend)
						}
						// The tree first: a regression that migrates config.json
						// must report the rewrite, not just the file it names.
						if after := legacyWorkspaceDigest(t, root); !maps.Equal(after, before) {
							t.Fatalf("%s changed the workspace: before %v, after %v", name, before, after)
						}
						if store != nil {
							t.Fatalf("%s returned a non-nil %T with its refusal", name, store)
						}
						if !strings.Contains(err.Error(), strconv.Quote(backend)) || !strings.Contains(err.Error(), "no storage database was opened or modified") {
							t.Fatalf("%s error = %v, want the refusal of backend %q with the data-safety guarantee", name, err, backend)
						}
						if !strings.Contains(err.Error(), layout.file) || strings.Contains(err.Error(), layout.other) {
							t.Fatalf("%s error = %v, want it to name %s, the file holding the backend, and not %s", name, err, layout.file, layout.other)
						}
					})
				}
			}
		}
	}
}

// TestDoltOnlyOpensRefuseRegisteredBackendBeforeWriting pins that the public
// opens that only open Dolt (OpenFromConfig and OpenGated) refuse a
// registered non-Dolt backend before they write anything, whichever file
// names it: Load would migrate a legacy config.json, and OpenGated's gates
// create files in and beside .beads. OpenBestAvailable* dispatch such a
// backend to the registry instead
// (TestOpenBestAvailableWithDispatchesRegisteredBackendAndPassesOptions).
func TestDoltOnlyOpensRefuseRegisteredBackendBeforeWriting(t *testing.T) {
	t.Setenv("BEADS_DOLT_SERVER_HOST", "127.0.0.1")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "1")
	const backend = "fake-registered-dolt-only"
	registerWorkspaceBackend(t, backend)
	opens := map[string]func(context.Context, string) (beads.Storage, error){
		"OpenFromConfig": legacyPublicOpens["OpenFromConfig"],
		"OpenGated":      legacyPublicOpens["OpenGated"],
	}
	for _, file := range []string{"config.json", "metadata.json"} {
		for name, open := range opens {
			t.Run(file+"/"+name, func(t *testing.T) {
				root := t.TempDir()
				beadsDir := filepath.Join(root, ".beads")
				if err := os.MkdirAll(filepath.Join(beadsDir, "dolt", ".dolt"), 0o750); err != nil {
					t.Fatal(err)
				}
				metadata := `{"backend":"` + backend + `","dolt_mode":"server","dolt_database":"beads_legacy","future_binding":"keep-me"}`
				if err := os.WriteFile(filepath.Join(beadsDir, file), []byte(metadata), 0o600); err != nil {
					t.Fatal(err)
				}
				before := legacyWorkspaceDigest(t, root)

				store, err := open(t.Context(), beadsDir)
				if err == nil {
					_ = store.Close()
					t.Fatalf("%s opened a workspace whose %s names registered backend %q", name, file, backend)
				}
				if after := legacyWorkspaceDigest(t, root); !maps.Equal(after, before) {
					t.Fatalf("%s changed the workspace: before %v, after %v", name, before, after)
				}
				if store != nil {
					t.Fatalf("%s returned a non-nil %T with its refusal", name, store)
				}
				if want := strconv.Quote(backend) + " cannot be opened as Dolt"; !strings.Contains(err.Error(), want) {
					t.Fatalf("%s error = %v, want %q", name, err, want)
				}
			})
		}
	}
}

// TestPublicOpensAdmitSupportedBackendInLegacyConfig pins the admitted side
// of the pre-Load backend refusal: a legacy config.json naming Dolt, or no
// backend, passes it, is migrated into metadata.json as before, and the open
// proceeds to the backend's own unreachable-server error.
func TestPublicOpensAdmitSupportedBackendInLegacyConfig(t *testing.T) {
	t.Setenv("BEADS_DOLT_SERVER_HOST", "127.0.0.1")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "1")
	for _, backend := range []string{"dolt", ""} {
		for name, open := range legacyPublicOpens {
			t.Run("backend="+backend+"/"+name, func(t *testing.T) {
				beadsDir := filepath.Join(t.TempDir(), ".beads")
				if err := os.MkdirAll(beadsDir, 0o750); err != nil {
					t.Fatal(err)
				}
				legacy := `{"backend":"` + backend + `","dolt_mode":"server"}`
				if err := os.WriteFile(filepath.Join(beadsDir, "config.json"), []byte(legacy), 0o600); err != nil {
					t.Fatal(err)
				}
				store, err := open(t.Context(), beadsDir)
				if err == nil {
					_ = store.Close()
					t.Fatalf("%s opened a store on an unreachable server", name)
				}
				if !strings.Contains(err.Error(), "unreachable") {
					t.Fatalf("%s error = %v, want the backend's unreachable-server error", name, err)
				}
				if _, statErr := os.Stat(filepath.Join(beadsDir, "metadata.json")); statErr != nil {
					t.Fatalf("%s did not migrate the admitted legacy config.json: %v", name, statErr)
				}
			})
		}
	}
}

package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

// The refused read must not migrate a foreign workspace's metadata (PR #7765).
func TestUnknownBackendDiscoveryPreservesLegacyMetadata(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_HOST", "127.0.0.1")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "1")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dolt"), 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"backend":"future-backend","database":"beads.db","future_binding":"keep"}`)
	name := filepath.Join(dir, "config.json")
	if err := os.WriteFile(name, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTypesFromDB(dir); err == nil {
		t.Fatal("unknown backend was not refused")
	}
	got, err := os.ReadFile(name)
	if err != nil || string(got) != string(original) {
		t.Fatalf("foreign metadata changed: %s, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "metadata.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata published on refusal: %v", err)
	}
}

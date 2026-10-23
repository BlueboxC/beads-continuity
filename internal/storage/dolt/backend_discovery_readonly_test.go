package dolt

import (
	"github.com/steveyegge/beads/internal/configfile"
	"os"
	"path/filepath"
	"testing"
)

func TestGetBackendFromConfigPreservesLegacyMetadata(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, configfile.LegacyConfigFileName)
	data := []byte(`{"backend":"postgres","operator_extension":{"keep":true}}`)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := GetBackendFromConfig(d); got != "postgres" {
		t.Fatalf("backend %q", got)
	}
	after, err := os.ReadFile(p)
	if err != nil || string(after) != string(data) {
		t.Fatalf("legacy metadata changed: %s %v", after, err)
	}
	if _, err := os.Stat(configfile.ConfigPath(d)); !os.IsNotExist(err) {
		t.Fatalf("discovery created metadata.json: %v", err)
	}
}

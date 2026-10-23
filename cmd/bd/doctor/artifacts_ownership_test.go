package doctor

import (
	"path/filepath"
	"testing"
)

func TestArtifactScanDoesNotOfferStorageAsRedirectCruft(t *testing.T) {
	for _, files := range []map[string]string{
		{"metadata.json": `{"database":"beads.db"}`, "beads.db": "SQLite format 3\x00", "beads.db-wal": "uncheckpointed"},
		{"beads.db": "SQLite format 3\x00", "beads.db-wal": "uncheckpointed"},
		{"metadata.json": `{"backend":"dolt"}`, "embeddeddolt/project/.dolt/noms/manifest": "database"},
	} {
		dir := filepath.Join(t.TempDir(), "crew", "alice", ".beads")
		writeArtifactTree(t, dir, files)
		report, err := ScanForArtifacts(filepath.Dir(filepath.Dir(filepath.Dir(dir))))
		if err != nil {
			t.Fatal(err)
		}
		if report.SafeDeleteCount != 0 {
			t.Fatalf("offered storage for deletion: %+v", report)
		}
	}
}

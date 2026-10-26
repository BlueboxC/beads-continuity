package fix

import (
	"io"
	"path/filepath"
	"reflect"
	"testing"
)

func TestClassicArtifactsPreservesStorageInRedirectLocations(t *testing.T) {
	for _, layout := range []string{"crew/alice", "polecats/worker", "refinery/rig", ".git/beads-worktrees/topic"} {
		for _, fixture := range []struct {
			name  string
			files map[string]string
		}{
			{"classic", map[string]string{"metadata.json": `{"database":"beads.db"}`, "beads.db": "SQLite format 3\x00", "beads.db-wal": "uncheckpointed"}},
			{"without metadata", map[string]string{"beads.db": "SQLite format 3\x00", "beads.db-wal": "uncheckpointed", "beads.backup-a.db": "backup"}},
			{"mixed explicit dolt", map[string]string{"metadata.json": `{"backend":"dolt"}`, "beads.db": "retained database", "beads.db-wal": "retained WAL"}},
			{"local dolt", map[string]string{"metadata.json": `{"backend":"dolt"}`, "dolt/project/.dolt/noms/manifest": "database"}},
			{"embedded dolt", map[string]string{"metadata.json": `{"backend":"dolt"}`, "embeddeddolt/project/.dolt/noms/manifest": "database"}},
			{"foreign backend", map[string]string{"metadata.json": `{"backend":"postgres"}`, "foreign.data": "foreign state"}},
		} {
			t.Run(layout+"/"+fixture.name, func(t *testing.T) {
				root := t.TempDir()
				dir := filepath.Join(root, filepath.FromSlash(layout), ".beads")
				fixture.files["extra.txt"] = "project-owned"
				writeBeadsTree(t, dir, fixture.files)
				before := snapshotBeadsTree(t, dir)
				if err := ClassicArtifacts(root, io.Discard); err != nil {
					t.Fatal(err)
				}
				if got := snapshotBeadsTree(t, dir); !reflect.DeepEqual(got, before) {
					t.Fatalf("storage workspace changed: got %q, want %q", got, before)
				}
			})
		}
	}
}

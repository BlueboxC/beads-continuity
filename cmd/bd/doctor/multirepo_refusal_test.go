package doctor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
)

// Adapted from Julian Knutsen's #7765: retain YAML types, but name every refused
// database using this fork's passive Graph/future/backend classifier.
func TestMultiRepoTypesReportsRefusedDiscovery(t *testing.T) {
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_TEST_IGNORE_REPO_CONFIG", "1")
	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)
	if err := config.Initialize(); err != nil {
		t.Fatal(err)
	}
	var repos []string
	for _, meta := range []string{`{"backend":"future-backend"}`, `{"backend":"dolt","graph_mode":"link"}`, `{"backend":"dolt","graph_mode":"future"}`} {
		repo := t.TempDir()
		repos = append(repos, repo)
		dir := filepath.Join(repo, ".beads")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(meta), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("types:\n  custom: [local-type]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		got, refused := discoverChildTypes(repo)
		if !reflect.DeepEqual(got, []string{"local-type"}) || refused == nil {
			t.Fatalf("types %v, refusal %v", got, refused)
		}
	}
	config.Set("repos.primary", repos[0])
	config.Set("repos.additional", repos)
	check := CheckMultiRepoTypes(t.TempDir())
	if strings.Count(check.Detail, "database not read, so only config.yaml types are listed:") != len(repos) {
		t.Fatalf("incomplete detail: %s", check.Detail)
	}
	if check.Status != StatusOK {
		t.Fatalf("informational status changed: %s", check.Status)
	}
}

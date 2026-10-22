package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Adapted from julianknutsen's PR #7738; native discovery must not rewrite
// another workspace even when this fork supports Graph Preview separately.
func TestDiscoverChildTypesPreservesAdditionalMetadata(t *testing.T) {
	for _, layout := range []struct {
		name, metadataName, metadata, marker string
	}{
		{"legacy native", "config.json", `{"backend":"dolt","dolt_mode":"server","dolt_database":"legacy_types"}`, ""},
		{"legacy link", "config.json", `{"backend":"dolt","dolt_mode":"server","dolt_database":"graph_types","graph_mode":"link","graph_future_binding":"keep-me"}`, ""},
		{"legacy future", "config.json", `{"backend":"dolt","graph_mode":"future-format","graph_future_binding":"keep-me"}`, ""},
		{"legacy marker", "config.json", `{"backend":"dolt","graph_future_binding":"keep-me"}`, "link-preview-v9\n"},
		{"current link", "metadata.json", `{"backend":"dolt","graph_mode":"link","graph_future_binding":"keep-me"}`, ""},
		{"current marker", "metadata.json", `{"backend":"dolt","graph_future_binding":"keep-me"}`, "link-preview-v9\n"},
	} {
		for _, root := range []bool{false, true} {
			if layout.name == "legacy native" && root {
				continue
			}
			t.Run(fmt.Sprintf("%s/root=%v", layout.name, root), func(t *testing.T) {
				repo := t.TempDir()
				dir := filepath.Join(repo, ".beads")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if root {
					if err := os.Mkdir(filepath.Join(dir, "dolt"), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				files := map[string]string{layout.metadataName: layout.metadata, "config.yaml": "types:\n  custom: [local-type]\n"}
				if layout.marker != "" {
					files["graph-preview-format"] = layout.marker
				}
				for name, data := range files {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if got, _ := discoverChildTypes(repo); !reflect.DeepEqual(got, []string{"local-type"}) {
					t.Fatalf("types = %v; want YAML fallback", got)
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				extra := 0
				if root {
					extra = 1
				}
				if len(entries) != len(files)+extra {
					t.Errorf("workspace entry count = %d; want %d", len(entries), len(files))
				}
				for name, expected := range files {
					got, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil || string(got) != expected {
						t.Errorf("%s changed: %q, %v", name, got, err)
					}
				}
			})
		}
	}
}

package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForkClaudePluginHonorsDisableAndExactName(t *testing.T) {
	for _, tc := range []struct{ name, user, project string }{{"unrelated", "{\"enabledPlugins\":{\"other-beads@market\":true}}", "{}"}, {"disabled here", "{\"enabledPlugins\":{\"beads@beads-marketplace\":true}}", "{\"enabledPlugins\":{\"beads@beads-marketplace\":false}}"}} {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			for p, data := range map[string]string{filepath.Join(home, ".claude", "settings.json"): tc.user, filepath.Join(repo, ".claude", "settings.local.json"): tc.project} {
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if isBeadsPluginInstalled(repo) {
				t.Fatal("doctor reports a disabled or unrelated plugin as enabled")
			}
		})
	}
}

package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaudeLocalDisableOverridesUserPlugin(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	for path, data := range map[string]string{globalSettingsPath(home): `{"enabledPlugins":{"beads@market":true}}`, legacyProjectSettingsPath(project): `{"enabledPlugins":{"beads@market":false}}`} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := claudeEnv{homeDir: home, projectDir: project, readFile: os.ReadFile}
	if hasBeadsPlugin(env, false) {
		t.Fatal("local false ignored; project would have no context hook")
	}
	if !hasBeadsPlugin(env, true) {
		t.Fatal("project override leaked to global scope")
	}
}

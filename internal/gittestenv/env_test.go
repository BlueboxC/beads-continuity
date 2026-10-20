package gittestenv

import (
	"os"
	"strings"
	"testing"
)

func TestClearInheritedGitEnvironment(t *testing.T) {
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_TEMPLATE_DIR"} {
		t.Setenv(name, "outer-repository")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "outer-global-config")
	t.Setenv("GIT_CONFIG_SYSTEM", "outer-system-config")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "0")
	t.Setenv("BEADS_FIXTURE_CONTROL", "preserved")
	Clear()
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		want := os.DevNull
		switch name {
		case "GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM":
		case "GIT_CONFIG_NOSYSTEM":
			want = "1"
		default:
			t.Errorf("inherited %s remains", name)
		}
		if value != want {
			t.Errorf("%s = %q, want %q", name, value, want)
		}
	}
	if os.Getenv("BEADS_FIXTURE_CONTROL") != "preserved" {
		t.Fatal("unrelated environment changed")
	}
}

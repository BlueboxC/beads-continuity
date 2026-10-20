package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsetRefusesCollateralChanges(t *testing.T) {
	cases := []struct{ name, body, key string }{
		{"nested flow", "sync:\n  remote: {url: a,\n    branch: b}\nnode_id: mini\n", "sync.remote"},
		{"flat flow", "sync.remote: {url: a,\n  branch: b}\nnode_id: mini\n", "sync.remote"},
		{"flow sequence", "types.custom: [step,\n  slot]\nnode_id: mini\n", "types.custom"},
		{"quoted", "notes: \"line one\n  line two\"\nnode_id: mini\n", "notes"},
		{"single quoted", "dolt.host: 'a\n  b'\nnode_id: mini\n", "dolt.host"},
		{"block scalar", "notes: |\n  line one\n  line two\nnode_id: mini\n", "notes"},
		{"parent mapping", "sync:\n  remote: a\nnode_id: mini\n", "sync"},
		{"flow parent", "sync: {remote: a}\nnode_id: mini\n", "sync.remote"},
		{"flow root", "{sync.remote: a, node_id: mini}\n", "sync.remote"},
		{"quoted collateral", "remote: a\nnotes: \"start\nremote: keep\"\n", "remote"},
		{"anchor still used", "remote: &r a\nbackup: *r\n", "remote"},
		{"later document", "metrics: {disabled: true}\n---\nnode_id: \"mini\n  box\"\n", "node_id"},
		{"CRLF", "sync:\r\n  remote: {url: a,\r\n    branch: b}\r\nnode_id: mini\r\n", "sync.remote"},
	}
	for _, mode := range []string{"project", "user"} {
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				path, unset := unsetIntegrityFixture(t, mode, tc.body)
				if err := unset(tc.key); err == nil {
					t.Error("unsafe unset succeeded")
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != tc.body {
					t.Errorf("refusal changed the file: %q, %v", got, err)
				}
			})
		}
	}
}

func TestUnsetPreservesSupportedValuesAndNoOps(t *testing.T) {
	cases := []struct{ name, body, key, want string }{
		{"simple", "remote: a\nnode_id: mini\n", "remote", "# remote: a\nnode_id: mini\n"},
		{"single line flow leaf", "sync:\n  remote: {url: a, branch: b}\nnode_id: mini\n", "sync.remote", "sync:\n  # remote: {url: a, branch: b}\nnode_id: mini\n"},
		{"indented document", "  sync:\n    remote: a\n  node_id: mini\n", "sync.remote", "  sync:\n    # remote: a\n  node_id: mini\n"},
		{"alias", "remote: &r a\nbackup:\n  remote: *r\n", "backup.remote", "remote: &r a\nbackup:\n  # remote: *r\n"},
		{"absent quoted key", "notes: \"start\nremote: keep\"\n", "remote", "notes: \"start\nremote: keep\"\n"},
		{"absent CRLF", "node_id: mini\r\n", "remote", "node_id: mini\r\n"},
		{"no final newline", "remote: a", "remote", "# remote: a"},
		{"blank final lines", "remote: a\n\n\n", "remote", "# remote: a\n\n\n"},
		{"long line", "other: " + strings.Repeat("x", 70000) + "\nremote: a\n", "remote", "other: " + strings.Repeat("x", 70000) + "\n# remote: a\n"},
	}
	for _, mode := range []string{"project", "user"} {
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				path, unset := unsetIntegrityFixture(t, mode, tc.body)
				if err := unset(tc.key); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != tc.want {
					t.Errorf("unexpected output: got %d bytes, want %d: %v", len(got), len(tc.want), err)
				}
			})
		}
	}
}

func unsetIntegrityFixture(t *testing.T, mode, body string) (string, func(string) error) {
	t.Helper()
	root := t.TempDir()
	var path string
	var unset func(string) error
	if mode == "project" {
		t.Setenv("BEADS_DIR", root)
		path, unset = filepath.Join(root, "config.yaml"), UnsetYamlConfig
	} else {
		t.Setenv("HOME", root)
		t.Setenv("XDG_CONFIG_HOME", root)
		t.Setenv("APPDATA", root)
		var err error
		path, err = UserConfigYamlPath()
		if err != nil {
			t.Fatal(err)
		}
		unset = UnsetUserYamlConfig
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path, unset
}

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceYamlSettingWithLocalReadsNullAsUnset(t *testing.T) {
	for _, tc := range []struct {
		name        string
		files       map[string]string
		wantValue   string
		wantPresent bool
		wantErr     bool
	}{
		{name: "commented-out section", files: map[string]string{"config.yaml": "dolt:\n  # mode: server\n"}},
		{name: "null value", files: map[string]string{"config.yaml": "dolt:\n  mode: null\n"}},
		{name: "null dotted value", files: map[string]string{"config.yaml": "dolt.mode: ~\n"}},
		{name: "null local section falls through", files: map[string]string{"config.yaml": "dolt:\n  mode: server\n", "config.local.yaml": "dolt:\n"}, wantValue: "server", wantPresent: true},
		{name: "set value", files: map[string]string{"config.yaml": "dolt:\n  mode: server\n"}, wantValue: "server", wantPresent: true},
		{name: "scalar parent", files: map[string]string{"config.yaml": "dolt: false\n"}, wantErr: true},
		{name: "list value", files: map[string]string{"config.yaml": "dolt:\n  mode: []\n"}, wantErr: true},
		{name: "unparseable", files: map[string]string{"config.yaml": "dolt: [unterminated\n"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.MkdirAll(beadsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(beadsDir, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			value, present, err := WorkspaceYamlSettingWithLocal(beadsDir, "dolt.mode")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && (value != tc.wantValue || present != tc.wantPresent) {
				t.Fatalf("= (%q, %v), want (%q, %v)", value, present, tc.wantValue, tc.wantPresent)
			}
		})
	}
}

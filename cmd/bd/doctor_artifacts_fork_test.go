package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForkDoctorArtifactCleanupJSONIsOneDocument(t *testing.T) {
	old := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = old })
	root := t.TempDir()
	dir := filepath.Join(root, "crew", "alice", ".beads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"README.md": "keep edits", "daemon.pid": "1"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := captureStdout(t, func() error { return runArtifactsCheck(root, true, true) })
	d := json.NewDecoder(strings.NewReader(out))
	var result map[string]any
	if err := d.Decode(&result); err != nil {
		t.Fatalf("invalid stdout: %q: %v", out, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		t.Fatalf("extra stdout after JSON: %q: %v", out, err)
	}
	if body, err := os.ReadFile(filepath.Join(dir, "README.md")); err != nil || string(body) != "keep edits" {
		t.Fatalf("project data changed: %q, %v", body, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "daemon.pid")); !os.IsNotExist(err) {
		t.Fatalf("runtime leftover not removed: %v", err)
	}
	if result["safe_delete_count"] != float64(0) {
		t.Fatalf("retained project material still marked safe: %+v", result)
	}
}

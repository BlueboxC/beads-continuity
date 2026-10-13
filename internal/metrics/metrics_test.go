package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUsageMetricsCannotCollectOrFlush(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BD_DISABLE_METRICS", "0")
	t.Setenv("BD_DISABLE_EVENT_FLUSH", "0")
	t.Setenv("BEADS_METRICS_ENDPOINT", "https://example.invalid/collect")
	dir := filepath.Join(home, ".beads", "eventsData")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	queued := filepath.Join(dir, "retained.evtq")
	if err := os.WriteFile(queued, []byte("retained upstream queue"), 0600); err != nil {
		t.Fatal(err)
	}
	if NewCommandEvent("create") != nil || Global() != nil {
		t.Fatal("usage collector is active")
	}
	CloseAndFlush()
	if RunSendMetrics() != 0 {
		t.Fatal("legacy uploader command failed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("queue changed: %v %v", entries, err)
	}
	b, err := os.ReadFile(queued)
	if err != nil || string(b) != "retained upstream queue" {
		t.Fatalf("retained queue changed: %q %v", b, err)
	}
}

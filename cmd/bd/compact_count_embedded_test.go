//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/types"
)

func TestEmbeddedCompactReportsActualHistoryCount(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "count")
	one := bdCreate(t, bd, dir, "retained one", "--type", "task")
	two := bdCreate(t, bd, dir, "retained two", "--type", "task")
	out := bdCompact(t, bd, dir, "--force", "--days", "0", "--json")
	var result struct {
		Success bool `json:"success"`
		Before  int  `json:"commits_before"`
		After   int  `json:"commits_after"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.Before < 3 {
		t.Fatalf("did not compact fixture: %s", out)
	}
	s, err := embeddeddolt.Open(context.Background(), beadsDir, "count", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log, err := s.Log(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) != result.After {
		t.Fatalf("reported %d commits, actual history has %d; output: %s", result.After, len(log), out)
	}
	for _, issue := range []*types.Issue{one, two} {
		got, err := s.GetIssue(context.Background(), issue.ID)
		if err != nil || got == nil || got.Title != issue.Title {
			t.Fatalf("compaction lost %s: %v", issue.ID, err)
		}
	}
	if result.After < 2 {
		t.Fatal("initial history commit must remain represented")
	}
}

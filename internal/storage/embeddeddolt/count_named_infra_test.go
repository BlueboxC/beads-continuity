//go:build cgo

package embeddeddolt_test

import (
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
	"testing"
)

func TestCounterNamedInfraReadsWorkspaceVocabulary(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "nct")
	ctx := t.Context()
	kit := newEmbeddedRoleFixtureKit(te, "nct")
	counter, err := te.store.Counter()
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][2]string{{"types.custom", "robot"}, {"types.infra", "robot"}} {
		if err := kit.SetConfig(ctx, kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := kit.CreateWisp(ctx, &types.Issue{ID: "nct-robot", Title: "Robot fixture", Status: types.StatusOpen, Priority: 2, IssueType: "robot", Ephemeral: true}, "tester"); err != nil {
		t.Fatal(err)
	}
	// The accessor predates the configuration change: each request must reload it.
	result, err := counter.Count(ctx, issueops.CountRequest{IssueType: "robot"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("count --type robot = %d, want 1", result.Total)
	}
	bare, err := counter.Count(ctx, issueops.CountRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if bare.Total != 0 {
		t.Fatalf("bare count = %d, want durable-only 0", bare.Total)
	}
}

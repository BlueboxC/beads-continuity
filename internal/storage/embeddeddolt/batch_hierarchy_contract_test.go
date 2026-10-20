//go:build cgo

package embeddeddolt_test

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestCreateBatchHierarchySnapshot(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	for _, invalid := range []bool{false, true} {
		name := "siblings-and-wisp"
		if invalid {
			name = "planned-ancestor-rollback"
		}
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t, "bh")
			ctx := t.Context()
			node := func(id string) *types.Issue {
				return &types.Issue{ID: id, Title: id, Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
			}
			a, b, w, p, g := node("bh-a"), node("bh-b"), node("bh-w"), node("bh-parent"), node("bh-grand")
			w.Ephemeral = true
			a.Dependencies = []*types.Dependency{{DependsOnID: b.ID, Type: types.DepBlocks}, {DependsOnID: w.ID, Type: types.DepConditionalBlocks}, {DependsOnID: p.ID, Type: types.DepParentChild}}
			if invalid {
				a.Dependencies[0].DependsOnID = g.ID
			}
			for _, n := range []*types.Issue{b, w} {
				n.Dependencies = []*types.Dependency{{DependsOnID: p.ID, Type: types.DepParentChild}}
			}
			p.Dependencies = []*types.Dependency{{DependsOnID: g.ID, Type: types.DepParentChild}}
			err := te.store.CreateIssues(ctx, []*types.Issue{a, b, w, p, g}, "tester")
			if invalid {
				if err == nil || !strings.Contains(err.Error(), "ancestor") {
					t.Fatalf("want planned ancestor refusal, got %v", err)
				}
				if _, err = te.store.GetIssue(ctx, a.ID); err == nil {
					t.Fatal("rejected batch left an issue behind")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				for _, n := range []*types.Issue{a, b, w, p, g} {
					if _, err := te.store.GetIssue(ctx, n.ID); err != nil {
						t.Fatalf("%s: %v", n.ID, err)
					}
				}
			}
		})
	}
}

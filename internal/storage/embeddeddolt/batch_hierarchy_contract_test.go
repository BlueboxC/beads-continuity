//go:build cgo

package embeddeddolt_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
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
			a, b, p, g := node("bh-a"), node("bh-b"), node("bh-parent"), node("bh-grand")
			w, wSibling, wParent := node("bh-w"), node("bh-w-sibling"), node("bh-w-parent")
			for _, n := range []*types.Issue{w, wSibling, wParent} {
				n.Ephemeral = true
			}
			// A mixed batch may create both planes, with each edge staying in
			// its own plane. The hierarchy check sees both sets of parent edges.
			a.Dependencies = []*types.Dependency{
				{DependsOnID: b.ID, Type: types.DepBlocks},
				{DependsOnID: p.ID, Type: types.DepParentChild},
			}
			if invalid {
				a.Dependencies[0].DependsOnID = g.ID
			}
			b.Dependencies = []*types.Dependency{{DependsOnID: p.ID, Type: types.DepParentChild}}
			p.Dependencies = []*types.Dependency{{DependsOnID: g.ID, Type: types.DepParentChild}}
			w.Dependencies = []*types.Dependency{
				{DependsOnID: wSibling.ID, Type: types.DepConditionalBlocks},
				{DependsOnID: wParent.ID, Type: types.DepParentChild},
			}
			wSibling.Dependencies = []*types.Dependency{{DependsOnID: wParent.ID, Type: types.DepParentChild}}

			batch := []*types.Issue{a, b, p, g, w, wSibling, wParent}
			err := te.store.CreateIssues(ctx, batch, "tester")
			if invalid {
				var conflict *domain.DependencyHierarchyConflictError
				if !errors.As(err, &conflict) || !conflict.BlockerIsAncestor || conflict.IssueID != a.ID || conflict.BlockerID != g.ID {
					t.Fatalf("want planned ancestor refusal for %s -> %s, got %v", a.ID, g.ID, err)
				}
				for _, n := range batch {
					if _, err := te.store.GetIssue(ctx, n.ID); !errors.Is(err, storage.ErrNotFound) {
						t.Fatalf("rejected batch must leave %s absent, got %v", n.ID, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range batch {
				got, err := te.store.GetIssue(ctx, n.ID)
				if err != nil {
					t.Fatalf("%s: %v", n.ID, err)
				}
				if got.Ephemeral != n.Ephemeral {
					t.Fatalf("%s: ephemeral = %v, want %v", n.ID, got.Ephemeral, n.Ephemeral)
				}
				if len(n.Dependencies) == 0 {
					continue
				}
				deps, err := te.store.GetDependencyRecords(ctx, n.ID)
				if err != nil {
					t.Fatalf("read dependencies for %s: %v", n.ID, err)
				}
				for _, want := range n.Dependencies {
					if !slices.ContainsFunc(deps, func(got *types.Dependency) bool {
						return got.DependsOnID == want.DependsOnID && got.Type == want.Type
					}) {
						t.Fatalf("%s is missing %s edge to %s", n.ID, want.Type, want.DependsOnID)
					}
				}
			}
		})
	}
}

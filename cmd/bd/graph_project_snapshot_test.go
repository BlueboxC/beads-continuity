package main

import (
	"context"
	"errors"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/issueops"
	"testing"
)

type projectSnapshotProbe struct {
	storage.DoltStorage
	*indexedMemoryProbe
	calls   int
	failure error
}

func (p *projectSnapshotProbe) ReadProjectSnapshot(_ context.Context, fn func(storage.ProjectReader) error) error {
	p.calls++
	if err := fn(p); err != nil {
		return err
	}
	return p.failure
}
func (p *projectSnapshotProbe) SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error) {
	return []*types.Issue{{ID: "parent"}, {ID: "child"}}, nil
}
func (p *projectSnapshotProbe) GetDependencyRecordsForIssues(context.Context, []string) (map[string][]*types.Dependency, error) {
	return map[string][]*types.Dependency{"child": {{IssueID: "child", DependsOnID: "parent", Type: types.DepBlocks}}}, nil
}

func TestProjectSnapshotSurvivesReadDecoratorsAndWithholdsFailedResult(t *testing.T) {
	previous := store
	defer func() { store = previous }()
	for _, failure := range []error{nil, errors.New("snapshot cleanup failed")} {
		raw := &projectSnapshotProbe{indexedMemoryProbe: &indexedMemoryProbe{maintenanceMemories: maintenanceMemories{rows: map[string]string{"human": "keep solution"}}}, failure: failure}
		store = storage.NewHookFiringStore(raw, nil)
		result, err := readNativeProject(t.Context(), nil, issueops.ListRequest{})
		if raw.calls != 1 {
			t.Fatal("project snapshot capability lost through decorator")
		}
		if failure != nil {
			if !errors.Is(err, failure) || result.plane.Memories != nil || result.issues != nil || result.deps != nil {
				t.Fatal("failed read exposed partial state", err)
			}
		} else if err != nil || result.plane.Memories["human"] != "keep solution" || len(result.issues) != 2 || len(result.deps) != 1 {
			t.Fatal("snapshot lost a project plane", err)
		}
	}
}

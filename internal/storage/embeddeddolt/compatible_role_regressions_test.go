//go:build cgo

package embeddeddolt_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestWorkspaceConfigBoundsLengthsAtTheRole(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "bounds")
	role, err := te.store.WorkspaceConfig()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	keyAt := strings.Repeat("é", types.MaxFieldLen)
	valueAt := strings.Repeat("é", types.MaxTextBytes/2) + "!"
	if _, err := role.SetSetting(ctx, publicops.SetSettingRequest{Key: keyAt, Value: valueAt}); err != nil {
		t.Fatalf("boundary-length setting was refused: %v", err)
	}
	for _, request := range []publicops.SetSettingRequest{
		{Key: keyAt + "é", Value: "short"},
		{Key: keyAt, Value: valueAt + "é"},
	} {
		_, err := role.SetSetting(ctx, request)
		if !errors.Is(err, publicops.ErrValidation) || !errors.Is(err, types.ErrFieldTooLong) {
			t.Errorf("over-limit setting returned %v, want typed validation and field-length causes", err)
		}
	}
	got, err := role.GetSetting(ctx, publicops.GetSettingRequest{Key: keyAt})
	if err != nil || got.Value != valueAt {
		t.Errorf("refused write changed the accepted setting (value length=%d, err=%v)", len(got.Value), err)
	}
}

func TestCascadeDeletionPreviewMatchesDependentClosure(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "casc")
	ctx := t.Context()
	parent, child, dependent := "casc-parent", "casc-child", "casc-dependent"
	unrelated := "casc-unrelated"
	for _, id := range []string{parent, child, dependent, unrelated} {
		if err := te.store.CreateIssue(ctx, &types.Issue{
			ID: id, Title: id, IssueType: types.TypeTask, Status: types.StatusOpen, Priority: 2,
		}, "fixture"); err != nil {
			t.Fatal(err)
		}
	}
	for _, dep := range []types.Dependency{
		{IssueID: child, DependsOnID: parent, Type: types.DepParentChild},
		{IssueID: dependent, DependsOnID: child, Type: types.DepBlocks},
	} {
		if err := te.store.AddDependency(ctx, &dep, "fixture"); err != nil {
			t.Fatal(err)
		}
	}

	preview, err := te.store.DeleteIssues(ctx, []string{parent}, true, true, true)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.DeletedCount != 3 || len(preview.OrphanedIssues) != 0 {
		t.Errorf("preview must cover the entire closure without orphans, got %+v", preview)
	}
	for _, id := range []string{parent, child, dependent, unrelated} {
		te.assertRowExists(t, ctx, "issues", id)
	}

	result, err := te.store.DeleteIssues(ctx, []string{parent}, true, true, false)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if result.DeletedCount != 3 || len(result.OrphanedIssues) != 0 {
		t.Errorf("delete must cover the entire closure without orphans, got %+v", result)
	}
	for _, id := range []string{parent, child, dependent} {
		te.assertRowNotExists(t, ctx, "issues", id)
	}
	te.assertRowExists(t, ctx, "issues", unrelated)
}

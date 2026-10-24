//go:build cgo

package graphstore

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/types"
)

// The workspace format generation is checked against the Types a fresh init
// installs, so the list it is checked against must be the installer's own list
// and not a second copy that can drift from it.
func TestFreshTypeNamesFollowPreviewTypeDefinitions(t *testing.T) {
	definitions := previewTypeDefinitions()
	want := make([]string, len(definitions))
	for i, definition := range definitions {
		want[i] = definition.name
	}
	got := FreshTypeNames()
	if !slices.Equal(got, want) {
		t.Fatalf("FreshTypeNames() = %v, want the installer's own order %v", got, want)
	}
	// The slice is the caller's: changing it must not reach the installer.
	got[0] = "changed"
	if again := FreshTypeNames(); !slices.Equal(again, want) {
		t.Fatalf("changing the returned slice changed a later call: %v", again)
	}
}

// Direct Store callers may omit TypeGeneration. The checked installed set, not
// an implied older generation, decides whether relationship journeys are safe.
func TestUnspecifiedStoreGenerationRequiresCompleteFreshTypes(t *testing.T) {
	ctx, options := issueExperimentOptions(t, "embedded")
	if options.TypeGeneration != 0 {
		t.Fatalf("direct Store fixture should omit TypeGeneration, got %d", options.TypeGeneration)
	}
	store, err := OpenExisting(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	epicRequest := plainIssue("Release epic")
	epicRequest.Issue.IssueType = types.TypeEpic
	epic, err := store.CreateIssue(ctx, "beads/epic", epicRequest)
	if err != nil {
		t.Fatal(err)
	}
	request := EpicCloseRequest{Path: "beads/epic", ExpectedRevision: epic.Revision, Reason: "Done", Actor: "author"}
	if _, err := store.ListChildren(ctx, "beads/epic"); err != nil {
		t.Fatalf("fresh children rejected: %v", err)
	}
	if _, err := store.ListChildrenLegacy(ctx, "beads/epic"); err != nil {
		t.Fatalf("fresh counted children rejected: %v", err)
	}
	if _, err := store.EpicStatuses(ctx); err != nil {
		t.Fatalf("fresh epic progress rejected: %v", err)
	}
	if _, err := store.CloseEligibleEpic(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("empty epic should reach eligibility check, got %v", err)
	}
	var fingerprint string
	if err := store.db.QueryRowContext(ctx, `SELECT fingerprint FROM graph_preview_types WHERE name='dependency'`).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE graph_preview_types SET fingerprint='wrong' WHERE name='dependency'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EpicStatuses(ctx); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("altered fingerprint admitted: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE graph_preview_types SET fingerprint=? WHERE name='dependency'`, fingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM graph_preview_types WHERE name='dependency'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListChildren(ctx, "beads/epic"); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("partial installation admitted: %v", err)
	}
}

// Common public identity changes canonical snapshot semantics and requires
// SchemaVersion 11. Older workspaces remain pinned to their original binary;
// this binary refuses them rather than silently upgrading their data. The
// workspace marker generation still describes the installed Type set.
//
// Counting Types is plain SQL over graph_preview_types and identical on both
// engines, so only the embedded engine is exercised here.
func TestPreviewSchemaVersionAndInstalledTypeCounts(t *testing.T) {
	if SchemaVersion != 11 {
		t.Fatalf("SchemaVersion is %d, want 11 for common canonical public identity", SchemaVersion)
	}
	ctx, o := issueExperimentOptions(t, "embedded")
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	rows, err := s.db.QueryContext(ctx, `SELECT native_attribution FROM graph_preview_versions LIMIT 0`)
	if err != nil {
		t.Fatalf("missing native Link attribution authority: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	old := o
	old.Binding.SchemaVersion = 10
	if prior, err := OpenExisting(ctx, old); err == nil {
		_ = prior.Close()
		t.Fatal("schema-10 binding was silently adopted")
	}
	installed := func() (int, error) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := tx.Rollback(); err != nil {
				t.Error(err)
			}
		}()
		descriptors, err := installedPreviewTypes(ctx, tx, s.ScopeURL())
		return len(descriptors), err
	}
	if count, err := installed(); err != nil || count != 55 {
		t.Fatalf("fresh installation has %d Types with %v; want 55", count, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_types WHERE name='dependency'`); err != nil {
		t.Fatal(err)
	}
	if count, err := installed(); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("partial selected Type installation admitted: %d, %v", count, err)
	}
}

// A prior six-Type generation is rejected even when every old descriptor is
// well-formed and fingerprinted. Read admission cannot adopt or rewrite it.
func TestPriorPreviewTypeInstallationIsNotSilentlyAdopted(t *testing.T) {
	ctx, options := issueExperimentOptions(t, "embedded")
	store, err := OpenExisting(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.db.ExecContext(ctx, `DELETE FROM graph_preview_types`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory", "issue", "dependency", "related", "example-follows", "example-cites"} {
		path := "preview-" + name
		if name == "example-follows" || name == "example-cites" {
			path = name
		}
		spec := graph.TypeDescriptorSpec{ID: store.ScopeURL() + "types/" + path, Name: name, Describes: graph.KindBead}
		if name != "memory" && name != "issue" {
			spec.Describes = graph.KindLink
			endpoint := graph.EndpointConstraint{}
			spec.Source, spec.Target = &endpoint, &endpoint
		}
		descriptor, err := graph.NewTypeDescriptor(spec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.ExecContext(ctx, `INSERT INTO graph_preview_types(name,descriptor,fingerprint) VALUES(?,?,?)`, name, descriptor.CanonicalJSON(), descriptor.Fingerprint()); err != nil {
			t.Fatal(err)
		}
	}
	before := workflowState(t, ctx, store)
	if _, err := store.ListInstalledTypes(ctx); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("older inventory admitted: %v", err)
	}
	if !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
		t.Fatal("refusing an old installation changed its data or coordination")
	}
}

// An unspecified direct Store generation uses the checked catalog, not a CLI
// marker. A valid old twenty-Type baseline is insufficient, even at schema 11.
func TestUnspecifiedStoreGenerationRefusesPriorTwentyTypes(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			if options.TypeGeneration != 0 || options.Binding.SchemaVersion != 11 {
				t.Fatalf("expected unspecified generation at schema11: %+v", options)
			}
			store, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			if current, err := store.ListInstalledTypes(ctx); err != nil || len(current) != 55 {
				t.Fatalf("complete current catalog refused: %d %v", len(current), err)
			}
			if _, err := store.db.ExecContext(ctx, `DELETE FROM graph_preview_types WHERE name LIKE 'native-issue/%' OR name LIKE 'native-dependency/%' OR name IN ('preview-related-v2','example-follows','example-cites')`); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_types`).Scan(&count); err != nil || count != 20 {
				t.Fatalf("old catalog fixture: %d %v", count, err)
			}
			before := workflowState(t, ctx, store)
			// Embedded engine opens serialize. Exercise the checked direct Store
			// entrypoints on this handle, rather than opening a second engine.
			if catalog, err := store.ListInstalledTypes(ctx); !errors.Is(err, ErrInvalidStore) || catalog != nil {
				t.Fatalf("unspecified generation admitted old catalog: %v %v", catalog, err)
			}
			if children, err := store.ListChildren(ctx, "beads/missing"); !errors.Is(err, ErrInvalidStore) || children != nil {
				t.Fatalf("relationship reader bypassed old catalog: %v %v", children, err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
				t.Fatal("old catalog refusal changed data or coordination")
			}
		})
	}
}

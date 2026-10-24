//go:build cgo

package graphstore

import (
	"errors"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// Example and previous preview identities are not aliases for the selected
// Types. Refusing them must not allocate a Link, revise its source or touch
// writer coordination state.
func TestObsoleteInformationalTypesAreNotInstalledOrAdmitted(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			store, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			source, err := store.Create(ctx, CreateRequest{Title: "Source", Path: "beads/source", Body: "Source"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Create(ctx, CreateRequest{Title: "Target", Path: "beads/target", Body: "Target"}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"preview-related", "preview-memory", "preview-issue", "preview-dependency", "type", "bead-type", "link-type", "conforms-to", "refines"} {
				t.Run(name, func(t *testing.T) {
					id := store.ScopeURL() + "types/" + name
					if IsInformationalTypeURL(store.ScopeURL(), id) {
						t.Fatalf("obsolete/intrinsic identity %s was classified as informational", id)
					}
					before := workflowState(t, ctx, store)
					if _, err := store.ReadType(ctx, "types/"+name); !errors.Is(err, ErrNotFound) {
						t.Fatalf("uninstalled Type read: %v", err)
					}
					_, err := store.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/obsolete", SourcePath: "beads/source", TargetPath: "beads/target", TypeURL: id, ExpectedSourceRevision: source.Revision})
					if !errors.Is(err, storage.ErrValidation) && !errors.Is(err, ErrCapabilityUnavailable) {
						t.Fatalf("uninstalled Link Type must be refused as selection, got %v", err)
					}
					if !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
						t.Fatal("refused obsolete selection mutated store")
					}
				})
			}
		})
	}
}

func TestSelectedInformationalTypeInstallationCorruptionIsReadOnly(t *testing.T) {
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
	if _, err := store.db.ExecContext(ctx, `UPDATE graph_preview_types SET fingerprint='bad' WHERE name='references'`); err != nil {
		t.Fatal(err)
	}
	before := workflowState(t, ctx, store)
	if _, err := store.CurrentSnapshot(ctx); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("inventory hid corrupt selected Type: %v", err)
	}
	if _, err := store.ReadType(ctx, "types/references"); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("Type read hid corruption: %v", err)
	}
	if !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
		t.Fatal("invalid installed Type reads changed state")
	}
}

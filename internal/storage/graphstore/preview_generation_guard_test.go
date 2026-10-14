//go:build cgo

package graphstore

import (
	"errors"
	"slices"
	"testing"
)

// The workspace format generation is checked against the Types a fresh init
// installs, so the list it is checked against must be the installer's own list
// and not a second copy that can drift from it.
func TestFreshTypeNamesFollowPreviewTypeDefinitions(t *testing.T) {
	definitions := previewTypeDefinitions(SchemaVersion)
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

// The common-metadata columns change the preview storage layout and require
// SchemaVersion 7. Schema 8 preserves native Dependency fields and Types.
// Both formats open without automatic upgrade; earlier layouts refuse. The
// workspace marker generation still describes the installed Type set.
//
// Counting Types is plain SQL over graph_preview_types and identical on both
// engines, so only the embedded engine is exercised here.
func TestPreviewSchemaVersionAndInstalledTypeCounts(t *testing.T) {
	if SchemaVersion != 8 || LegacySchemaVersion != 7 {
		t.Fatalf("SchemaVersion is %d, want 8 with legacy format 7 still supported", SchemaVersion)
	}
	ctx, o := issueExperimentOptionsForSchema(t, "embedded", LegacySchemaVersion)
	s, err := OpenExisting(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
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
		descriptors, err := installedPreviewTypes(ctx, tx, s.ScopeURL(), s.options.Binding.SchemaVersion)
		return len(descriptors), err
	}
	if count, err := installed(); err != nil || count != 6 {
		t.Fatalf("fresh installation admitted %d Types with %v, want all 6", count, err)
	}
	// Five Types is neither the legacy installation nor the current one.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_types WHERE name='example-cites'`); err != nil {
		t.Fatal(err)
	}
	if count, err := installed(); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("five Types admitted: %d Types with %v, want ErrInvalidStore", count, err)
	}
	// Removing the second example row leaves precisely the original four.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_types WHERE name='example-follows'`); err != nil {
		t.Fatal(err)
	}
	if count, err := installed(); err != nil || count != 4 {
		t.Fatalf("legacy installation admitted %d Types with %v, want the original 4", count, err)
	}
}

//go:build cgo

package graphstore

import (
	"errors"
	"testing"
)

func TestLocalRevisionRemainsStableAcrossDeletionRecovery(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			created, err := s.Create(ctx, CreateRequest{PublicID: "revision-example", Title: "First"})
			if err != nil {
				t.Fatal(err)
			}
			path, err := s.ResolveExactAllocatedResourcePath(ctx, "revision-example")
			if err != nil {
				t.Fatal(err)
			}
			first, err := s.ResolveLocalRevision(ctx, path, 1)
			if err != nil || first.Version != created.Revision || first.Removed {
				t.Fatalf("first = %+v, %v", first, err)
			}
			deleted, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: path, ExpectedRevision: created.Revision})
			if err != nil {
				t.Fatal(err)
			}
			terminal, err := s.ResolveLocalRevision(ctx, path, 2)
			if err != nil || !terminal.Removed || terminal.Version != deleted.Terminal.Revision {
				t.Fatalf("terminal = %+v, %v", terminal, err)
			}
			if _, err := s.ReadVersion(ctx, path, terminal.Version); !errors.Is(err, ErrGone) {
				t.Fatalf("deletion read: %v", err)
			}
			restored, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, FromRevision: first.Version, ExpectedRevision: terminal.Version})
			if err != nil {
				t.Fatal(err)
			}
			for _, before := range []VersionRow{first, terminal} {
				after, err := s.ResolveLocalRevision(ctx, path, before.Ordinal)
				if err != nil || after.Version != before.Version || after.Removed != before.Removed {
					t.Fatalf("alias changed after recovery: %+v, %v", after, err)
				}
			}
			current, err := s.LocalRevisionForToken(ctx, path, restored.Memory.Revision)
			if err != nil || current.Ordinal != 3 || current.Removed {
				t.Fatalf("restored = %+v, %v", current, err)
			}
			for _, n := range []int64{0, -1, 4} {
				if _, err := s.ResolveLocalRevision(ctx, path, n); !errors.Is(err, ErrVersionUnknown) {
					t.Fatalf("ordinal %d: %v", n, err)
				}
			}
			if _, err := s.LocalRevisionForToken(ctx, path, "unavailable"); !errors.Is(err, ErrVersionUnknown) {
				t.Fatalf("absent token: %v", err)
			}
			// Ordinal aliases need the checked ledger, not terminal payload hydration.
			last, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: path, ExpectedRevision: restored.Memory.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_versions SET snapshot='{"state":"deleted"}' WHERE path=? AND version=?`, path, last.Terminal.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err := s.History(ctx, path); !errors.Is(err, ErrInvalidStore) {
				t.Fatalf("fixture did not corrupt terminal content: %v", err)
			}
			if got, err := s.ResolveLocalRevision(ctx, path, 1); err != nil || got.Version != first.Version {
				t.Fatalf("healthy alias depends on terminal payload: %+v %v", got, err)
			}
			if got, err := s.LocalRevisionForToken(ctx, path, last.Terminal.Revision); err != nil || got.Ordinal != 4 || !got.Removed {
				t.Fatalf("D alias depends on terminal payload: %+v %v", got, err)
			}
		})
	}
}

//go:build cgo

package graphstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/memoryops"
)

// A complete real-engine inventory has native Issues and all four Dependency
// types, plus exact keyed learning-like envelopes. Limits apply to accepted
// writes, rather than being demonstrated only with malformed catalog rows.
func TestPreviewCapacity2048MixedInventory(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			_, o := issueExperimentOptions(t, backend)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			s := openVersionsStore(t, ctx, o)
			const families = 128
			for i := range families {
				for _, kind := range []string{"source", "target"} {
					path := fmt.Sprintf("beads/%s-%03d", kind, i)
					if _, err := s.CreateIssue(ctx, path, plainIssue(path)); err != nil {
						t.Fatal(err)
					}
				}
			}
			for i := range families {
				for j, typ := range []types.DependencyType{types.DepBlocks, types.DepParentChild, types.DepDiscoveredFrom, types.DepRelated} {
					if _, err := s.AddDependency(ctx, DependencyRequest{SourcePath: fmt.Sprintf("beads/source-%03d", i), TargetPath: fmt.Sprintf("beads/target-%03d", (i+j)%families), Type: typ, Actor: "fixture"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			anchor, err := s.Create(ctx, CreateRequest{Path: "beads/anchor", Title: "Fixture anchor", Body: strings.Repeat("old writer content ", 64), Actor: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			memories := map[string]string{}
			for i := 0; i < PreviewSnapshotLimit-families*6-1; i++ {
				body, err := json.Marshal(map[string]any{"kind": "fixture-learning", "module": fmt.Sprintf("module-%04d", i), "problem": "simulated fixture problem", "solution": strings.Repeat("existing solution and test evidence; ", 32), "verification": "fixture only; no project qualification"})
				if err != nil {
					t.Fatal(err)
				}
				memories[fmt.Sprintf("@knowledge/fixture/%04d", i)] = string(body)
			}
			m := s.Memories("fixture")
			if _, err := m.Apply(ctx, memoryops.BatchRequest{Remember: memories}); err != nil {
				t.Fatal(err)
			}
			var measurements []map[string]any
			for range 3 {
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				start := time.Now()
				snapshot, err := s.CurrentSnapshot(ctx)
				elapsed := time.Since(start)
				runtime.ReadMemStats(&after)
				if err != nil || len(snapshot.Records) != 2048 || len(snapshot.Types) < len(previewTypeDefinitions()) {
					t.Fatalf("capacity inventory: count=%d types=%d err=%v", len(snapshot.Records), len(snapshot.Types), err)
				}
				measurements = append(measurements, map[string]any{"milliseconds": float64(elapsed.Microseconds()) / 1000, "total_alloc_bytes": after.TotalAlloc - before.TotalAlloc})
			}
			if plane, snap, err := s.ReadContinuity(ctx); err != nil || len(snap.Records) != 2048 || len(plane.Memories) != len(memories) {
				t.Fatalf("complete continuity: rows=%d records=%d err=%v", len(plane.Memories), len(snap.Records), err)
			}
			state := workflowState(t, ctx, s, "config", "graph_preview_types")
			refusals := []func() error{
				func() error { _, e := s.Create(ctx, CreateRequest{Title: "Fixture", Path: "beads/excess"}); return e },
				func() error { _, e := s.CreateIssue(ctx, "beads/excess", plainIssue("extra")); return e },
				func() error {
					_, e := s.AddDependency(ctx, DependencyRequest{SourcePath: "beads/source-000", TargetPath: "beads/target-005", Actor: "fixture"})
					return e
				},
				func() error {
					_, e := s.AddInformationalLink(ctx, LinkCreateRequest{SourcePath: "beads/source-000", TargetPath: "beads/target-000", Actor: "fixture"})
					return e
				},
				func() error {
					_, e := m.Remember(ctx, memoryops.RememberRequest{Key: "new-learning", Content: "extra"})
					return e
				},
				func() error {
					_, e := m.Apply(ctx, memoryops.BatchRequest{Remember: map[string]string{"batch-a": "a", "batch-b": "b"}})
					return e
				},
			}
			for i, write := range refusals {
				if err := write(); !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(state, workflowState(t, ctx, s, "config", "graph_preview_types")) {
					t.Fatalf("writer %d leaked count overflow: %v", i, err)
				}

			}
			// A valid out-of-band 2049th Resource models an older writer; retain
			// its history when reducing it back to the admitted current inventory.
			extra := anchor
			extra.ID = s.ScopeURL() + "beads/old-writer"
			extra.PublicID = "beads/old-writer"
			extra.Revision, err = freshToken()
			if err != nil {
				t.Fatal(err)
			}
			extra.Version = extra.Revision
			properties, err := canonicalJSON(extra.Properties)
			if err != nil {
				t.Fatal(err)
			}
			retained, err := canonicalJSON(extra)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = s.createInTx(ctx, tx, CreateRequest{Title: "Fixture", Path: "beads/old-writer", Actor: extra.Attribution.Actor}, extra, properties, retained)
			if err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if snap, err := s.CurrentSnapshot(ctx); !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(snap, Snapshot{}) {
				t.Fatalf("2049 was truncated or accepted: %v", err)
			}
			smaller := "small"
			shrunk, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/old-writer", Body: &smaller, ExpectedRevision: extra.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if noop, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/old-writer", Body: &smaller, ExpectedRevision: shrunk.Memory.Revision}); err != nil || noop.Changed {
				t.Fatalf("oversized no-op: %v", err)
			}
			if _, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/old-writer", ExpectedRevision: shrunk.Memory.Revision}); err != nil {
				t.Fatal(err)
			}
			if got, err := s.ReadVersion(ctx, "beads/old-writer", extra.Version); err != nil || !reflect.DeepEqual(got, extra) {
				t.Fatalf("reduction erased history: %v", err)
			}
			if snap, err := s.CurrentSnapshot(ctx); err != nil || len(snap.Records) != 2048 {
				t.Fatalf("repaired inventory: %v", err)
			}
			raw, _ := json.Marshal(map[string]any{"backend": backend, "resources": 2048, "issues": families * 2, "native_links": families * 4, "keyed_envelopes": len(memories), "measurements": measurements, "acquisition_byte_limit": PreviewCurrentReadByteLimit})
			t.Logf("CAPACITY_RESULT %s", raw)
		})
	}
}

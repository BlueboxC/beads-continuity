//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestGeneratedPublicIDSkipsLifetimeAndAuthorityCollisions(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			created, err := s.Create(ctx, CreateRequest{PublicID: "notes/aaaaaa", Title: "Reserved forever"})
			if err != nil {
				t.Fatal(err)
			}
			path, err := s.ResolveExactAllocatedResourcePath(ctx, "notes/aaaaaa")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: path, ExpectedRevision: created.Revision}); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			calls := 0
			var accepted string
			err = s.withTx(ctx, false, func(tx *sql.Tx) error {
				var err error
				accepted, err = selectGeneratedPublicIDWithCandidateInTx(ctx, tx, "notes", func(root string, length int) (string, error) {
					calls++
					if root != "notes" {
						t.Fatalf("root changed: %q", root)
					}
					if calls <= 8 {
						if length != 6 {
							t.Fatalf("early lengthening: %d", length)
						}
						return "notes/aaaaaa", nil
					}
					if length != 7 {
						t.Fatalf("no lengthening after collisions: %d", length)
					}
					if calls == 9 {
						return "bdp.json", nil
					}
					if calls == 10 {
						return "types/memory", nil
					}
					return root + "/bbbbbbb", nil
				})
				return err
			})
			if err != nil || accepted != "notes/bbbbbbb" || calls != 11 {
				t.Fatalf("selected %q after %d calls: %v", accepted, calls, err)
			}
			// Selection by itself must not leave an allocation behind.
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("candidate selection changed graph state")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := selectGeneratedPublicIDWithCandidateInTx(cancelled, nil, "", func(string, int) (string, error) { t.Fatal("read entropy after cancellation"); return "", nil }); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation = %v", err)
			}
		})
	}
}

//go:build cgo

package graphstore

import (
	"errors"
	"reflect"
	"testing"
)

func TestExplicitPublicLinkIDNeverExposesPrivateAlias(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			for _, name := range []string{"source", "target"} {
				if _, err := s.CreateIssue(ctx, "beads/"+name, plainIssue(name)); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Create(ctx, CreateRequest{Path: "beads/memory-" + name, Title: name}); err != nil {
					t.Fatal(err)
				}
			}
			for _, backing := range []string{"informational", "dependency"} {
				t.Run(backing, func(t *testing.T) {
					privatePath, publicID := "links/private-"+backing, "public-"+backing
					if backing == "informational" {
						if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: privatePath, PublicID: publicID, SourcePath: "beads/memory-source", TargetPath: "beads/memory-target", TypeURL: s.ScopeURL() + "types/references", UnconditionalSource: true}); err != nil {
							t.Fatal(err)
						}
					} else {
						if _, err := s.AddDependency(ctx, DependencyRequest{Path: privatePath, PublicID: publicID, SourcePath: "beads/source", TargetPath: "beads/target", Actor: "test-author"}); err != nil {
							t.Fatal(err)
						}
					}
					for _, deleted := range []bool{false, true} {
						if deleted {
							if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: privatePath, Actor: "test-author", Unconditional: true, UnconditionalSource: true}); err != nil {
								t.Fatal(err)
							}
						}
						before := workflowState(t, ctx, s)
						if got, err := s.ResolveAllocatedResourcePath(ctx, publicID); err != nil || got != privatePath {
							t.Fatalf("public identity deleted=%v: %q %v", deleted, got, err)
						}
						if got, err := s.ResolveAllocatedResourcePath(ctx, "private-"+backing); !errors.Is(err, ErrNotFound) {
							t.Errorf("private alias deleted=%v selected %q: %v", deleted, got, err)
						}
						if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
							t.Fatal("alias lookup changed state")
						}
					}
				})
			}
			if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/legacy", SourcePath: "beads/memory-source", TargetPath: "beads/memory-target", TypeURL: s.ScopeURL() + "types/references", UnconditionalSource: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, "UPDATE graph_preview_catalog SET public_id=NULL WHERE path=?", "links/legacy"); err != nil {
				t.Fatal(err)
			}
			for _, deleted := range []bool{false, true} {
				if deleted {
					if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/legacy", Unconditional: true, UnconditionalSource: true}); err != nil {
						t.Fatal(err)
					}
				}
				if got, err := s.ResolveAllocatedResourcePath(ctx, "legacy"); err != nil || got != "links/legacy" {
					t.Fatalf("NULL legacy compatibility deleted=%v: %q %v", deleted, got, err)
				}
			}
		})
	}
}

//go:build cgo

package graphstore

import (
	"reflect"
	"testing"
)

func TestInformationalPairWritesSelectOpaquePublicBeadID(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			store, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			owner := plainIssue("Path owner")
			owner.Issue.ID = "exp-x"
			pathOwner, err := store.CreateIssue(ctx, "beads/exp-x", owner)
			if err != nil {
				t.Fatal(err)
			}
			literal := plainIssue("Literal public ID")
			literal.Issue.ID = "beads/exp-x"
			literal.ForceIDPrefix = true
			selected, err := store.CreateIssue(ctx, "", literal)
			if err != nil {
				t.Fatal(err)
			}
			memory, err := store.Create(ctx, CreateRequest{PublicID: "beads/context", Title: "Context"})
			if err != nil {
				t.Fatal(err)
			}
			for _, reverse := range []bool{false, true} {
				request := LinkCreateRequest{TypeURL: store.ScopeURL() + "types/references", Path: "links/out", SourcePath: "beads/beads/exp-x", SourceLocalID: "beads/exp-x", TargetPath: "beads/" + memory.PublicID, TargetLocalID: memory.PublicID}
				if reverse {
					request.Path = "links/in"
					request.SourcePath, request.TargetPath = request.TargetPath, request.SourcePath
					request.SourceLocalID, request.TargetLocalID = request.TargetLocalID, request.SourceLocalID
				}
				created, err := store.AddInformationalLink(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				wantSource, wantTarget := selected.ID, memory.ID
				if reverse {
					wantSource, wantTarget = memory.ID, selected.ID
				}
				if created.Link.Source != wantSource || created.Link.Target != wantTarget || created.Link.Source == pathOwner.ID || created.Link.Target == pathOwner.ID {
					t.Fatalf("informational Link selected path owner: %+v", created.Link)
				}
				removed, err := store.Unlink(ctx, LinkDeleteRequest{
					SourcePath: request.SourcePath, SourceLocalID: request.SourceLocalID,
					TargetPath: request.TargetPath, TargetLocalID: request.TargetLocalID,
					TypeURL: request.TypeURL, Unconditional: true, DefaultInformationalSource: true,
				})
				if err != nil || !removed.Changed || removed.Link.ID != created.Link.ID {
					t.Fatalf("pair unlink selected path owner or wrong Link: %+v, %v", removed, err)
				}
			}
		})
	}
}

// A Link's exact public ID must not fall through to a different Memory whose
// path has that Link ID as a suffix. Both pair writes use this selector.
func TestInformationalPairWritesRefuseExactLinkIDAsBead(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		for _, state := range []string{"live", "deleted"} {
			t.Run(backend+"/"+state, func(t *testing.T) {
				ctx, options := issueExperimentOptions(t, backend)
				store, err := OpenExisting(ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				for _, path := range []string{"beads/source", "beads/target", "beads/links/x"} {
					if _, err := store.Create(ctx, CreateRequest{Path: path, Title: path}); err != nil {
						t.Fatal(err)
					}
				}
				typ := store.ScopeURL() + "types/references"
				original, err := store.AddInformationalLink(ctx, LinkCreateRequest{
					Path: "links/x", TypeURL: typ, SourcePath: "beads/source", TargetPath: "beads/target",
				})
				if err != nil {
					t.Fatal(err)
				}
				if state == "deleted" {
					if _, err := store.Unlink(ctx, LinkDeleteRequest{Path: "links/x", ExpectedRevision: original.Link.Revision}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.AddInformationalLink(ctx, LinkCreateRequest{
					Path: "links/pair-decoy", TypeURL: typ, SourcePath: "beads/links/x", TargetPath: "beads/target",
				}); err != nil {
					t.Fatal(err)
				}
				beforePair := workflowState(t, ctx, store)
				if _, err := store.Unlink(ctx, LinkDeleteRequest{
					SourcePath: "beads/links/x", SourceLocalID: "links/x", TargetPath: "beads/target",
					TypeURL: typ, Unconditional: true, DefaultInformationalSource: true,
				}); err == nil {
					t.Error("pair unlink selected the Memory suffix of an exact Link ID")
				}
				if after := workflowState(t, ctx, store); !reflect.DeepEqual(beforePair, after) {
					t.Error("refused pair unlink changed graph state")
				}
				beforeCreate := workflowState(t, ctx, store)
				if _, err := store.AddInformationalLink(ctx, LinkCreateRequest{
					Path: "links/wrong", TypeURL: typ, SourcePath: "beads/links/x", SourceLocalID: "links/x", TargetPath: "beads/target",
				}); err == nil {
					t.Error("Link create selected the Memory suffix of an exact Link ID")
				}
				if after := workflowState(t, ctx, store); !reflect.DeepEqual(beforeCreate, after) {
					t.Error("refused Link create changed graph state")
				}
			})
		}
	}
}

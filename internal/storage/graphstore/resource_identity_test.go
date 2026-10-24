//go:build cgo

package graphstore

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	graph "github.com/steveyegge/beads/graphops"
)

func TestAllocatedResourceIdentityUsesCatalogKind(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, store := memoryDeleteStore(t, engine)
			memory, err := store.Create(ctx, CreateRequest{PublicID: "links/a-memory", Title: "Literal prefix"})
			if err != nil {
				t.Fatal(err)
			}
			path := fixtureResourcePath(t, ctx, store, memory.ID)
			got, err := store.ResolveAllocatedResourceIdentity(ctx, "links/a-memory")
			want := AllocatedResourceIdentity{PublicID: "links/a-memory", PrivatePath: path, Kind: graph.KindBead, State: "live"}
			if err != nil || got != want {
				t.Fatalf("prefix selected kind: %+v %v", got, err)
			}
			publicURI, err := graph.CanonicalPublicResourceURL(store.ScopeURL(), want.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			byURI, err := store.ResolveCanonicalPublicResourceIdentity(ctx, publicURI)
			if err != nil || byURI != want {
				t.Fatalf("canonical public identity did not select allocation: %+v %v", byURI, err)
			}
			if memory.ID != publicURI {
				t.Fatal("record does not expose its canonical public identity")
			}
			if got, err := store.ResolveCanonicalPublicResourceIdentity(ctx, memory.ID); err != nil || got != want {
				t.Fatalf("record public URI: %+v %v", got, err)
			}
			privateURI := store.ScopeURL() + path
			if _, err := store.ResolveCanonicalPublicResourceIdentity(ctx, privateURI); privateURI != publicURI && !errors.Is(err, ErrNotFound) {
				t.Fatalf("old private record URI was relabeled as public identity: %v", err)
			}
			issueRequest := plainIssue("Catalog Issue kind")
			issueRequest.Issue.ID = "types/an-issue"
			issueRequest.ForceIDPrefix = true
			issueCreated, err := store.CreateIssue(ctx, "beads/private-issue", issueRequest)
			if err != nil {
				t.Fatal(err)
			}
			issueIdentity, err := store.ResolveAllocatedResourceIdentity(ctx, "types/an-issue")
			if err != nil || issueIdentity.Kind != graph.KindBead || issueIdentity.PrivatePath != "beads/private-issue" || issueIdentity.PublicID != "types/an-issue" {
				t.Fatalf("native Issue public identity: %+v %v", issueIdentity, err)
			}
			link, err := store.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/reference", TypeURL: store.ScopeURL() + "types/references", SourcePath: path, TargetPath: "beads/private-issue"})
			if err != nil {
				t.Fatal(err)
			}
			linkIdentity, err := store.ResolveAllocatedResourceIdentity(ctx, "links/reference")
			if err != nil || linkIdentity.Kind != graph.KindLink || store.ScopeURL()+linkIdentity.PrivatePath != link.Link.ID {
				t.Fatalf("Link public identity: %+v %v", linkIdentity, err)
			}
			if _, err := store.Unlink(ctx, LinkDeleteRequest{Path: linkIdentity.PrivatePath, ExpectedRevision: link.Link.Revision}); err != nil {
				t.Fatal(err)
			}
			deletedLink, err := store.ResolveAllocatedResourceIdentity(ctx, "links/reference")
			if err != nil || deletedLink.State != "deleted" || deletedLink.Kind != graph.KindLink || deletedLink.PrivatePath != linkIdentity.PrivatePath {
				t.Fatalf("deleted Link identity: %+v %v", deletedLink, err)
			}
			if _, err := store.DeleteIssue(ctx, IssueDeleteRequest{Path: issueIdentity.PrivatePath, ExpectedRevision: issueCreated.Revision}); err != nil {
				t.Fatal(err)
			}
			deletedIssue, err := store.ResolveAllocatedResourceIdentity(ctx, "types/an-issue")
			if err != nil || deletedIssue.State != "deleted" || deletedIssue.Kind != graph.KindBead || deletedIssue.PrivatePath != issueIdentity.PrivatePath {
				t.Fatalf("deleted Issue identity: %+v %v", deletedIssue, err)
			}
			reverse, err := store.AllocatedResourceIdentityForPath(ctx, path)
			if err != nil || reverse != want {
				t.Fatalf("private locator lost public identity: %+v %v", reverse, err)
			}
			encoded, err := json.Marshal(got)
			if err != nil || string(encoded) != "{}" {
				t.Fatal("allocation helper leaked an assumed wire projection", string(encoded), err)
			}
			if _, err := store.DeleteMemory(ctx, MemoryDeleteRequest{Path: path, ExpectedRevision: memory.Revision}); err != nil {
				t.Fatal(err)
			}
			want.State = "deleted"
			got, err = store.ResolveAllocatedResourceIdentity(ctx, want.PublicID)
			if err != nil || got != want {
				t.Fatalf("deleted identity was lost: %+v %v", got, err)
			}
			reverse, err = store.AllocatedResourceIdentityForPath(ctx, path)
			if err != nil || reverse != want {
				t.Fatalf("deleted reverse identity: %+v %v", reverse, err)
			}
			before := workflowState(t, ctx, store)
			if _, err := store.ResolveCanonicalPublicResourceIdentity(ctx, store.ScopeURL()+"a//b"); err == nil {
				t.Fatal("canonical helper accepted unselected legacy-key mapping")
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
				t.Fatal("read helper changed state")
			}
			if _, err := store.ResolveAllocatedResourceIdentity(ctx, "missing"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing allocation: %v", err)
			}
			// Corrupt a disposable catalog to prove a duplicate cannot silently
			// choose one kind. This is not an identity migration or authoring API.
			if _, err := store.db.ExecContext(ctx, `UPDATE graph_preview_catalog SET backing_key=? WHERE path='beads/private-issue'`, want.PublicID); err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, store)
			if _, err := store.ResolveAllocatedResourceIdentity(ctx, want.PublicID); !errors.Is(err, ErrInvalidStore) {
				t.Fatalf("duplicate public ID selected a Resource: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, store)) {
				t.Fatal("duplicate refusal changed state")
			}
		})
	}
}

func TestAllocatedIdentityReadDoesNotAcquireUnrelatedPayload(t *testing.T) {
	ctx, _, store := memoryDeleteStore(t, "embedded")
	selected, err := store.Create(ctx, CreateRequest{PublicID: "selected", Title: "Selected"})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := store.Create(ctx, CreateRequest{PublicID: "unrelated", Title: "Unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	path := fixtureResourcePath(t, ctx, store, unrelated.ID)
	if _, err := store.db.ExecContext(ctx, `UPDATE graph_preview_payloads SET properties=REPEAT('x',?) WHERE path=?`, PreviewCurrentReadByteLimit, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CurrentSnapshot(ctx); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("whole-Scope control did not exceed budget: %v", err)
	}
	identity, err := store.ResolveAllocatedResourceIdentity(ctx, "selected")
	if err != nil || identity.PublicID != "selected" || store.ScopeURL()+identity.PublicID != selected.ID {
		t.Fatalf("scoped identity acquired unrelated payload: %+v %v", identity, err)
	}
}

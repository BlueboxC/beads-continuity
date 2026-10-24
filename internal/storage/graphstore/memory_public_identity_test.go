//go:build cgo

package graphstore

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

func TestMemoryPublicIdentityExactLifecycle(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			keys := []string{"plain", "PLAIN", "snow/%E9%9B%AA", "%C3%A9", "e%CC%81", "beads/plain", "beads/free", "links/memory", "types/memory-copy", "a@b", strings.Repeat("long", 600)}
			for _, key := range keys {
				body := "  Exact body\n雪\n"
				created, err := s.Create(ctx, CreateRequest{PublicID: key, Title: "Independent title", Body: body})
				if err != nil {
					t.Fatal(err)
				}
				path, err := s.ResolveExactAllocatedResourcePath(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				if created.ID != s.ScopeURL()+key || created.PublicID != key || created.Properties.Body != body {
					t.Fatalf("identity/content changed: %+v", created)
				}
				selected, err := s.ResolveExactAllocatedResourcePath(ctx, key)
				if err != nil || selected != path {
					t.Fatalf("resolve %q: %q %v", key, selected, err)
				}
				_, read, err := s.ReadResourceSelected(ctx, "beads/"+key, key)
				if err != nil || !reflect.DeepEqual(read, created) {
					t.Fatalf("read %q: %+v %v", key, read, err)
				}
				retained, err := s.ReadVersion(ctx, created.ID, created.Revision)
				if err != nil || !reflect.DeepEqual(retained, created) {
					t.Fatalf("retained %q: %+v %v", key, retained, err)
				}
				next := "Replacement body"
				edited, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, Body: &next, ExpectedRevision: created.Revision})
				if err != nil || edited.Memory.PublicID != key || edited.Memory.Properties.Title != created.Properties.Title {
					t.Fatalf("overwrite: %+v %v", edited, err)
				}
				deleted, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: path, ExpectedRevision: edited.Memory.Revision})
				if err != nil || deleted.Terminal == nil {
					t.Fatalf("delete: %+v %v", deleted, err)
				}
				before := workflowState(t, ctx, s)
				if _, err := s.Create(ctx, CreateRequest{PublicID: key, Title: "Forbidden reuse"}); !errors.Is(err, ErrAlreadyExists) {
					t.Fatalf("reuse: %v", err)
				}
				if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatal("reuse changed state")
				}
				restored, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, FromRevision: created.Revision, ExpectedRevision: deleted.Terminal.Revision})
				if err != nil || restored.Memory.PublicID != key || restored.Memory.ID != created.ID || restored.Memory.Revision == created.Revision {
					t.Fatalf("recovery: %+v %v", restored, err)
				}
			}
		})
	}
}

func TestMemoryPublicIdentityCrossKindAndAmbiguity(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			issueReq := plainIssue("Issue")
			issueReq.Issue.ID = "exp-overlap"
			if _, err := s.CreateIssue(ctx, "beads/issue", issueReq); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			if _, err := s.Create(ctx, CreateRequest{PublicID: "exp-overlap", Title: "Collision"}); !errors.Is(err, ErrAlreadyExists) {
				t.Fatalf("collision: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("collision changed state")
			}
			one, err := s.Create(ctx, CreateRequest{PublicID: "one", Title: "One"})
			if err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			for _, key := range []string{one.ID, "..", "a//b", "\x00", "雪", "a?b", "%61", "%2F"} {
				_, err := s.Create(ctx, CreateRequest{PublicID: key, Title: "Rejected key"})
				if !errors.Is(err, storage.ErrValidation) {
					t.Fatalf("nonrepresentable key %q: %v", key, err)
				}
				if !strings.Contains(err.Error(), key) && !strings.Contains(err.Error(), "\\x00") {
					t.Fatalf("key refusal omitted offending key %q: %v", key, err)
				}
				if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatal("rejected identity changed state")
				}
			}
			_, mismatch := s.AddInformationalLink(ctx, LinkCreateRequest{PublicID: "mismatched", TypeURL: s.ScopeURL() + "types/references", SourcePath: "beads/another", SourceLocalID: "one", TargetPath: one.ID})
			if !errors.Is(mismatch, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("mismatched path/public ID did not refuse atomically", mismatch)
			}
			selected, err := s.ResolveMemorySelector(ctx, one.ID)
			literal, literalErr := s.ResolveMemorySelector(ctx, "one")
			if err != nil || literalErr != nil || selected != literal {
				t.Fatalf("canonical/exact selection disagree: %s %s %v %v", selected, literal, err, literalErr)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("selection changed state")
			}
		})
	}
}

// A Memory public ID may resemble another allocation's private locator. Issue
// discovery must compare public identity, never that accidental SQL spelling.
func TestCommonIdentityIssueSelectionDoesNotAliasMemory(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			request := plainIssue("Unrelated Issue")
			request.Issue.ID = "exp-real"
			issue, err := s.CreateIssue(ctx, "beads/chosen", request)
			if err != nil {
				t.Fatal(err)
			}
			memory, err := s.Create(ctx, CreateRequest{PublicID: "chosen", Title: "Memory"})
			if err != nil {
				t.Fatal(err)
			}
			memoryPath, err := s.ResolveExactAllocatedResourcePath(ctx, "chosen")
			if err != nil || memoryPath == "beads/chosen" {
				t.Fatalf("fixture did not separate locator: %q %v", memoryPath, err)
			}
			before := workflowState(t, ctx, s)
			selector := IssueSelector{Path: "beads/chosen", NativeID: "chosen"}
			if _, err := s.ShowIssueSelected(ctx, selector); !errors.Is(err, ErrCapabilityUnavailable) {
				t.Fatalf("Memory selected as Issue: %v", err)
			}
			paths, err := s.ResolveIssuePaths(ctx, []IssueSelector{selector, {Path: "beads/exp-real", NativeID: "exp-real"}})
			if err != nil || !reflect.DeepEqual(paths, []string{"chosen", issue.Properties.ID}) {
				t.Fatalf("count public IDs: %v %v", paths, err)
			}
			if memory.ID != s.ScopeURL()+"chosen" || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("selection changed allocation state")
			}
		})
	}
}

func TestMemoryPublicIdentityIsNotBDPWire(t *testing.T) {
	record := Record{ID: "https://example.invalid/beads/locator", Type: "https://example.invalid/types/memory", Revision: "r", Version: "r", Properties: Properties{Title: "Title", Body: "Body"}}
	before, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	record.PublicID = " exact 雪 @key "
	after, err := canonicalJSON(record)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("local public ID altered pinned canonical record JSON")
	}
}

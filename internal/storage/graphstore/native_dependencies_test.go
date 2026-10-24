//go:build cgo

package graphstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// Real native rows, retained graph state and scheduling share one transaction;
// changing an edge's display Type must not change its Issue-domain meaning.
func TestNativeDependenciesPreserveFieldsAndWorkflow(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, backend)
			s := openVersionsStore(t, ctx, o)
			identities := map[string]string{}
			for _, name := range []string{"parent", "child", "gate", "source"} {
				created, err := s.CreateIssue(ctx, "beads/"+name, plainIssue(name))
				if err != nil {
					t.Fatal(err)
				}
				identities[name] = created.ID
			}
			var links []LinkRecord
			for i, req := range []DependencyRequest{
				{SourcePath: "beads/child", TargetPath: "beads/parent", Type: types.DepParentChild},
				{SourcePath: "beads/parent", TargetPath: "beads/gate", Type: types.DepBlocks},
				{SourcePath: "beads/source", TargetPath: "beads/parent", Type: types.DepDiscoveredFrom},
				{SourcePath: "beads/source", TargetPath: "beads/child", Type: types.DepRelated},
			} {
				req.Path, req.Actor, req.Metadata, req.ThreadID = "links/"+string(req.Type), "native-author", []byte(`{"cause":"diagnóstico","evidence":{"path":"tests/check.go"}}`), "thread-記憶"
				before, err := s.ShowIssue(ctx, req.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				// Failure after the native fields were written must roll back even
				// metadata, journaling and derived blocking, before graph allocation.
				state := workflowState(t, ctx, s)
				fault := errors.New("native-fields rollback")
				s.afterWrite = func(stage string) error {
					if stage == "dependency-fields" {
						return fault
					}
					return nil
				}
				failed, err := s.AddDependency(ctx, req)
				s.afterWrite = nil
				if !errors.Is(err, fault) || !reflect.DeepEqual(failed, DependencyResult{}) || !reflect.DeepEqual(state, workflowState(t, ctx, s)) {
					t.Fatalf("%d: leaked failed native mutation: %v", i, err)
				}
				added, err := s.AddDependency(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				wantType, _ := NativeDependencyTypeURLForSchema(s.ScopeURL(), req.Type, SchemaVersion)
				if !added.Changed || added.Link.Type != wantType || added.Link.Type == RelatedTypeURL(s.ScopeURL()) || added.Source.Version == before.Version {
					t.Fatalf("native Type/ownership lost: %+v", added)
				}
				var nativeID, metadata, thread, actor, typ string
				if err := s.db.QueryRowContext(ctx, `SELECT d.id,d.metadata,d.thread_id,d.created_by,d.type FROM dependencies d JOIN graph_preview_catalog c ON c.backing_key=d.id WHERE c.path=?`, req.Path).Scan(&nativeID, &metadata, &thread, &actor, &typ); err != nil {
					t.Fatal(err)
				}
				canonical, err := commonMetadata([]byte(metadata))
				if err != nil || !bytes.Equal(added.Link.Metadata, canonical) || nativeID != added.Link.Properties["nativeId"] || thread != req.ThreadID || thread != added.Link.Properties["threadId"] || actor != req.Actor || added.Link.Attribution.Actor != actor || typ != string(req.Type) {
					t.Fatalf("native fields changed: %+v %v", added.Link, err)
				}
				state = workflowState(t, ctx, s)
				noop, err := s.AddDependency(ctx, req)
				if err != nil || noop.Changed || !reflect.DeepEqual(added, DependencyResult{Link: noop.Link, Source: noop.Source, Target: noop.Target, Changed: true}) || !reflect.DeepEqual(state, workflowState(t, ctx, s)) {
					t.Fatalf("same native intent was not a no-op: %+v %v", noop, err)
				}
				for _, changed := range []DependencyRequest{
					func() DependencyRequest {
						r := req
						r.Type = types.DepRelated
						if r.Type == req.Type {
							r.Type = types.DepBlocks
						}
						return r
					}(),
					func() DependencyRequest { r := req; r.ThreadID = "other"; return r }(),
					func() DependencyRequest { r := req; r.Metadata = []byte(`{"cause":"different"}`); return r }(),
				} {
					if _, err := s.AddDependency(ctx, changed); !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(state, workflowState(t, ctx, s)) {
						t.Fatalf("same pair silently changed: %v", err)
					}
				}
				if got, err := s.ReadVersion(ctx, req.Path, added.Link.Version); err != nil || !reflect.DeepEqual(got, added.Link) {
					t.Fatalf("native retained Link: %v", err)
				}
				if got, err := s.ReadVersion(ctx, req.SourcePath, added.Source.Version); err != nil || !sameVersionJSON(mustCanonicalNativeTest(t, got), added.Source) {
					t.Fatalf("native retained owner: %v", err)
				}
				incident, err := s.ListLinks(ctx, LinksRequest{BeadPath: req.TargetPath, Direction: "in", TypeURL: wantType})
				if err != nil || len(incident) != 1 || !reflect.DeepEqual(incident[0], added.Link) {
					t.Fatalf("typed incoming relation: %+v %v", incident, err)
				}
				links = append(links, added.Link)
			}
			assertReadyIDs(t, ctx, s, identities["gate"], identities["source"])
			assertNativeWorkParity(t, ctx, s)
			blocked, err := s.BlockedIssues(ctx)
			if err != nil || len(blocked) != 2 {
				t.Fatalf("parent blocker not inherited: %+v %v", blocked, err)
			}
			// A child's ordinary hierarchy witness may name the parent rather
			// than the gate; compare the native query, not a rewritten predicate.
			until := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
			if _, err := s.SetIssueDeferred(ctx, IssueDeferralRequest{Path: "beads/parent", Actor: "owner", Unconditional: true, Deferred: true, Until: &until}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CloseIssue(ctx, "beads/gate", "verified", "owner"); err != nil {
				t.Fatal(err)
			}
			assertReadyIDs(t, ctx, s, identities["source"])
			assertNativeWorkParity(t, ctx, s)
			if _, err := s.SetIssueDeferred(ctx, IssueDeferralRequest{Path: "beads/parent", Actor: "owner", Unconditional: true}); err != nil {
				t.Fatal(err)
			}
			assertReadyIDs(t, ctx, s, identities["child"], identities["parent"], identities["source"])
			state := workflowState(t, ctx, s)
			if _, err := s.CloseIssue(ctx, "beads/parent", "premature", "owner"); err == nil || !reflect.DeepEqual(state, workflowState(t, ctx, s)) {
				t.Fatalf("parent closed over open child: %v", err)
			}
			if _, err := s.CloseIssue(ctx, "beads/child", "verified", "owner"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CloseIssue(ctx, "beads/parent", "verified", "owner"); err != nil {
				t.Fatal(err)
			}
			assertNativeWorkParity(t, ctx, s)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openVersionsStore(t, ctx, o)
			for i, link := range links {
				path := strings.TrimPrefix(link.ID, s.ScopeURL())
				current, err := s.ShowLink(ctx, path)
				if err != nil || !reflect.DeepEqual(current, link) {
					t.Fatalf("reopen lost native Link: %v", err)
				}
				sourceRecord, err := s.Read(ctx, link.Source)
				if err != nil {
					t.Fatal(err)
				}
				source, ok := sourceRecord.(IssueRecord)
				if !ok {
					t.Fatalf("native source: %T", sourceRecord)
				}
				removed, err := s.Unlink(ctx, LinkDeleteRequest{Path: path, ExpectedRevision: link.Revision, ExpectedSourceRevision: source.Revision, Actor: "remover"})
				if err != nil || removed.Link.Type != link.Type {
					t.Fatalf("%d: unlink native type: %+v %v", i, removed, err)
				}
				if got, err := s.ReadVersion(ctx, path, link.Version); err != nil || !reflect.DeepEqual(got, link) {
					t.Fatalf("unlink erased evidence: %v", err)
				}
				if _, err := s.ShowLink(ctx, path); !errors.Is(err, ErrGone) {
					t.Fatalf("deleted relation visible: %v", err)
				}
				if _, _, err := s.Versions(ctx, path); err != nil {
					t.Fatalf("native history after unlink: %v", err)
				}
			}
			if _, err := s.CurrentSnapshot(ctx); err != nil {
				t.Fatalf("final current inventory: %v", err)
			}
		})
	}
}

func assertNativeWorkParity(t *testing.T, ctx context.Context, s *Store) {
	t.Helper()
	ready, err := s.ReadyIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	native, err := issueops.GetReadyWorkInTx(ctx, s.db, types.WorkFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for _, r := range ready {
		got = append(got, r.Properties.ID)
	}
	for _, r := range native {
		want = append(want, r.ID)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("native ready mismatch: got %v want %v", got, want)
	}
}

func TestNativeDependenciesRefuseUnsupportedWithoutMutation(t *testing.T) {
	for _, version := range []int{SchemaVersion} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, "embedded")
			s := openVersionsStore(t, ctx, o)
			for _, name := range []string{"source", "target"} {
				if _, err := s.CreateIssue(ctx, "beads/"+name, plainIssue(name)); err != nil {
					t.Fatal(err)
				}
			}
			base := DependencyRequest{SourcePath: "beads/source", TargetPath: "beads/target", Actor: "owner"}
			requests := []DependencyRequest{
				func() DependencyRequest {
					r := base
					r.Type = types.DependencyType(strings.Repeat("x", types.MaxDependencyTypeLen+1))
					return r
				}(),
				func() DependencyRequest { r := base; r.Metadata = json.RawMessage(`[]`); return r }(),
				func() DependencyRequest { r := base; r.ThreadID = strings.Repeat("x", 256); return r }(),
			}
			if version == LegacySchemaVersion {
				requests = append(requests, func() DependencyRequest { r := base; r.Type = types.DepParentChild; return r }(), func() DependencyRequest { r := base; r.Metadata = []byte(`{"unsupported":true}`); return r }())
			}
			before := workflowState(t, ctx, s)
			for _, r := range requests {
				if _, err := s.AddDependency(ctx, r); err == nil || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("unsupported intent changed rows: %v", err)
				}
			}
			added, err := s.AddDependency(ctx, base)
			if err != nil {
				t.Fatal(err)
			}
			if version == LegacySchemaVersion && (added.Link.Type != DependencyTypeURL(s.ScopeURL()) || len(added.Link.Properties) != 0) {
				t.Fatalf("legacy Link upgraded: %+v", added.Link)
			}
			if _, err := s.CurrentSnapshot(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func mustCanonicalNativeTest(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := canonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

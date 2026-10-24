//go:build cgo

package graphstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func nativeMetadata(value string) publicops.MetadataPatch {
	return publicops.MetadataPatch{Replace: publicops.Field[json.RawMessage]{Set: true, Value: json.RawMessage(value)}}
}

func setupHistoricalNativeKind(t *testing.T, engine string, kind types.DependencyType) dependencyUnlinkFixture {
	t.Helper()
	f := setupDependencyUnlink(t, engine, 0)
	added, err := f.store.AddDependency(f.ctx, DependencyRequest{Path: "links/first", SourcePath: "beads/source", TargetPath: "beads/first", Type: kind, ExpectedSourceRevision: f.source.Revision, Actor: "author"})
	if err != nil {
		t.Fatal(err)
	}
	f.source, f.links = added.Source, []LinkRecord{added.Link}
	return f
}

func TestHistoricalNativeLinkCompositionRecovery(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			f := setupDependencyUnlink(t, engine, 1)
			s, ctx := f.store, issueops.WithEventsJournal(f.ctx, true)
			initial := f.links[0]
			var creator, createdAt string
			if err := s.db.QueryRowContext(ctx, `SELECT created_by,CAST(created_at AS CHAR) FROM dependencies WHERE issue_id=? AND depends_on_issue_id=?`, f.source.Properties.ID, f.targets[0].Properties.ID).Scan(&creator, &createdAt); err != nil {
				t.Fatal(err)
			}
			metadata, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", MetadataOnly: true, Metadata: nativeMetadata(`{"old":true}`), ExpectedRevision: initial.Revision, ExpectedSourceRevision: f.source.Revision, Actor: "metadata-author"})
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", Properties: map[string]any{"dep_type": "blocks"}, Metadata: nativeMetadata(`{"current":true}`), ExpectedRevision: metadata.Link.Revision, ExpectedSourceRevision: metadata.Source.(IssueRecord).Revision, Actor: "current-author"})
			if err != nil {
				t.Fatal(err)
			}
			assertDependencyReadiness(t, ctx, s, current.Source.(IssueRecord), false)
			// The concrete declared Type is immutable even when its native property
			// is supplied through the generic Link editor.
			beforeReclassification := workflowState(t, ctx, s)
			_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", Properties: map[string]any{"dep_type": "relates-to"}, ExpectedRevision: current.Link.Revision, ExpectedSourceRevision: current.Source.(IssueRecord).Revision, Actor: "refused-reclassification"})
			if !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(beforeReclassification, workflowState(t, ctx, s)) {
				t.Fatalf("concrete Dependency Type changed or refusal wrote: %v", err)
			}
			var ordinal int
			if err := s.db.QueryRowContext(ctx, `SELECT current_revision FROM issues WHERE id=?`, f.source.Properties.ID).Scan(&ordinal); err != nil {
				t.Fatal(err)
			}
			restored, err := s.PatchLinkProperties(ctx, LinkPropertiesPatchRequest{Path: "links/first", FromRevision: metadata.Link.Revision, Patch: []byte(`[{"op":"replace","path":"/dep_type","value":"blocks"}]`), Metadata: publicops.MetadataPatch{Set: map[string]json.RawMessage{"explicit": json.RawMessage(`7`)}}, ExpectedRevision: current.Link.Revision, ExpectedSourceRevision: current.Source.(IssueRecord).Revision, Actor: "historical-author"})
			if err != nil {
				t.Fatal(err)
			}
			if !restored.Changed || restored.Link.ID != initial.ID || restored.Link.Source != initial.Source || restored.Link.Target != initial.Target || restored.Link.Type != initial.Type || restored.Link.Properties["dep_type"] != "blocks" || string(restored.Link.Metadata) != `{"explicit":7,"old":true}` {
				t.Fatalf("wrong native composition: %+v", restored)
			}
			if h := restored.Link.Attribution.HistoricalUpdate; h == nil || h.FromRevision != metadata.Link.Revision || h.PreviousRevision != current.Link.Revision || restored.Link.Attribution.Actor != "historical-author" {
				t.Fatalf("wrong attribution: %+v", restored.Link)
			}
			var removedEvents, addedEvents int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bd_events_journal WHERE issue_id=? AND actor='historical-author' AND op='dep_remove'`, f.source.Properties.ID).Scan(&removedEvents); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bd_events_journal WHERE issue_id=? AND actor='historical-author' AND op='dep_add'`, f.source.Properties.ID).Scan(&addedEvents); err != nil || removedEvents != 0 || addedEvents != 1 {
				t.Fatalf("metadata update must journal one upsert: adds=%d removes=%d %v", addedEvents, removedEvents, err)
			}
			var nextOrdinal int
			if err := s.db.QueryRowContext(ctx, `SELECT current_revision FROM issues WHERE id=?`, f.source.Properties.ID).Scan(&nextOrdinal); err != nil || nextOrdinal != ordinal+1 {
				t.Fatalf("source versions %d -> %d: %v", ordinal, nextOrdinal, err)
			}
			var nextCreator, nextCreatedAt string
			if err := s.db.QueryRowContext(ctx, `SELECT created_by,CAST(created_at AS CHAR) FROM dependencies WHERE issue_id=? AND depends_on_issue_id=?`, f.source.Properties.ID, f.targets[0].Properties.ID).Scan(&nextCreator, &nextCreatedAt); err != nil || creator != nextCreator || createdAt != nextCreatedAt {
				t.Fatalf("native creation facts changed: %s %s -> %s %s: %v", creator, createdAt, nextCreator, nextCreatedAt, err)
			}
			assertDependencyReadiness(t, ctx, s, restored.Source.(IssueRecord), false)
			assertDependencyOwned(t, restored.Source.(IssueRecord), restored.Link)
			assertLinkPatchSaved(t, ctx, s, "links/first", initial, metadata.Link, current.Link, restored.Link)
			shown, err := s.ShowLink(ctx, "links/first")
			if err != nil || !reflect.DeepEqual(shown, restored.Link) {
				t.Fatalf("current authority differs: %+v %v", shown, err)
			}
			before := workflowState(t, ctx, s)
			noop, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: restored.Link.Revision, ExpectedRevision: restored.Link.Revision, ExpectedSourceRevision: restored.Source.(IssueRecord).Revision, Actor: "noop"})
			if err != nil || noop.Changed || !reflect.DeepEqual(noop.Link, restored.Link) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("native no-op wrote: %+v %v", noop, err)
			}
			emptyActor, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: restored.Link.Revision, ExpectedRevision: restored.Link.Revision, ExpectedSourceRevision: restored.Source.(IssueRecord).Revision})
			if err != nil || emptyActor.Changed || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("non-authoring no-op requires actor: %+v %v", emptyActor, err)
			}
			deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/first", ExpectedRevision: restored.Link.Revision, ExpectedSourceRevision: restored.Source.(IssueRecord).Revision, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			history, err := s.History(ctx, "links/first")
			if err != nil || history.CurrentState == nil || history.CurrentState.Revision != deleted.Link.Revision {
				t.Fatalf("native terminal missing: %+v %v", history, err)
			}
			recovered, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: metadata.Link.Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(IssueRecord).Revision, Actor: "recover"})
			if err != nil {
				t.Fatal(err)
			}
			if !recovered.Changed || recovered.Link.ID != initial.ID || recovered.Link.Revision == deleted.Link.Revision || recovered.Link.Attribution.Inception != "revert" || string(recovered.Link.Metadata) != `{"old":true}` {
				t.Fatalf("wrong native recovery: %+v", recovered)
			}
			assertDependencyOwned(t, recovered.Source.(IssueRecord), recovered.Link)
			assertDependencyReadiness(t, ctx, s, recovered.Source.(IssueRecord), false)
			if _, err := s.ReadVersion(ctx, "links/first", deleted.Link.Revision); !errors.Is(err, ErrGone) {
				t.Fatalf("native D became citable: %v", err)
			}
			assertLinkPatchSaved(t, ctx, s, "links/first", initial, metadata.Link, current.Link, restored.Link, recovered.Link)
			second, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/first", ExpectedRevision: recovered.Link.Revision, ExpectedSourceRevision: recovered.Source.(IssueRecord).Revision, Actor: "delete-again"})
			if err != nil || second.Link.Attribution.Inception != "revert" {
				t.Fatalf("native deletion lost protected inception: %+v %v", second, err)
			}
			again, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: initial.Revision, ExpectedRevision: second.Link.Revision, ExpectedSourceRevision: second.Source.(IssueRecord).Revision, Actor: "recover-again"})
			if err != nil || again.Link.ID != initial.ID || string(again.Link.Metadata) != `{}` || again.Link.Attribution.Inception != "revert" {
				t.Fatalf("repeat native recovery: %+v %v", again, err)
			}
		})
	}
}

func TestHistoricalNativeLinkMissingEndpoint(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			f := setupDependencyUnlink(t, engine, 1)
			s, ctx := f.store, f.ctx
			deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/first", ExpectedRevision: f.links[0].Revision, ExpectedSourceRevision: f.source.Revision, Actor: "unlink"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/first", ExpectedRevision: f.targets[0].Revision, Actor: "delete-target"}); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: f.links[0].Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(IssueRecord).Revision, Actor: "recover"})
			if !errors.Is(err, ErrGone) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("native endpoint revival/partial recovery: %v", err)
			}
		})
	}
}

func TestHistoricalNativeLinkRefusalsAndOccupiedPair(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			f := setupDependencyUnlink(t, engine, 2)
			s, ctx := f.store, f.ctx
			for _, tc := range []struct {
				name    string
				request LinkUpdateRequest
				want    error
			}{
				{"unknown", LinkUpdateRequest{FromRevision: strings.Repeat("f", 32)}, ErrVersionUnknown},
				{"foreign", LinkUpdateRequest{FromRevision: f.links[1].Revision}, ErrVersionUnknown},
				{"stale-link", LinkUpdateRequest{FromRevision: f.links[0].Revision, ExpectedRevision: strings.Repeat("f", 32)}, ErrConflict},
				{"stale-source", LinkUpdateRequest{FromRevision: f.links[0].Revision, ExpectedSourceRevision: strings.Repeat("f", 32)}, ErrConflict},
				{"pin", LinkUpdateRequest{FromRevision: f.links[0].Revision, TargetPin: &EndpointPin{Current: true}}, storage.ErrValidation},
				{"invalid-kind", LinkUpdateRequest{FromRevision: f.links[0].Revision, Properties: map[string]any{"dep_type": 7}}, storage.ErrValidation},
			} {
				r := tc.request
				r.Path = "links/first"
				r.Actor = "refused"
				if r.ExpectedRevision == "" {
					r.ExpectedRevision = f.links[0].Revision
				}
				if r.ExpectedSourceRevision == "" {
					r.ExpectedSourceRevision = f.source.Revision
				}
				before := workflowState(t, ctx, s)
				_, err := s.UpdateLink(ctx, r)
				if !errors.Is(err, tc.want) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("%s refusal: %v want %v", tc.name, err, tc.want)
				}
			}
			deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/first", ExpectedRevision: f.links[0].Revision, ExpectedSourceRevision: f.source.Revision, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			reasserted, err := s.AddDependency(ctx, DependencyRequest{Path: "links/reasserted", SourcePath: "beads/source", TargetPath: "beads/first", ExpectedSourceRevision: deleted.Source.(IssueRecord).Revision, Actor: "new-assertion"})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: f.links[0].Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: reasserted.Source.Revision, Actor: "cannot-adopt"})
			if !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("occupied pair recovery: %v", err)
			}
			before = workflowState(t, ctx, s)
			_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: deleted.Link.Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: reasserted.Source.Revision, Actor: "cannot-use-D"})
			if !errors.Is(err, ErrGone) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("deleted source recovery: %v", err)
			}
		})
	}
}

func TestHistoricalNativeLinkRollbackAndCycle(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			f := setupDependencyUnlink(t, engine, 1)
			s, ctx := f.store, f.ctx
			liveBefore := workflowState(t, ctx, s)
			liveFailure := errors.New("live native edit rollback")
			for _, stage := range []string{"coordination", "dependency", "link-catalog", "link-retained", "source-catalog", "source-retained"} {
				s.afterWrite = func(at string) error {
					if at == stage {
						return liveFailure
					}
					return nil
				}
				_, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: f.links[0].Revision, Metadata: nativeMetadata(`{"probe":true}`), ExpectedRevision: f.links[0].Revision, ExpectedSourceRevision: f.source.Revision, Actor: "live-edit"})
				s.afterWrite = nil
				if !errors.Is(err, liveFailure) || !reflect.DeepEqual(liveBefore, workflowState(t, ctx, s)) {
					t.Fatalf("%s partial live native update: %v", stage, err)
				}
			}
			deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/first", ExpectedRevision: f.links[0].Revision, ExpectedSourceRevision: f.source.Revision, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			injected := errors.New("native historical rollback")
			for _, stage := range []string{"coordination", "dependency", "link-catalog", "link-retained", "source-catalog", "source-retained"} {
				s.afterWrite = func(at string) error {
					if at == stage {
						return injected
					}
					return nil
				}
				_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: f.links[0].Revision, Metadata: nativeMetadata(`{"recovered":true}`), ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(IssueRecord).Revision, Actor: "recover"})
				s.afterWrite = nil
				if !errors.Is(err, injected) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("%s partial native recovery: %v", stage, err)
				}
			}
			// A reverse blocking edge is admissible while the original is deleted.
			// Recovering the original concrete blocks Type must still detect its cycle.
			if _, err = s.AddDependency(ctx, DependencyRequest{Path: "links/reverse", SourcePath: "beads/first", TargetPath: "beads/source", ExpectedSourceRevision: f.targets[0].Revision, Actor: "reverse"}); err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: f.links[0].Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(IssueRecord).Revision, Actor: "cannot-cycle"})
			if !errors.Is(err, publicops.ErrDependencyCycle) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("historical native cycle admitted or changed state: %v", err)
			}
		})
	}
}

func TestNativeLinkInitialProjectionAuthority(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			f := setupDependencyUnlink(t, engine, 1)
			s, ctx := f.store, f.ctx
			if _, err := s.db.ExecContext(ctx, `UPDATE dependencies SET metadata=NULL WHERE issue_id=? AND depends_on_issue_id=?`, f.source.Properties.ID, f.targets[0].Properties.ID); err != nil {
				t.Fatal(err)
			}
			link, err := s.ShowLink(ctx, "links/first")
			if err != nil || !reflect.DeepEqual(link, f.links[0]) {
				t.Fatalf("nullable native metadata projection: %+v %v", link, err)
			}
			if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_versions WHERE path=? AND version=?`, "links/first", f.links[0].Revision); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			err = s.withTx(ctx, false, func(tx *sql.Tx) error { _, err := s.currentLinkInTx(ctx, tx, "links/first"); return err })
			if !errors.Is(err, ErrInvalidStore) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("ordinary read used creation fallback: %v", err)
			}
		})
	}
}

func TestHistoricalNativeLinkAttributionAuthority(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			f := setupDependencyUnlink(t, engine, 1)
			s, ctx := f.store, f.ctx
			updated, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", MetadataOnly: true, Metadata: nativeMetadata(`{"key":"value"}`), ExpectedRevision: f.links[0].Revision, ExpectedSourceRevision: f.source.Revision, Actor: "updater"})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				value any
				want  error
			}{{nil, ErrInvalidStore}, {[]byte(`{}`), ErrInvalidStore}, {[]byte(strings.Repeat("x", PreviewCurrentReadByteLimit+1)), ErrLimitExceeded}} {
				if _, err = s.db.ExecContext(ctx, `UPDATE graph_preview_versions SET native_attribution=? WHERE path=? AND version=?`, tc.value, "links/first", updated.Link.Revision); err != nil {
					t.Fatal(err)
				}
				if _, err = s.ShowLink(ctx, "links/first"); !errors.Is(err, tc.want) {
					t.Fatalf("bad native authority accepted: %v want %v", err, tc.want)
				}
				if _, err = s.ExportCurrentSnapshot(ctx); !errors.Is(err, tc.want) {
					t.Fatalf("export hid invalid native authority: %v want %v", err, tc.want)
				}
			}
		})
	}
}

func TestHistoricalNativeLinkMetadataReadiness(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			f := setupHistoricalNativeKind(t, engine, "waits-for")
			s, ctx := f.store, f.ctx
			waiter := LinkMutationResult{Link: f.links[0], Source: f.source}
			assertDependencyReadiness(t, ctx, s, waiter.Source.(IssueRecord), true)
			for _, path := range []string{"beads/open-child", "beads/closed-child"} {
				r := plainIssue(path)
				if path == "beads/closed-child" {
					r.Issue.Status = "closed"
				}
				child, err := s.CreateIssue(ctx, path, r)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.AddDependency(ctx, DependencyRequest{SourcePath: path, TargetPath: "beads/first", Type: "parent-child", ExpectedSourceRevision: child.Revision, Actor: "parent"}); err != nil {
					t.Fatal(err)
				}
			}
			assertDependencyReadiness(t, ctx, s, waiter.Source.(IssueRecord), false)
			anyChild, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", MetadataOnly: true, Metadata: nativeMetadata(`{"gate":"any-children"}`), ExpectedRevision: waiter.Link.Revision, ExpectedSourceRevision: waiter.Source.(IssueRecord).Revision, Actor: "gate"})
			if err != nil {
				t.Fatal(err)
			}
			assertDependencyReadiness(t, ctx, s, anyChild.Source.(IssueRecord), true)
			blocked, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", MetadataOnly: true, Metadata: publicops.MetadataPatch{Set: map[string]json.RawMessage{"also_blocks": json.RawMessage(`true`)}}, ExpectedRevision: anyChild.Link.Revision, ExpectedSourceRevision: anyChild.Source.(IssueRecord).Revision, Actor: "also-blocks"})
			if err != nil {
				t.Fatal(err)
			}
			assertDependencyReadiness(t, ctx, s, blocked.Source.(IssueRecord), false)
			restored, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: anyChild.Link.Revision, ExpectedRevision: blocked.Link.Revision, ExpectedSourceRevision: blocked.Source.(IssueRecord).Revision, Actor: "restore-gate"})
			if err != nil {
				t.Fatal(err)
			}
			assertDependencyReadiness(t, ctx, s, restored.Source.(IssueRecord), true)
			exported, err := s.ExportCurrentSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, record := range exported.Snapshot.Records {
				if link, ok := record.(LinkRecord); ok && link.ID == restored.Link.ID {
					found = true
					if !reflect.DeepEqual(link, restored.Link) {
						t.Fatalf("export lost metadata/attribution: %+v", link)
					}
				}
			}
			if !found {
				t.Fatal("export omitted native Link")
			}
		})
	}
}

func TestHistoricalNativeLinkHierarchyTransitions(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			t.Run("parent-child-history", func(t *testing.T) {
				f := setupHistoricalNativeKind(t, engine, "parent-child")
				s, ctx := f.store, f.ctx
				current, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", MetadataOnly: true, Metadata: nativeMetadata(`{"current":true}`), ExpectedRevision: f.links[0].Revision, ExpectedSourceRevision: f.source.Revision, Actor: "parent-metadata"})
				if err != nil {
					t.Fatal(err)
				}
				restored, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: f.links[0].Revision, ExpectedRevision: current.Link.Revision, ExpectedSourceRevision: current.Source.(IssueRecord).Revision, Actor: "restore-parent-metadata"})
				if err != nil || !restored.Changed || restored.Link.Type != f.links[0].Type || restored.Link.Properties["dep_type"] != "parent-child" || string(restored.Link.Metadata) != `{}` {
					t.Fatalf("parent-child history changed concrete Type: %+v %v", restored, err)
				}
				assertDependencyOwned(t, restored.Source.(IssueRecord), restored.Link)
			})
			t.Run("blocking-ancestor-recovery", func(t *testing.T) {
				f := setupDependencyUnlink(t, engine, 1)
				s, ctx := f.store, f.ctx
				deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/first", ExpectedRevision: f.links[0].Revision, ExpectedSourceRevision: f.source.Revision, Actor: "delete"})
				if err != nil {
					t.Fatal(err)
				}
				indirect, err := s.AddDependency(ctx, DependencyRequest{SourcePath: "beads/source", TargetPath: "beads/second", Type: "parent-child", ExpectedSourceRevision: deleted.Source.(IssueRecord).Revision, Actor: "parent"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.AddDependency(ctx, DependencyRequest{SourcePath: "beads/second", TargetPath: "beads/first", Type: "parent-child", ExpectedSourceRevision: f.targets[1].Revision, Actor: "ancestor"}); err != nil {
					t.Fatal(err)
				}
				before := workflowState(t, ctx, s)
				_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/first", FromRevision: f.links[0].Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: indirect.Source.Revision, Actor: "cannot-block-ancestor"})
				var hierarchy *publicops.DependencyHierarchyConflictError
				if !errors.As(err, &hierarchy) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("native hierarchy refusal changed state: %v", err)
				}
			})
		})
	}
}

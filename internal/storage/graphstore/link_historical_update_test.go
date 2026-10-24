//go:build cgo

package graphstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	publicops "github.com/steveyegge/beads/issueops"
)

func TestHistoricalLinkUpdateComposition(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s, owner, _, initial := disclosureFixture(t, engine)
			first, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", Properties: map[string]any{"note": "first"}, Metadata: publicops.MetadataPatch{Replace: publicops.Field[json.RawMessage]{Set: true, Value: json.RawMessage(`{"old":true}`)}}, ExpectedRevision: initial.Link.Revision, ExpectedSourceRevision: owner.Revision})
			if err != nil {
				t.Fatal(err)
			}
			last, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", Properties: map[string]any{"note": "last"}, Metadata: publicops.MetadataPatch{Replace: publicops.Field[json.RawMessage]{Set: true, Value: json.RawMessage(`{"last":true}`)}}, ExpectedRevision: first.Link.Revision, ExpectedSourceRevision: first.Source.(Record).Revision})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.PatchLinkProperties(ctx, LinkPropertiesPatchRequest{Path: "links/out", FromRevision: first.Link.Revision, Patch: linkNotePatch(t, "explicit"), Metadata: publicops.MetadataPatch{Set: map[string]json.RawMessage{"explicit": json.RawMessage(`7`)}}, ExpectedRevision: last.Link.Revision, ExpectedSourceRevision: last.Source.(Record).Revision, Actor: "historical"})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Changed || result.Link.ID != first.Link.ID || result.Link.Type != last.Link.Type || result.Link.Source != last.Link.Source || result.Link.Target != last.Link.Target || result.Link.Properties["note"] != "explicit" || string(result.Link.Metadata) != `{"explicit":7,"old":true}` {
				t.Fatalf("wrong composition: %+v", result)
			}
			if h := result.Link.Attribution.HistoricalUpdate; h == nil || h.FromRevision != first.Link.Revision || h.PreviousRevision != last.Link.Revision {
				t.Fatalf("missing context: %+v", result.Link)
			}
			assertLinkPatchSaved(t, ctx, s, "links/out", first.Link, last.Link, result.Link)
			metadataOnly, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", MetadataOnly: true, Metadata: publicops.MetadataPatch{Replace: publicops.Field[json.RawMessage]{Set: true, Value: json.RawMessage(`{"different":true}`)}}, ExpectedRevision: result.Link.Revision, ExpectedSourceRevision: result.Source.(Record).Revision})
			if err != nil {
				t.Fatal(err)
			}
			restoredMetadata, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: result.Link.Revision, ExpectedRevision: metadataOnly.Link.Revision, ExpectedSourceRevision: metadataOnly.Source.(Record).Revision})
			if err != nil || !restoredMetadata.Changed || !reflect.DeepEqual(restoredMetadata.Link.Properties, metadataOnly.Link.Properties) || string(restoredMetadata.Link.Metadata) != string(result.Link.Metadata) {
				t.Fatalf("metadata-only source restore was lost as a no-op: %+v %v", restoredMetadata, err)
			}
			assertLinkPatchSaved(t, ctx, s, "links/out", result.Link, metadataOnly.Link, restoredMetadata.Link)
			result = restoredMetadata
			before := workflowState(t, ctx, s)
			noop, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: result.Link.Revision, ExpectedRevision: result.Link.Revision, ExpectedSourceRevision: result.Source.(Record).Revision, Actor: "different"})
			if err != nil || noop.Changed || !reflect.DeepEqual(noop.Link, result.Link) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("no-op changed envelope: %+v %v", noop, err)
			}
		})
	}
}

func TestHistoricalLinkRecoveryAndTerminalHistory(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s, owner, _, initial := disclosureFixture(t, engine)
			deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/out", ExpectedRevision: initial.Link.Revision, ExpectedSourceRevision: owner.Revision, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			history, err := s.History(ctx, "links/out")
			if err != nil || history.CurrentState == nil || history.CurrentState.Revision != deleted.Link.Revision || history.CurrentState.Type != initial.Link.Type {
				t.Fatalf("missing Link D: %+v %v", history, err)
			}
			recovered, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: initial.Link.Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(Record).Revision, Actor: "recover"})
			if err != nil {
				t.Fatal(err)
			}
			if !recovered.Changed || recovered.Link.ID != initial.Link.ID || recovered.Link.Revision == initial.Link.Revision || recovered.Link.Revision == deleted.Link.Revision || recovered.Link.Attribution.Inception != "revert" || len(recovered.Source.(Record).Owned) != 1 {
				t.Fatalf("wrong recovery: %+v", recovered)
			}
			assertLinkPatchSaved(t, ctx, s, "links/out", initial.Link, recovered.Link)
			if _, err := s.ReadVersion(ctx, "links/out", deleted.Link.Revision); !errors.Is(err, ErrGone) {
				t.Fatalf("old D became citable: %v", err)
			}
			second, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/out", ExpectedRevision: recovered.Link.Revision, ExpectedSourceRevision: recovered.Source.(Record).Revision, Actor: "delete again"})
			if err != nil {
				t.Fatal(err)
			}
			if second.Link.Attribution.Inception != recovered.Link.Attribution.Inception {
				t.Fatal("deletion dropped recovery inception")
			}
			again, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: initial.Link.Revision, ExpectedRevision: second.Link.Revision, ExpectedSourceRevision: second.Source.(Record).Revision, Actor: "recover again"})
			if err != nil || !again.Changed {
				t.Fatalf("repeat recovery: %+v %v", again, err)
			}
			history, err = s.History(ctx, "links/out")
			if err != nil || history.CurrentState != nil {
				t.Fatalf("live History: %+v %v", history, err)
			}
			removed := 0
			for _, row := range history.Versions {
				if row.Removed {
					removed++
				}
			}
			if removed != 2 {
				t.Fatalf("lost old terminals: %+v", history)
			}
		})
	}
}

func TestHistoricalLinkRefusalsPreserveAllState(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s, owner, _, initial := disclosureFixture(t, engine)
			foreign, err := s.Create(ctx, CreateRequest{Path: "beads/foreign", Title: "Foreign"})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				source, guard, ownerGuard string
				want                      error
			}{
				{"unknown", initial.Link.Revision, owner.Revision, ErrVersionUnknown},
				{foreign.Revision, initial.Link.Revision, owner.Revision, ErrVersionUnknown},
				{initial.Link.Revision, "stale", owner.Revision, ErrConflict},
				{initial.Link.Revision, initial.Link.Revision, "stale", ErrConflict},
			} {
				before := workflowState(t, ctx, s)
				_, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: tc.source, ExpectedRevision: tc.guard, ExpectedSourceRevision: tc.ownerGuard})
				if !errors.Is(err, tc.want) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("refusal: %v want %v", err, tc.want)
				}
			}
			deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/out", ExpectedRevision: initial.Link.Revision, ExpectedSourceRevision: owner.Revision})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: deleted.Link.Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(Record).Revision})
			if !errors.Is(err, ErrGone) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("deleted source accepted: %v", err)
			}
		})
	}
}

func TestHistoricalLinkPinsAndRecoveryRollback(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s, owner, target, initial := disclosureFixture(t, engine)
			first, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", Properties: map[string]any{"note": "old"}, TargetPin: &EndpointPin{Revision: target.Revision}, ExpectedRevision: initial.Link.Revision, ExpectedSourceRevision: owner.Revision})
			if err != nil {
				t.Fatal(err)
			}
			laterTarget, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: historicalBody("new target"), Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			last, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", Properties: map[string]any{"note": "new"}, TargetPin: &EndpointPin{Revision: laterTarget.Memory.Revision}, ExpectedRevision: first.Link.Revision, ExpectedSourceRevision: first.Source.(Record).Revision})
			if err != nil {
				t.Fatal(err)
			}
			restored, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: first.Link.Revision, ExpectedRevision: last.Link.Revision, ExpectedSourceRevision: last.Source.(Record).Revision})
			if err != nil || restored.Link.TargetRevision != laterTarget.Memory.Revision || restored.Link.Properties["note"] != "old" {
				t.Fatalf("historical content changed current pin: %+v %v", restored, err)
			}
			deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/out", ExpectedRevision: restored.Link.Revision, ExpectedSourceRevision: restored.Source.(Record).Revision})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			injected := errors.New("historical Link rollback")
			for _, stage := range []string{"coordination", "link-catalog", "link-payload", "link-retained", "source-catalog", "source-retained"} {
				s.afterWrite = func(at string) error {
					if at == stage {
						return injected
					}
					return nil
				}
				_, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: first.Link.Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(Record).Revision})
				s.afterWrite = nil
				if !errors.Is(err, injected) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("%s: partial recovery %v", stage, err)
				}
			}
			recovered, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: first.Link.Revision, TargetPin: &EndpointPin{}, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(Record).Revision})
			if err != nil || recovered.Link.TargetRevision != "" {
				t.Fatalf("explicit recovery unpin: %+v %v", recovered, err)
			}
		})
	}
}

func TestHistoricalLinkTerminalPrivacyBudgetAndMissingEndpoint(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s, owner, target, initial := disclosureFixture(t, engine)
			deleted, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/out", ExpectedRevision: initial.Link.Revision, ExpectedSourceRevision: owner.Revision})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				query string
				args  []any
				want  error
			}{
				{`UPDATE graph_preview_versions SET snapshot='{}' WHERE path=? AND version=?`, []any{"links/out", initial.Link.Revision}, ErrInvalidStore},
				{`UPDATE graph_preview_versions SET snapshot=REPEAT('x',?) WHERE path=? AND version=?`, []any{PreviewCurrentReadByteLimit + 1, "links/out", initial.Link.Revision}, ErrLimitExceeded},
			} {
				if _, err := s.db.ExecContext(ctx, tc.query, tc.args...); err != nil {
					t.Fatal(err)
				}
				history, err := s.History(ctx, "links/out")
				if err != nil || history.CurrentState == nil || history.CurrentState.Revision != deleted.Link.Revision {
					t.Fatalf("History hydrated private baseline: %+v %v", history, err)
				}
				before := workflowState(t, ctx, s)
				_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: initial.Link.Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(Record).Revision})
				if !errors.Is(err, tc.want) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("baseline refusal %v, want %v", err, tc.want)
				}
			}
			raw, err := canonicalJSON(initial.Link)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_versions SET snapshot=? WHERE path=? AND version=?`, raw, "links/out", initial.Link.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/target", ExpectedRevision: target.Revision}); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", FromRevision: initial.Link.Revision, ExpectedRevision: deleted.Link.Revision, ExpectedSourceRevision: deleted.Source.(Record).Revision})
			if !errors.Is(err, ErrGone) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("missing endpoint recovery: %v", err)
			}
		})
	}
}

func TestHistoricalLinkCumulativeCurrentSourceAndPinBudget(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			fixture, _, s, owner, _, initial := disclosureFixture(t, engine)
			// Several multi-MiB retained snapshots exercise the cumulative byte
			// budget. Coverage and race instrumentation need more than the shared
			// fixture's 90s; retain a bounded test context without changing limits.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(fixture), 5*time.Minute)
			t.Cleanup(cancel)
			first, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", Properties: map[string]any{"note": strings.Repeat("h", 2<<20)}, ExpectedRevision: initial.Link.Revision, ExpectedSourceRevision: owner.Revision})
			if err != nil {
				t.Fatal(err)
			}
			last, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", Properties: map[string]any{"note": "small"}, ExpectedRevision: first.Link.Revision, ExpectedSourceRevision: first.Source.(Record).Revision})
			if err != nil {
				t.Fatal(err)
			}
			oldTarget, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: historicalBody(strings.Repeat("p", 5<<20)), Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: historicalBody("small"), Unconditional: true}); err != nil {
				t.Fatal(err)
			}
			currentOwner, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/plan", Body: historicalBody(strings.Repeat("c", 5<<20)), Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			// Every individual exact source is admitted. The current working set
			// plus Link source is also admitted, and explicit edits collapse the
			// large source properties back to the complete unchanged live state.
			if _, err := s.ReadVersion(ctx, "links/out", first.Link.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReadVersion(ctx, "beads/target", oldTarget.Memory.Revision); err != nil {
				t.Fatal(err)
			}
			request := LinkUpdateRequest{Path: "links/out", FromRevision: first.Link.Revision, Properties: map[string]any{"note": "small"}, MergeProperties: true, ExpectedRevision: last.Link.Revision, ExpectedSourceRevision: currentOwner.Memory.Revision}
			before := workflowState(t, ctx, s)
			noop, err := s.UpdateLink(ctx, request)
			if err != nil || noop.Changed || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("current+source control should fit: %+v %v", noop, err)
			}
			request.TargetPin = &EndpointPin{Revision: oldTarget.Memory.Revision}
			_, err = s.UpdateLink(ctx, request)
			if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("combined current+source+pin must refuse atomically: %v", err)
			}
			// Ordinary pin writes keep their existing policy: the same current
			// set and pin fit without historical Link acquisition.
			ordinary, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/out", TargetPin: request.TargetPin, ExpectedRevision: last.Link.Revision, ExpectedSourceRevision: currentOwner.Memory.Revision})
			if err != nil || !ordinary.Changed || ordinary.Link.TargetRevision != oldTarget.Memory.Revision {
				t.Fatalf("ordinary pin policy changed: %+v %v", ordinary, err)
			}
		})
	}
}

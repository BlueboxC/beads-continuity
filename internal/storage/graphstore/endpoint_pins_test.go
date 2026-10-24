//go:build cgo

package graphstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
)

func TestEndpointPinsPersistAndRepinSameIdentity(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			source, err := s.Create(ctx, CreateRequest{Path: "beads/source", Title: "Source", Body: "Source"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := s.Create(ctx, CreateRequest{Path: "beads/target", Title: "Target", Body: "First"})
			if err != nil {
				t.Fatal(err)
			}
			created, err := s.AddInformationalLink(ctx, LinkCreateRequest{SourcePath: "beads/source", TargetPath: "beads/target", Path: "links/citation", SourcePin: EndpointPin{Current: true}, TargetPin: EndpointPin{Revision: target.Revision}, Properties: map[string]any{"note": "Keep"}})
			if err != nil {
				t.Fatal(err)
			}
			if created.Link.SourceRevision != source.Revision || created.Link.TargetRevision != target.Revision || created.Source.(Record).Revision != source.Revision {
				t.Fatalf("lost independent endpoint intent: %+v", created)
			}
			oldBytes, err := canonicalJSON(created.Link)
			if err != nil {
				t.Fatal(err)
			}
			secondBody := "Second"
			next, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: &secondBody, ExpectedRevision: target.Revision})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			if _, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/citation", TargetPin: &EndpointPin{Revision: "not-retained"}, ExpectedRevision: created.Link.Revision}); !errors.Is(err, ErrVersionUnknown) {
				t.Fatalf("missing exact pin fell back: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("missing pin changed state")
			}
			repinned, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/citation", TargetPin: &EndpointPin{Revision: next.Memory.Revision}, ExpectedRevision: created.Link.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if !repinned.Changed || repinned.Link.ID != created.Link.ID || repinned.Link.Type != created.Link.Type || repinned.Link.Revision == created.Link.Revision || repinned.Link.SourceRevision != source.Revision || repinned.Link.TargetRevision != next.Memory.Revision || !reflect.DeepEqual(repinned.Link.Properties, created.Link.Properties) {
				t.Fatalf("repin changed identity/payload: %+v", repinned)
			}
			retained, err := s.ReadVersion(ctx, "links/citation", created.Link.Revision)
			if err != nil {
				t.Fatal(err)
			}
			retainedBytes, _ := canonicalJSON(retained)
			if string(retainedBytes) != string(oldBytes) {
				t.Fatal("repin rewrote old Link bytes")
			}
			comparison, err := s.CompareVersions(ctx, "links/citation", created.Link.Revision, repinned.Link.Revision)
			if err != nil || len(comparison.Changes) != 1 || comparison.Changes[0].Area != "endpoints" || comparison.Changes[0].Member != "target" {
				t.Fatalf("retained pin comparison lost change: %+v %v", comparison, err)
			}
			noOp, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/citation", TargetPin: &EndpointPin{Current: true}, ExpectedRevision: repinned.Link.Revision})
			if err != nil || noOp.Changed || !reflect.DeepEqual(noOp.Link, repinned.Link) {
				t.Fatalf("same full Reference was not no-op: %+v %v", noOp, err)
			}
			if _, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/citation", TargetPin: &EndpointPin{Revision: next.Memory.Revision}, ExpectedRevision: created.Link.Revision}); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale no-op guard bypass: %v", err)
			}
			if _, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/citation", TargetPin: &EndpointPin{Revision: next.Memory.Revision}, ExpectedRevision: repinned.Link.Revision, ExpectedSourceRevision: "stale"}); !errors.Is(err, ErrConflict) {
				t.Fatalf("source guard conflated with endpoint pin: %v", err)
			}
			unpinned, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/citation", TargetPin: &EndpointPin{}, ExpectedRevision: repinned.Link.Revision})
			if err != nil || !unpinned.Changed || unpinned.Link.TargetRevision != "" || unpinned.Link.SourceRevision != source.Revision {
				t.Fatalf("explicit unpin lost intent: %+v %v", unpinned, err)
			}
			// Reopen reads the actual persisted reference columns, not an in-memory DTO.
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.ShowLink(ctx, "links/citation")
			if err != nil || !reflect.DeepEqual(current, unpinned.Link) {
				t.Fatalf("reopen lost references: %+v %v", current, err)
			}
			links, err := s.ListLinks(ctx, LinksRequest{BeadPath: "beads/source"})
			if err != nil || len(links) != 1 || links[0].ID != created.Link.ID {
				t.Fatalf("identity incident traversal changed with pins: %+v %v", links, err)
			}
		})
	}
}

func TestOwnedTargetRepinRetainsOwnerAndRollsBack(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			}()
			source, err := s.Create(ctx, CreateRequest{Path: "beads/source", Title: "Source", Body: "Owner"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := s.Create(ctx, CreateRequest{Path: "beads/target", TypeURL: s.ScopeURL() + "types/constraint", Title: "Target", Body: "Constraint1"})
			if err != nil {
				t.Fatal(err)
			}
			created, err := s.AddInformationalLink(ctx, LinkCreateRequest{TypeURL: s.ScopeURL() + "types/subject-to", SourcePath: "beads/source", TargetPath: "beads/target", Path: "links/obligation", TargetPin: EndpointPin{Revision: target.Revision}, ExpectedSourceRevision: source.Revision})
			if err != nil {
				t.Fatal(err)
			}
			owner := created.Source.(Record)
			oldOwnerBytes, _ := canonicalJSON(owner)
			body := "Constraint2"
			target2, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: &body, ExpectedRevision: target.Revision})
			if err != nil {
				t.Fatal(err)
			}
			request := LinkUpdateRequest{Path: "links/obligation", TargetPin: &EndpointPin{Revision: target2.Memory.Revision}, ExpectedRevision: created.Link.Revision, ExpectedSourceRevision: owner.Revision}
			before := workflowState(t, ctx, s)
			for _, stage := range []string{"coordination", "link-catalog", "link-payload", "link-retained", "source-catalog", "source-retained"} {
				t.Run(stage, func(t *testing.T) {
					fault := errors.New("pin failure")
					s.afterWrite = func(at string) error {
						if at == stage {
							return fault
						}
						return nil
					}
					_, err := s.UpdateLink(ctx, request)
					s.afterWrite = nil
					if !errors.Is(err, fault) {
						t.Fatalf("stage %s: %v", stage, err)
					}
					if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatalf("partial pin/owner commit at %s", stage)
					}
				})
			}
			updated, err := s.UpdateLink(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			newOwner := updated.Source.(Record)
			if !updated.Changed || updated.Link.ID != created.Link.ID || newOwner.Revision == owner.Revision || len(newOwner.Owned) != 1 {
				t.Fatalf("owner not atomically revised: %+v", updated)
			}
			var owned LinkRecord
			if err := json.Unmarshal(newOwner.Owned[0], &owned); err != nil || owned.TargetRevision != target2.Memory.Revision || owned.SourceRevision != "" {
				t.Fatalf("owned pin lost: %+v %v", owned, err)
			}
			oldOwner, err := s.ReadVersion(ctx, "beads/source", owner.Revision)
			if err != nil {
				t.Fatal(err)
			}
			bytes, _ := canonicalJSON(oldOwner)
			if string(bytes) != string(oldOwnerBytes) {
				t.Fatal("owner retained snapshot rewritten")
			}
			comparison, err := s.CompareVersions(ctx, "beads/source", owner.Revision, newOwner.Revision)
			if err != nil || len(comparison.Changes) != 1 || comparison.Changes[0].Area != "owned" || comparison.Changes[0].ID != created.Link.ID {
				t.Fatalf("owned retained pin comparison failed: %+v %v", comparison, err)
			}
			same, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/obligation", TargetPin: &EndpointPin{Revision: target2.Memory.Revision}, ExpectedRevision: updated.Link.Revision, ExpectedSourceRevision: newOwner.Revision})
			if err != nil || same.Changed || !reflect.DeepEqual(same.Source, newOwner) {
				t.Fatalf("pin no-op minted owner: %+v %v", same, err)
			}
			before = workflowState(t, ctx, s)
			_, err = s.AddInformationalLink(ctx, LinkCreateRequest{TypeURL: s.ScopeURL() + "types/superseded-by", SourcePath: "beads/source", TargetPath: "beads/target", Path: "links/forbidden-pin", TargetPin: EndpointPin{Revision: target2.Memory.Revision}, ExpectedSourceRevision: newOwner.Revision})
			if !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("pin bypassed selected floating-only rule or rollback: %v", err)
			}
		})
	}
}

func TestPinnedEndpointValidationBudgetChargesHistoricalPayloads(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			ctx = currentReadBudgetContext(t, ctx)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			}()
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/source", Title: "Source", Body: "Small"}); err != nil {
				t.Fatal(err)
			}
			old, err := s.Create(ctx, CreateRequest{Path: "beads/target", Title: "Target", Body: strings.Repeat("x", 3<<20)})
			if err != nil {
				t.Fatal(err)
			}
			small := "Small current body"
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: &small, ExpectedRevision: old.Revision}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				_, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/" + string(rune('a'+i)), SourcePath: "beads/source", TargetPath: "beads/target", TargetPin: EndpointPin{Revision: old.Revision}})
				if err != nil {
					t.Fatalf("bounded history population %d: %v", i, err)
				}
			}
			before := workflowState(t, ctx, s)
			_, err = s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/overflow", SourcePath: "beads/source", TargetPath: "beads/target", TargetPin: EndpointPin{Revision: old.Revision}})
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("unbudgeted historical endpoint population: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("historical budget refusal partially committed")
			}
		})
	}
}

func TestOwnedSourcePinsRefuseBeforeAndAfterRetyping(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			subject, err := s.Create(ctx, CreateRequest{Path: "beads/build", Title: "Build"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/rule", TypeURL: s.ScopeURL() + "types/constraint", Title: "Rule"}); err != nil {
				t.Fatal(err)
			}
			properties, err := json.Marshal(map[string]any{"title": "Assessment", "body": "", "subjects": []any{map[string]string{"uri": subject.ID, "revision": subject.Revision}}})
			if err != nil {
				t.Fatal(err)
			}
			evaluation, err := s.Create(ctx, CreateRequest{Path: "beads/evaluation", TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: properties})
			if err != nil {
				t.Fatal(err)
			}
			request := LinkCreateRequest{Path: "links/claim", TypeURL: s.ScopeURL() + "types/satisfies", SourcePath: "beads/evaluation", TargetPath: "beads/rule", SourcePin: EndpointPin{Revision: evaluation.Revision}, ExpectedSourceRevision: evaluation.Revision}
			assertRefusal := func() {
				t.Helper()
				before := workflowState(t, ctx, s)
				if _, err := s.AddInformationalLink(ctx, request); !errors.Is(err, ErrCapabilityUnavailable) {
					t.Fatalf("owned source pin admitted: %v", err)
				}
				if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatal("owned source pin refusal partially committed")
				}
			}
			assertRefusal()
			plain, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/evaluation", TypeURL: s.ScopeURL() + "types/memory", ExpectedRevision: evaluation.Revision})
			if err != nil {
				t.Fatal(err)
			}
			request.ExpectedSourceRevision = plain.Memory.Revision
			assertRefusal()
		})
	}
}

func TestEndpointPinsConcurrentOwnedRepin(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			first, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := first.Close(); err != nil {
					t.Error(err)
				}
			})
			source, err := first.Create(ctx, CreateRequest{Path: "beads/source", Title: "Source"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := first.Create(ctx, CreateRequest{Path: "beads/target", Title: "Rule", TypeURL: first.ScopeURL() + "types/constraint"})
			if err != nil {
				t.Fatal(err)
			}
			links := []LinkRecord{}
			for _, path := range []string{"links/one", "links/two"} {
				added, err := first.AddInformationalLink(ctx, LinkCreateRequest{Path: path, SourcePath: "beads/source", TargetPath: "beads/target", TypeURL: first.ScopeURL() + "types/subject-to", TargetPin: EndpointPin{Revision: target.Revision}, ExpectedSourceRevision: source.Revision})
				if err != nil {
					t.Fatal(err)
				}
				source = added.Source.(Record)
				links = append(links, added.Link)
			}
			body := "Next rule"
			targetNext, err := first.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: &body, ExpectedRevision: target.Revision})
			if err != nil {
				t.Fatal(err)
			}
			second := &Store{db: first.db, options: options}
			if backend == "server" {
				second, err = OpenExisting(ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := second.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			reached, release := make(chan struct{}, 2), make(chan struct{})
			pause := func(stage string) error {
				if backend != "server" || stage != "source-retained" {
					return nil
				}
				reached <- struct{}{}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			first.afterWrite, second.afterWrite = pause, pause
			results := make(chan error, 2)
			var writers sync.WaitGroup
			defer func() { cancel(); writers.Wait() }()
			for i, store := range []*Store{first, second} {
				writers.Add(1)
				go func() {
					defer writers.Done()
					_, err := store.UpdateLink(ctx, LinkUpdateRequest{Path: []string{"links/one", "links/two"}[i], ExpectedRevision: links[i].Revision, ExpectedSourceRevision: source.Revision, TargetPin: &EndpointPin{Current: true}})
					results <- err
				}()
			}
			if backend == "server" {
				for range 2 {
					select {
					case <-reached:
					case err := <-results:
						t.Fatalf("writer before overlap: %v", err)
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
				}
				close(release)
			}
			won, conflicted := 0, 0
			for range 2 {
				select {
				case err := <-results:
					if err == nil {
						won++
					} else if errors.Is(err, ErrConflict) {
						conflicted++
					} else {
						t.Fatalf("unexpected race result: %v", err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			writers.Wait()
			first.afterWrite, second.afterWrite = nil, nil
			if won != 1 || conflicted != 1 {
				t.Fatalf("repin guard race: %d wins %d conflicts", won, conflicted)
			}
			owner, err := first.Show(ctx, "beads/source")
			if err != nil || len(owner.Owned) != 2 || owner.Revision == source.Revision {
				t.Fatalf("owner state: %+v %v", owner, err)
			}
			newPins := 0
			for _, raw := range owner.Owned {
				var owned LinkRecord
				if err := json.Unmarshal(raw, &owned); err != nil {
					t.Fatal(err)
				}
				current, err := first.ShowLink(ctx, strings.TrimPrefix(owned.ID, first.ScopeURL()))
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := canonicalJSON(current)
				if err != nil || !bytes.Equal(raw, encoded) {
					t.Fatalf("split standalone/owner state: %v", err)
				}
				if current.TargetRevision == targetNext.Memory.Revision {
					newPins++
				} else if current.TargetRevision != target.Revision {
					t.Fatal("unexpected endpoint revision")
				}
			}
			if newPins != 1 {
				t.Fatalf("race committed %d re-pins", newPins)
			}
		})
	}
}

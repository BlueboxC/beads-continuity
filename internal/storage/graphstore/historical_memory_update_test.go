//go:build cgo

package graphstore

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	publicops "github.com/steveyegge/beads/issueops"
)

func TestHistoricalMemoryUpdateComposition(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			original, err := s.Create(ctx, CreateRequest{Path: "beads/subject", Title: "Original", Body: "R1", Actor: "original", Metadata: json.RawMessage(`{"old":null,"shared":"old"}`)})
			if err != nil {
				t.Fatal(err)
			}
			later, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/subject", Body: historicalBody("R2"), Unconditional: true, Actor: "later", Metadata: publicops.MetadataPatch{Set: map[string]json.RawMessage{"new": json.RawMessage(`true`), "shared": json.RawMessage(`"new"`)}, Unset: []string{"old"}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/target", Title: "Target", Body: "target"}); err != nil {
				t.Fatal(err)
			}
			link, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/owned", TypeURL: s.ScopeURL() + "types/superseded-by", SourcePath: "beads/subject", TargetPath: "beads/target", ExpectedSourceRevision: later.Memory.Revision})
			if err != nil {
				t.Fatal(err)
			}
			current := link.Source.(Record)
			if len(current.Owned) != 1 {
				t.Fatal("fixture must exercise owned membership")
			}
			result, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/subject", FromRevision: original.Revision, ExpectedRevision: current.Revision, Actor: "historical-editor", Metadata: publicops.MetadataPatch{Set: map[string]json.RawMessage{"explicit": json.RawMessage(`7`)}}})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Changed || result.Memory.Revision == current.Revision || result.Memory.Properties != original.Properties || !reflect.DeepEqual(result.Memory.Owned, current.Owned) {
				t.Fatalf("wrong composed result: %+v", result)
			}
			if string(result.Memory.Metadata) != `{"explicit":7,"old":null,"shared":"old"}` {
				t.Fatalf("wrong metadata: %s", result.Memory.Metadata)
			}
			h := result.Memory.Attribution.HistoricalUpdate
			if h == nil || h.FromRevision != original.Revision || h.PreviousRevision != current.Revision || result.Memory.Attribution.Actor != "historical-editor" {
				t.Fatalf("wrong context: %+v", result.Memory.Attribution)
			}
			for _, record := range []Record{original, current, result.Memory} {
				got, err := s.ReadVersion(ctx, "beads/subject", record.Revision)
				if err != nil || !reflect.DeepEqual(got, record) {
					t.Fatalf("retained changed: %+v %v", got, err)
				}
			}
			// Source-only and ordinary no-ops retain an already historical envelope.
			before := workflowState(t, ctx, s)
			for _, request := range []MemoryPatchRequest{{Path: "beads/subject", FromRevision: result.Memory.Revision, ExpectedRevision: result.Memory.Revision, Actor: "different"}, {Path: "beads/subject", PropertiesProvided: true, ExpectedRevision: result.Memory.Revision, Actor: "different"}} {
				noop, err := s.PatchMemory(ctx, request)
				if err != nil || noop.Changed || !reflect.DeepEqual(noop.Memory, result.Memory) {
					t.Fatalf("no-op rewrote context: %+v %v", noop, err)
				}
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("no-op changed store")
			}
			// Explicit edits apply to source, but equality is against the real current head.
			body := "R1"
			equal, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/subject", FromRevision: later.Memory.Revision, Body: &body, ExpectedRevision: result.Memory.Revision, Metadata: publicops.MetadataPatch{Unset: []string{"new"}, Set: map[string]json.RawMessage{"old": json.RawMessage(`null`), "shared": json.RawMessage(`"old"`), "explicit": json.RawMessage(`7`)}}})
			if err != nil || equal.Changed || !reflect.DeepEqual(equal.Memory, result.Memory) {
				t.Fatalf("patch-to-current should no-op: %+v %v", equal, err)
			}
		})
	}
}

func historicalBody(value string) *string { return &value }

func TestHistoricalMemoryUpdateRefusalsAndRollback(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			first, err := s.Create(ctx, CreateRequest{Path: "beads/subject", Title: "Subject", Body: "first"})
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/subject", Body: historicalBody("second"), Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			other, err := s.Create(ctx, CreateRequest{Path: "beads/other", Title: "Other", Body: "other"})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			for _, tc := range []struct {
				name, source, guard string
				want                error
			}{
				{"foreign revision", other.Revision, current.Memory.Revision, ErrVersionUnknown},
				{"unknown source", "unavailable-source", current.Memory.Revision, ErrVersionUnknown},
				{"stale guard", first.Revision, first.Revision, ErrConflict},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/subject", FromRevision: tc.source, ExpectedRevision: tc.guard})
					if !errors.Is(err, tc.want) {
						t.Fatalf("want %v got %v", tc.want, err)
					}
					if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatal("refusal wrote")
					}
				})
			}
			injected := errors.New("historical rollback")
			for _, stage := range []string{"coordination", "memory-payload", "source-catalog", "source-retained"} {
				t.Run(stage, func(t *testing.T) {
					s.afterWrite = func(at string) error {
						if at == stage {
							return injected
						}
						return nil
					}
					_, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/subject", FromRevision: first.Revision, Unconditional: true})
					s.afterWrite = nil
					if !errors.Is(err, injected) {
						t.Fatalf("missing failure: %v", err)
					}
					if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatal("partial rollback")
					}
				})
			}
		})
	}
}

func TestHistoricalMemoryUpdateCurrentTypeAndProtectedEvaluation(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := assessmentMemoryStore(t, engine)
			original, err := s.Create(ctx, CreateRequest{Path: "beads/evaluation", Title: "Before Evaluation", Body: "historical content"})
			if err != nil {
				t.Fatal(err)
			}
			subject, err := s.Create(ctx, CreateRequest{Path: "beads/subject", Title: "Assessed subject"})
			if err != nil {
				t.Fatal(err)
			}
			subjects, err := json.Marshal(map[string]any{"subjects": []any{map[string]string{"uri": subject.ID, "revision": subject.Revision}}, "body": "current content"})
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/evaluation", TypeURL: s.ScopeURL() + "types/evaluation", PropertiesMerge: subjects, Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/evaluation", FromRevision: original.Revision, PropertiesMerge: json.RawMessage(`{"title":"Edited historical title"}`), ExpectedRevision: current.Memory.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Changed || result.Memory.Type != current.Memory.Type || result.Memory.Properties.Title != "Edited historical title" || result.Memory.Properties.Body != original.Properties.Body {
				t.Fatalf("wrong final Type/content: %+v", result)
			}
			if err := preserveEvaluationSubjectGroup(current.Memory.Properties, result.Memory.Properties); err != nil {
				t.Fatalf("lost protected group: %v", err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/evaluation", FromRevision: original.Revision, PropertiesMerge: json.RawMessage(`{"subjects":[]}`), Unconditional: true})
			if err == nil || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("protected group edit accepted or wrote: %v", err)
			}
			_, err = s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/evaluation", FromRevision: original.Revision, TypeURL: original.Type, Unconditional: true})
			if err == nil || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("historical retype accepted or wrote: %v", err)
			}
		})
	}
}

func TestHistoricalMemoryRecoveryTerminalGuardAndRollback(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			first, err := s.Create(ctx, CreateRequest{Path: "beads/recover", Title: "Before", Body: "first", Actor: "creator"})
			if err != nil {
				t.Fatal(err)
			}
			last, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/recover", Body: historicalBody("last"), Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			previewBefore := workflowState(t, ctx, s)
			preview, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/recover", Preview: true})
			if err != nil || preview.Deleted || preview.Terminal != nil || !reflect.DeepEqual(previewBefore, workflowState(t, ctx, s)) {
				t.Fatalf("preview minted terminal: %+v %v", preview, err)
			}
			deleted, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/recover", Actor: "deleter", ExpectedRevision: last.Memory.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if deleted.Terminal == nil || deleted.Terminal.Revision == last.Memory.Revision || deleted.Terminal.PreviousVersion != last.Memory.Revision || deleted.Memory.Revision != last.Memory.Revision || deleted.Terminal.Attribution.Inception != first.Attribution.Inception {
				t.Fatalf("not a real separate terminal: %+v", deleted)
			}
			d := deleted.Terminal.Revision
			if _, err := s.Show(ctx, "beads/recover"); !errors.Is(err, ErrGone) {
				t.Fatalf("ordinary deleted read: %v", err)
			}
			if _, err := s.ReadVersion(ctx, "beads/recover", d); !errors.Is(err, ErrGone) {
				t.Fatalf("terminal became live source: %v", err)
			}
			history, err := s.History(ctx, "beads/recover")
			if err != nil || !reflect.DeepEqual(history.CurrentState, deleted.Terminal) || len(history.Versions) != 3 || !history.Versions[0].Removed {
				t.Fatalf("terminal history: %+v %v", history, err)
			}
			before := workflowState(t, ctx, s)
			for _, guard := range []string{first.Revision, last.Memory.Revision} {
				_, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/recover", FromRevision: first.Revision, ExpectedRevision: guard})
				if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("fake D guard accepted/wrote: %v", err)
				}
			}
			injected := errors.New("recovery rollback")
			for _, stage := range []string{"coordination", "memory-recover-allocation", "memory-payload", "source-catalog", "source-retained"} {
				s.afterWrite = func(at string) error {
					if at == stage {
						return injected
					}
					return nil
				}
				_, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/recover", FromRevision: first.Revision, ExpectedRevision: d})
				s.afterWrite = nil
				if !errors.Is(err, injected) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("%s rollback failed: %v", stage, err)
				}
			}
			recovered, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/recover", FromRevision: first.Revision, ExpectedRevision: d, Actor: "recoverer"})
			if err != nil {
				t.Fatal(err)
			}
			h := recovered.Memory.Attribution.HistoricalUpdate
			if !recovered.Changed || recovered.Memory.ID != first.ID || recovered.Memory.Revision == d || recovered.Memory.Revision == first.Revision || recovered.Memory.Properties != first.Properties || recovered.Memory.Attribution.Inception != "revert" || h == nil || h.PreviousRevision != d || h.FromRevision != first.Revision || len(recovered.Memory.Owned) != 0 {
				t.Fatalf("wrong recovery: %+v", recovered)
			}
			if _, err := s.ReadVersion(ctx, "beads/recover", d); !errors.Is(err, ErrGone) {
				t.Fatalf("historical D became snapshot after recovery: %v", err)
			}
			history, err = s.History(ctx, "beads/recover")
			if err != nil || history.CurrentState != nil || len(history.Versions) != 4 || !history.Versions[1].Removed {
				t.Fatalf("recovery lost D history: %+v %v", history, err)
			}
			ordinary, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/recover", Body: historicalBody("ordinary"), Unconditional: true})
			if err != nil || ordinary.Memory.Attribution.Inception != "revert" || ordinary.Memory.Attribution.HistoricalUpdate != nil {
				t.Fatalf("ordinary mint provenance: %+v %v", ordinary, err)
			}
			secondDelete, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/recover", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			// Optional current guards stay optional; equal historical content is
			// still a presence-changing recovery and must mint a fresh revision.
			again, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/recover", FromRevision: ordinary.Memory.Revision, Unconditional: true})
			if err != nil || !again.Changed || again.Memory.Attribution.HistoricalUpdate.PreviousRevision != secondDelete.Terminal.Revision {
				t.Fatalf("unguarded/equal-content recovery: %+v %v", again, err)
			}
		})
	}
}

func TestHistoricalMemoryRecoveryProtectedBaselineAndEvaluationLifetime(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := assessmentMemoryStore(t, engine)
			a, err := s.Create(ctx, CreateRequest{Path: "beads/a", Title: "A"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Create(ctx, CreateRequest{Path: "beads/b", Title: "B"})
			if err != nil {
				t.Fatal(err)
			}
			props := func(subject Record) json.RawMessage {
				raw, err := json.Marshal(map[string]any{"title": "Evaluation", "body": "", "subjects": []any{map[string]string{"uri": subject.ID, "revision": subject.Revision}}})
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			for _, previouslyEvaluation := range []bool{false, true} {
				path := "beads/new-evaluation"
				if previouslyEvaluation {
					path = "beads/prior-evaluation"
				}
				initial, err := s.Create(ctx, CreateRequest{Path: path, Title: "Initial"})
				if err != nil {
					t.Fatal(err)
				}
				if previouslyEvaluation {
					if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: props(a), Unconditional: true}); err != nil {
						t.Fatal(err)
					}
					if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, TypeURL: s.ScopeURL() + "types/constraint", PropertiesJSON: json.RawMessage(`{"title":"Constraint","body":""}`), Unconditional: true}); err != nil {
						t.Fatal(err)
					}
				}
				deleted, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: path, Unconditional: true})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, FromRevision: initial.Revision, ExpectedRevision: deleted.Terminal.Revision}); err != nil {
					t.Fatal(err)
				}
				if previouslyEvaluation {
					before := workflowState(t, ctx, s)
					if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: props(b), Unconditional: true}); err == nil || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatalf("recovery reset lifetime group: %v", err)
					}
				}
				if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: props(a), Unconditional: true}); err != nil {
					t.Fatalf("valid first/prior group blocked by D: %v", err)
				}
			}
			lost, err := s.Create(ctx, CreateRequest{Path: "beads/lost-baseline", Title: "Last current"})
			if err != nil {
				t.Fatal(err)
			}
			deleted, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/lost-baseline", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			// D is retained evidence, never an admitted live subject snapshot.
			fakeSubject := lost
			fakeSubject.Revision = deleted.Terminal.Revision
			before := workflowState(t, ctx, s)
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/terminal-assessment", TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: props(fakeSubject)}); err == nil || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("terminal admitted as live subject or wrote: %v", err)
			}
			if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_versions WHERE path=? AND version=?`, "beads/lost-baseline", lost.Revision); err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/lost-baseline", FromRevision: lost.Revision, ExpectedRevision: deleted.Terminal.Revision}); !errors.Is(err, ErrInvalidStore) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("missing protected baseline fell back or wrote: %v", err)
			}
		})
	}
}

func TestHistoricalMemoryRecoveryDoesNotResurrectOwnedLinks(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			owner, err := s.Create(ctx, CreateRequest{Path: "beads/owner", Title: "Owner"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/target", Title: "Target"}); err != nil {
				t.Fatal(err)
			}
			link, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/old", TypeURL: s.ScopeURL() + "types/superseded-by", SourcePath: "beads/owner", TargetPath: "beads/target", ExpectedSourceRevision: owner.Revision})
			if err != nil {
				t.Fatal(err)
			}
			source := link.Source.(Record)
			if len(source.Owned) != 1 {
				t.Fatal("fixture has no owned Link")
			}
			if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/old", ExpectedRevision: link.Link.Revision, ExpectedSourceRevision: source.Revision}); err != nil {
				t.Fatal(err)
			}
			d, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/owner", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/owner", FromRevision: source.Revision, ExpectedRevision: d.Terminal.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if len(recovered.Memory.Owned) != 0 {
				t.Fatalf("resurrected owned membership: %+v", recovered.Memory)
			}
			if _, err := s.ShowLink(ctx, "links/old"); !errors.Is(err, ErrGone) {
				t.Fatalf("resurrected removed Link: %v", err)
			}
			if old, err := s.ReadVersion(ctx, "beads/owner", source.Revision); err != nil || !reflect.DeepEqual(old, source) {
				t.Fatalf("rewrote historical owned state: %+v %v", old, err)
			}
		})
	}
}

func TestHistoricalMemoryRecoveryConcurrentWriters(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			parent, options, first := memoryDeleteStore(t, engine)
			ctx, cancel := context.WithTimeout(parent, 90*time.Second)
			defer cancel()
			original, err := first.Create(ctx, CreateRequest{Path: "beads/race", Title: "Race"})
			if err != nil {
				t.Fatal(err)
			}
			d, err := first.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/race", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			// Embedded uses its ordinary one-session pool. Opening the same
			// filesystem engine twice would test a file lock, not write races.
			second := &Store{db: first.db, options: options}
			if engine == "server" {
				second, err = OpenExisting(ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				defer second.Close()
			}
			reached, release := make(chan struct{}, 2), make(chan struct{})
			if engine == "server" {
				barrier := func(stage string) error {
					if stage != "source-retained" {
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
				first.afterWrite, second.afterWrite = barrier, barrier
			}
			results := make(chan error, 2)
			var writers sync.WaitGroup
			for _, s := range []*Store{first, second} {
				writers.Add(1)
				go func() {
					defer writers.Done()
					_, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/race", FromRevision: original.Revision, ExpectedRevision: d.Terminal.Revision})
					results <- err
				}()
			}
			defer func() { cancel(); writers.Wait() }()
			if engine == "server" {
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
			success, conflict := 0, 0
			for range 2 {
				select {
				case err := <-results:
					if err == nil {
						success++
					} else if errors.Is(err, ErrConflict) && !errors.Is(err, ErrOutcomeUnknown) {
						conflict++
					} else {
						t.Fatalf("unexpected race outcome: %v", err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			writers.Wait()
			first.afterWrite, second.afterWrite = nil, nil
			if success != 1 || conflict != 1 {
				t.Fatalf("success=%d conflict=%d", success, conflict)
			}
			history, err := first.History(ctx, "beads/race")
			if err != nil || history.CurrentState != nil || len(history.Versions) != 3 || !history.Versions[1].Removed {
				t.Fatalf("race retained duplicate/partial recovery: %+v %v", history, err)
			}
		})
	}
}

func TestHistoricalMemoryRecoveryLostCommitResponse(t *testing.T) {
	ctx, options, direct := memoryDeleteStore(t, "server")
	original, err := direct.Create(ctx, CreateRequest{Path: "beads/unknown", Title: "Unknown outcome"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := direct.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/unknown", Unconditional: true})
	if err != nil {
		t.Fatal(err)
	}
	port, observed := startCommitLossProxy(t, net.JoinHostPort(options.ServerHost, strconv.Itoa(options.ServerPort)))
	proxied := options
	proxied.ServerPort = port
	s, err := OpenExisting(ctx, proxied)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/unknown", FromRevision: original.Revision, ExpectedRevision: d.Terminal.Revision})
	if !errors.Is(err, ErrOutcomeUnknown) || !reflect.ValueOf(result).IsZero() {
		t.Fatalf("uncertain recovery claimed success: %+v %v", result, err)
	}
	select {
	case packet := <-observed:
		if len(packet) == 0 || packet[0] != 0 {
			t.Fatalf("no commit witness: %x", packet)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	current, err := direct.Show(ctx, "beads/unknown")
	if err != nil || current.Attribution.HistoricalUpdate == nil || current.Attribution.HistoricalUpdate.PreviousRevision != d.Terminal.Revision {
		t.Fatalf("witnessed recovery incomplete: %+v %v", current, err)
	}
	before := workflowState(t, ctx, direct)
	if _, err := direct.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/unknown", FromRevision: original.Revision, ExpectedRevision: d.Terminal.Revision}); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, workflowState(t, ctx, direct)) {
		t.Fatalf("guarded replay minted another revision: %v", err)
	}
}

func TestHistoricalMemoryRecoveryUnsupportedBaselineBoundaries(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			issue, err := s.CreateIssue(ctx, "beads/native", plainIssue("Native"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/native", Unconditional: true}); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/native", FromRevision: issue.Revision, Unconditional: true}); !errors.Is(err, ErrCapabilityUnavailable) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("unsupported native recovery implied corruption or wrote: %v", err)
			}
			memory, err := s.Create(ctx, CreateRequest{Path: "beads/legacy-delete", Title: "Legacy"})
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/legacy-delete", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			// Model the pre-terminal preview representation, not a migration.
			if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=? WHERE path=?`, memory.Revision, "beads/legacy-delete"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_versions WHERE path=? AND version=?`, "beads/legacy-delete", d.Terminal.Revision); err != nil {
				t.Fatal(err)
			}
			if old, err := s.ReadVersion(ctx, "beads/legacy-delete", memory.Revision); err != nil || !reflect.DeepEqual(old, memory) {
				t.Fatalf("legacy retained read changed: %+v %v", old, err)
			}
			before = workflowState(t, ctx, s)
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/legacy-delete", FromRevision: memory.Revision, ExpectedRevision: memory.Revision}); !errors.Is(err, ErrCapabilityUnavailable) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("legacy last-live token substituted for D: %v", err)
			}
		})
	}
}

func TestHistoricalMemoryAcquisitionBudget(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			// Keep independent acquisition scenarios in separate fixtures: their
			// setup and full-store rollback checks must not share one deadline.
			t.Run("current-source", func(t *testing.T) {
				ctx, _, s := memoryDeleteStore(t, engine)
				ctx = currentReadBudgetContext(t, ctx)
				original, err := createLegacyMemoryFixture(t, ctx, s, CreateRequest{Path: "beads/budget", Title: "Budget", Body: strings.Repeat("a", 6<<20)})
				if err != nil {
					t.Fatal(err)
				}
				current, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/budget", Body: historicalBody(strings.Repeat("b", 6<<20)), Unconditional: true})
				if err != nil {
					t.Fatal(err)
				}
				before := workflowState(t, ctx, s)
				if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/budget", FromRevision: original.Revision, ExpectedRevision: current.Memory.Revision}); !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("individually bounded current/source exceeded combined budget or wrote: %v", err)
				}
				// A small result must not excuse acquiring an oversized historical input.
				if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/budget", FromRevision: original.Revision, Body: historicalBody("small"), Unconditional: true}); !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("postwrite-only limit accepted oversized acquisition: %v", err)
				}
			})
			t.Run("recovery-baseline-source", func(t *testing.T) {
				// Two individually legal retained inputs exceed the combined
				// protected-baseline/source acquisition budget.
				ctx, _, s := memoryDeleteStore(t, engine)
				ctx = currentReadBudgetContext(t, ctx)
				large, err := createLegacyMemoryFixture(t, ctx, s, CreateRequest{Path: "beads/budget", Title: "Budget", Body: strings.Repeat("c", 9<<20)})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/budget", Body: historicalBody(strings.Repeat("d", 8<<20)), Unconditional: true}); err != nil {
					t.Fatal(err)
				}
				deleted, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/budget", Unconditional: true})
				if err != nil {
					t.Fatal(err)
				}
				before := workflowState(t, ctx, s)
				if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/budget", FromRevision: large.Revision, ExpectedRevision: deleted.Terminal.Revision}); !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("baseline and source exceeded combined recovery budget or wrote: %v", err)
				}
				if history, err := s.History(ctx, "beads/budget"); err != nil || history.CurrentState == nil || history.CurrentState.Revision != deleted.Terminal.Revision {
					t.Fatalf("descriptor hydrated irrelevant large baseline: %v", err)
				}
			})
		})
	}
}

func TestHistoricalMemoryTerminalDescriptorBudget(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			ctx = currentReadBudgetContext(t, ctx)
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/descriptor", Title: "Descriptor"}); err != nil {
				t.Fatal(err)
			}
			deleted, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/descriptor", Actor: strings.Repeat("a", 6<<20), Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			if deleted.Terminal == nil {
				t.Fatal("missing D fixture")
			}
			before := workflowState(t, ctx, s)
			if history, err := s.History(ctx, "beads/descriptor"); !errors.Is(err, ErrLimitExceeded) || !reflect.ValueOf(history).IsZero() || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("metadata plus D descriptor exceeded cumulative budget or wrote: %v", err)
			}
		})
	}
}

func TestHistoricalMemoryTerminalDescriptorPrivacy(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			last, err := s.Create(ctx, CreateRequest{Path: "beads/private-baseline", Title: "Private baseline", Body: "not part of terminal descriptor", Metadata: json.RawMessage(`{"private":"baseline"}`)})
			if err != nil {
				t.Fatal(err)
			}
			deleted, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/private-baseline", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_versions SET snapshot='{}' WHERE path=? AND version=?`, "beads/private-baseline", last.Revision); err != nil {
				t.Fatal(err)
			}
			history, err := s.History(ctx, "beads/private-baseline")
			if err != nil || history.CurrentState == nil || history.CurrentState.Revision != deleted.Terminal.Revision {
				t.Fatalf("D descriptor depended on decoding baseline: %v", err)
			}
			raw, err := json.Marshal(history.CurrentState)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"type", "properties", "metadata", "owned", "source", "target"} {
				if _, present := fields[key]; present {
					t.Fatalf("terminal descriptor disclosed %s", key)
				}
			}
			before := workflowState(t, ctx, s)
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/private-baseline", FromRevision: last.Revision, ExpectedRevision: deleted.Terminal.Revision}); !errors.Is(err, ErrInvalidStore) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("recovery bypassed its independent protected baseline: %v", err)
			}
		})
	}
}

func TestHistoricalMemoryUpdateCumulativeAcquisitionBudget(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			ctx = currentReadBudgetContext(t, ctx)
			first, err := s.Create(ctx, CreateRequest{Path: "beads/budget", Title: "Budget", Body: strings.Repeat("a", 6<<20)})
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/budget", Body: historicalBody(strings.Repeat("b", 6<<20)), Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			for _, body := range []*string{nil, historicalBody("small")} {
				_, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/budget", FromRevision: first.Revision, Body: body, ExpectedRevision: current.Memory.Revision})
				if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("current/source acquisition exceeded cumulative bound or wrote: %v", err)
				}
			}
		})
	}
}

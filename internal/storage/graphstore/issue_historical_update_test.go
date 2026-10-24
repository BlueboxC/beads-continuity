//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestHistoricalIssueUpdateCompositionAndContext(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			request := plainIssue("Original")
			request.Issue.Description = "original body"
			request.Issue.Metadata = json.RawMessage(`{"old":null}`)
			original, err := s.CreateIssue(ctx, "beads/issue", request)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", Description: issueEditString("current body"), Metadata: publicops.MetadataPatch{Replace: publicops.Field[json.RawMessage]{Set: true, Value: json.RawMessage(`{"current":true}`)}}, Actor: "current-writer", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", FromRevision: original.Revision, ExpectedRevision: current.Issue.Revision, Title: issueEditString("Explicit title"), Metadata: publicops.MetadataPatch{Set: map[string]json.RawMessage{"explicit": json.RawMessage(`7`)}}, Actor: "historical-writer"})
			if err != nil {
				t.Fatal(err)
			}
			h := result.Issue.Attribution.HistoricalUpdate
			if !result.Changed || result.Issue.Properties.Description != original.Properties.Description || result.Issue.Properties.Title != "Explicit title" || string(result.Issue.Metadata) != `{"explicit":7,"old":null}` || h == nil || h.FromRevision != original.Revision || h.PreviousRevision != current.Issue.Revision || result.Issue.Attribution.Actor != "historical-writer" || result.Issue.Attribution.Inception != original.Attribution.Inception {
				t.Fatalf("wrong historical native composition: %+v", result)
			}
			if !result.Issue.Properties.CreatedAt.Equal(current.Issue.Properties.CreatedAt) || result.Issue.Properties.CreatedBy != current.Issue.Properties.CreatedBy || !reflect.DeepEqual(result.Issue.Properties.Labels, current.Issue.Properties.Labels) {
				t.Fatal("rewound native protected structure/initialization")
			}
			assertIssueEditVersion(t, ctx, s, "beads/issue", result.Issue)
			before := workflowState(t, ctx, s)
			for _, r := range []UpdateIssueRequest{{Path: "beads/issue", FromRevision: result.Issue.Revision, Actor: "noop", Unconditional: true}, {Path: "beads/issue", PropertiesProvided: true, Actor: "noop", Unconditional: true}} {
				noop, err := s.UpdateIssue(ctx, r)
				if err != nil || noop.Changed || !reflect.DeepEqual(noop.Issue, result.Issue) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("native no-op rewrote envelope/context: %v", err)
				}
			}
			ordinary, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", Description: issueEditString("ordinary"), Actor: "ordinary", Unconditional: true})
			if err != nil || ordinary.Issue.Attribution.HistoricalUpdate != nil || ordinary.Issue.Attribution.Inception != result.Issue.Attribution.Inception {
				t.Fatalf("ordinary native mint retained old historical context or lost Inception: %v", err)
			}
			before = workflowState(t, ctx, s)
			for _, stage := range []string{"coordination", "issue-update", "issue-retained", "source-catalog", "source-retained"} {
				injected := errors.New("native historical rollback")
				s.afterWrite = func(at string) error {
					if at == stage {
						return injected
					}
					return nil
				}
				_, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", FromRevision: original.Revision, Actor: "rollback", Unconditional: true})
				s.afterWrite = nil
				if !errors.Is(err, injected) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("%s leaked native historical write: %v", stage, err)
				}
			}
		})
	}
}

func TestHistoricalIssuePreservesLabelsAndOwnedGraph(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			request := plainIssue("Source")
			request.Issue.Labels = []string{"old"}
			source, err := s.CreateIssue(ctx, "beads/source", request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateIssue(ctx, "beads/target", plainIssue("Target")); err != nil {
				t.Fatal(err)
			}
			dep, err := s.AddDependency(ctx, DependencyRequest{SourcePath: "beads/source", TargetPath: "beads/target", Path: "links/old", Type: types.DepBlocks, ExpectedSourceRevision: source.Revision, Actor: "linker"})
			if err != nil {
				t.Fatal(err)
			}
			_ = dep
			historical, err := s.ShowIssue(ctx, "beads/source")
			if err != nil || len(historical.Owned) != 1 {
				t.Fatalf("missing historical owned fixture: %v", err)
			}
			if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/old", Unconditional: true, UnconditionalSource: true, Actor: "unlinker"}); err != nil {
				t.Fatal(err)
			}
			// Exercise the native label writer inside its ordinary graph recording seam;
			// historical mode itself never gets a label setter.
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				if err := s.touchCoordination(ctx, tx); err != nil {
					return err
				}
				unScope := issueops.ScopeVersionedHistoryTransaction(tx, true)
				defer unScope()
				_, _, err := issueops.ExecuteUpdate(ctx, tx, publicops.UpdateRequest{IssueID: source.Properties.ID, Actor: "labeler", IssuePlaneOnly: true, Patch: publicops.IssuePatch{Description: publicops.Field[string]{Set: true, Value: "current body"}, Labels: publicops.LabelPatch{Replace: publicops.Field[[]string]{Set: true, Value: []string{"current"}}}}})
				if err != nil {
					return err
				}
				return s.recordIssueMappingInTx(ctx, tx, "beads/source", source.Properties.ID)
			}); err != nil {
				t.Fatal(err)
			}
			current, err := s.ShowIssue(ctx, "beads/source")
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/source", FromRevision: historical.Revision, ExpectedRevision: current.Revision, Actor: "historical"})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Changed || !reflect.DeepEqual(result.Issue.Properties.Labels, []string{"current"}) || len(result.Issue.Owned) != 0 || result.Issue.Properties.Description != historical.Properties.Description {
				t.Fatalf("historical native update rewound protected labels/graph: %+v", result)
			}
			if _, err := s.ShowLink(ctx, "links/old"); !errors.Is(err, ErrGone) {
				t.Fatalf("historical native mode revived Link: %v", err)
			}
		})
	}
}

func TestHistoricalIssueRemovedSourceRefuses(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			original, err := s.CreateIssue(ctx, "beads/issue", plainIssue("Original"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", Title: issueEditString("Current"), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE issue_versions SET removed_at=CURRENT_TIMESTAMP,removed_reason=?,removed_restriction=?,durable_state=? WHERE issue_id=? AND revision=(SELECT issue_revision FROM graph_preview_issue_versions WHERE path=? AND version=?)`, "selected source removed", "gone_erasure", []byte(`invalid retained body must not be hydrated`), original.Properties.ID, "beads/issue", original.Revision)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			if _, err := s.ReadVersion(ctx, "beads/issue", original.Revision); !errors.Is(err, ErrVersionUnknown) || !strings.Contains(err.Error(), "gone_erasure: selected source removed") {
				t.Fatalf("wrong exact removed refusal: %v", err)
			}
			if _, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", FromRevision: original.Revision, Actor: "historical", Unconditional: true}); !errors.Is(err, ErrVersionUnknown) || !strings.Contains(err.Error(), "gone_erasure: selected source removed") {
				t.Fatalf("wrong historical removed refusal: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("removed source changed destination")
			}
		})
	}
}

func TestHistoricalIssueSourceRefusals(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			original, err := s.CreateIssue(ctx, "beads/issue", plainIssue("Original"))
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := s.CreateIssue(ctx, "beads/other", plainIssue("Other"))
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", Title: issueEditString("Current"), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			for _, tc := range []struct {
				name, source, guard string
				want                error
			}{
				{"unknown", "unknown", current.Issue.Revision, ErrVersionUnknown},
				{"foreign", foreign.Revision, current.Issue.Revision, ErrVersionUnknown},
				{"stale", original.Revision, original.Revision, ErrConflict},
			} {
				t.Run(tc.name, func(t *testing.T) {
					_, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", FromRevision: tc.source, ExpectedRevision: tc.guard, Actor: "historical"})
					if !errors.Is(err, tc.want) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatalf("source refusal changed state or wrong error: %v", err)
					}
				})
			}
			if _, err := s.CloseIssues(ctx, IssueCloseBatchRequest{Paths: []string{"beads/issue"}, Reasons: []string{"closed"}, Actor: "closer"}); err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", FromRevision: original.Revision, Unconditional: true, Actor: "historical"})
			if !errors.Is(err, ErrCapabilityUnavailable) || !strings.Contains(err.Error(), "status requires its native domain mapping") || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("unsupported status historical copy must refuse atomically: %v", err)
			}
		})
	}
}

func TestHistoricalIssueKeepsNativeWriteFences(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			request := plainIssue("Notes")
			request.Issue.Notes = "historical notes"
			original, err := s.CreateIssue(ctx, "beads/notes", request)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/notes", Notes: issueEditString("current notes"), Actor: "writer", Unconditional: true, ForceNotesOverwrite: true})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/notes", FromRevision: original.Revision, ExpectedRevision: current.Issue.Revision, Actor: "historical"})
			if !errors.Is(err, storage.ErrNotesOverwrite) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("historical mode bypassed native notes fence: %v", err)
			}
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/notes", FromRevision: original.Revision, ExpectedRevision: current.Issue.Revision, Actor: "historical", PropertiesPatch: []byte(`[{"op":"replace","path":"/design","value":"explicit"}]`)})
			if !errors.Is(err, storage.ErrNotesOverwrite) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("ordered historical mode bypassed native notes fence: %v", err)
			}
			forced, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/notes", FromRevision: original.Revision, Unconditional: true, Actor: "historical", ForceNotesOverwrite: true, AppendNotes: issueEditString("appended")})
			if err != nil || forced.Issue.Properties.Notes != "historical notes\nappended" {
				t.Fatalf("explicit append did not follow selected notes base: %v %+v", err, forced)
			}
			request = plainIssue("Assignment")
			request.Issue.Status = types.StatusInProgress
			request.Issue.Assignee = "old-holder"
			assigned, err := s.CreateIssue(ctx, "beads/assigned", request)
			if err != nil {
				t.Fatal(err)
			}
			current, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/assigned", Assignee: issueEditString("current-holder"), Actor: "old-holder", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/assigned", FromRevision: assigned.Revision, ExpectedRevision: current.Issue.Revision, Actor: "outsider"})
			if !errors.Is(err, storage.ErrAlreadyClaimed) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("historical mode bypassed active assignment fence: %v", err)
			}
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/assigned", FromRevision: assigned.Revision, ExpectedRevision: current.Issue.Revision, Actor: "outsider", PropertiesPatch: []byte(`[{"op":"replace","path":"/design","value":"explicit"}]`)})
			if !errors.Is(err, storage.ErrAlreadyClaimed) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("ordered historical mode bypassed active assignment fence: %v", err)
			}
		})
	}
}

func TestHistoricalIssueCumulativeAcquisitionBudget(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			ctx = currentReadBudgetContext(t, ctx)
			request := plainIssue("Large retained source")
			request.Issue.Description = strings.Repeat("s", 7<<20)
			source, err := s.CreateIssue(ctx, "beads/large", request)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", Description: issueEditString(strings.Repeat("c", 5<<20)), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error { return checkCurrentReadBytes(ctx, tx) }); err != nil {
				t.Fatalf("current state alone must fit acquisition budget: %v", err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", FromRevision: source.Revision, ExpectedRevision: current.Issue.Revision, Actor: "historical"})
			if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("native current+source budget not atomic: %v", err)
			}
			// Removed payloads are unavailable inputs, not bytes this operation
			// may hydrate. Preserve that row's refusal even when its size would
			// exceed the combined budget if it were live.
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE issue_versions SET removed_at=CURRENT_TIMESTAMP,removed_reason=?,removed_restriction=? WHERE issue_id=? AND revision=(SELECT issue_revision FROM graph_preview_issue_versions WHERE path=? AND version=?)`, "large source removed", "gone_retention", source.Properties.ID, "beads/large", source.Revision)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", FromRevision: source.Revision, ExpectedRevision: current.Issue.Revision, Actor: "historical"})
			if !errors.Is(err, ErrVersionUnknown) || !strings.Contains(err.Error(), "gone_retention: large source removed") || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("removed large source lost availability refusal or wrote: %v", err)
			}
		})
	}
}

func TestHistoricalIssueConcurrentWriters(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			parent, options, first := memoryDeleteStore(t, engine)
			ctx, cancel := context.WithTimeout(parent, 90*time.Second)
			defer cancel()
			source, err := first.CreateIssue(ctx, "beads/race", plainIssue("Historical"))
			if err != nil {
				t.Fatal(err)
			}
			current, err := first.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/race", Title: issueEditString("Current"), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
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
					_, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/race", FromRevision: source.Revision, ExpectedRevision: current.Issue.Revision, Actor: "historical"})
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
						t.Fatalf("unexpected race: %v", err)
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
			if err != nil || len(history.Versions) != 3 {
				t.Fatalf("partial/duplicate history: %+v %v", history, err)
			}
		})
	}
}

func TestHistoricalIssueLostCommitResponse(t *testing.T) {
	ctx, options, direct := memoryDeleteStore(t, "server")
	source, err := direct.CreateIssue(ctx, "beads/unknown", plainIssue("Historical"))
	if err != nil {
		t.Fatal(err)
	}
	current, err := direct.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/unknown", Title: issueEditString("Current"), Actor: "current", Unconditional: true})
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
	result, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/unknown", FromRevision: source.Revision, ExpectedRevision: current.Issue.Revision, Actor: "historical"})
	if !errors.Is(err, ErrOutcomeUnknown) || !reflect.ValueOf(result).IsZero() {
		t.Fatalf("lost response claimed success: %+v %v", result, err)
	}
	select {
	case packet := <-observed:
		if len(packet) == 0 || packet[0] != 0 {
			t.Fatalf("no commit witness: %x", packet)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	now, err := direct.ShowIssue(ctx, "beads/unknown")
	if err != nil || now.Attribution.HistoricalUpdate == nil || now.Attribution.HistoricalUpdate.PreviousRevision != current.Issue.Revision {
		t.Fatalf("witnessed native update incomplete: %+v %v", now, err)
	}
	before := workflowState(t, ctx, direct)
	_, err = direct.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/unknown", FromRevision: source.Revision, ExpectedRevision: current.Issue.Revision, Actor: "historical"})
	if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, workflowState(t, ctx, direct)) {
		t.Fatalf("guarded replay rewrote: %v", err)
	}
}

func TestHistoricalIssueOrderedPropertyBase(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			request := plainIssue("  Preserved title  ")
			request.Issue.Description = "historical body"
			request.Issue.Metadata = json.RawMessage(`{"historical":true}`)
			source, err := s.CreateIssue(ctx, "beads/patch", request)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/patch", Description: issueEditString("current body"), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/patch", FromRevision: source.Revision, ExpectedRevision: current.Issue.Revision, Actor: "patcher", PropertiesPatch: []byte(`[{"op":"replace","path":"/design","value":"intermediate"},{"op":"replace","path":"/design","value":"explicit"}]`)})
			if err != nil || !result.Changed || result.Issue.Properties.Description != "historical body" || result.Issue.Properties.Design != "explicit" || result.Issue.Properties.Title != source.Properties.Title || string(result.Issue.Metadata) != string(source.Metadata) {
				t.Fatalf("ordered native patch ignored selected base/protected title: %+v %v", result, err)
			}
			before := workflowState(t, ctx, s)
			noop, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/patch", FromRevision: result.Issue.Revision, Actor: "noop", Unconditional: true, PropertiesPatch: []byte(`[{"op":"replace","path":"/design","value":"explicit"}]`)})
			if err != nil || noop.Changed || !reflect.DeepEqual(noop.Issue, result.Issue) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("equal ordered patch rewrote envelope: %v", err)
			}
			for _, raw := range []string{
				`[]`,
				`[{"op":"remove","path":"/title"}]`,
				`[{"op":"replace","path":"/priority","value":null}]`,
				`[{"op":"replace","path":"/priority","value":1.5}]`,
				`[{"op":"add","path":"/status","value":"closed"}]`,
				`[{"op":"replace","path":"/description","value":null}]`,
				`[{"op":"replace","path":"/title","value":""}]`,
			} {
				_, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/patch", FromRevision: source.Revision, Actor: "invalid", Unconditional: true, PropertiesPatch: []byte(raw)})
				if !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("invalid native ordered patch wrote: %s %v", raw, err)
				}
			}
			replaced, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/patch", FromRevision: source.Revision, Actor: "replace", Unconditional: true, PropertiesPatch: []byte(`[{"op":"replace","path":"","value":{"title":"Replacement","priority":0}}]`)})
			if err != nil || !replaced.Changed || replaced.Issue.Properties.Title != "Replacement" || replaced.Issue.Properties.Priority != 0 || replaced.Issue.Properties.Description != "" || replaced.Issue.Properties.EstimatedMinutes != nil || string(replaced.Issue.Metadata) != string(source.Metadata) {
				t.Fatalf("native replacement lost clear/source metadata semantics: %+v %v", replaced, err)
			}
		})
	}
}

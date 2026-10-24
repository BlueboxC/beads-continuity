//go:build cgo

package graphstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Real engines, normal schema initialization and only public graph authoring
// APIs plus explicitly seeded legacy Memory state. New public writes refuse
// acquisition growth before publishing an unreadable current snapshot.
func TestCurrentReadAcquisitionBudget(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			for _, scenario := range []string{"memory", "issue-hydration", "owned-link-and-history"} {
				t.Run(scenario, func(t *testing.T) {
					ctx, o := issueExperimentOptions(t, backend)
					ctx = currentReadBudgetContext(t, ctx)
					s, err := OpenExisting(ctx, o)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := s.Close(); err != nil {
							t.Error(err)
						}
					})
					if _, err := s.Create(ctx, CreateRequest{Title: "Small", Path: "beads/small", TypeURL: s.ScopeURL() + "types/constraint", Body: "small"}); err != nil {
						t.Fatal(err)
					}
					if snapshot, err := s.CurrentSnapshot(ctx); err != nil || len(snapshot.Records) != 1 {
						t.Fatalf("ordinary inventory: %v %+v", err, snapshot)
					}
					switch scenario {
					case "memory":
						if _, err := createLegacyMemoryFixture(t, ctx, s, CreateRequest{Title: "Large", Path: "beads/large", Body: strings.Repeat("x", PreviewCurrentReadByteLimit/2+1024)}); err != nil {
							t.Fatal(err)
						}
						assertCurrentReadBudgetRefusal(t, ctx, s, "beads/large")
					case "issue-hydration":
						// The filler alone fits. A legal TEXT-sized Issue description plus its
						// current durable_state pushes acquisition over the remaining headroom.
						if _, err := createLegacyMemoryFixture(t, ctx, s, CreateRequest{Title: "Filler", Path: "beads/filler", Body: strings.Repeat("x", PreviewCurrentReadByteLimit/2-(32<<10))}); err != nil {
							t.Fatal(err)
						}
						if _, err := s.CurrentSnapshot(ctx); err != nil {
							t.Fatalf("filler alone should fit: %v", err)
						}
						request := plainIssue("bounded issue")
						request.Issue.Description = strings.Repeat("d", 60<<10)
						before := workflowState(t, ctx, s)
						if _, err := s.CreateIssue(ctx, "beads/issue", request); !errors.Is(err, ErrLimitExceeded) {
							t.Fatalf("public native growth: %v", err)
						}
						if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
							t.Fatal("native growth leaked history")
						}
					case "owned-link-and-history":
						source, err := s.Create(ctx, CreateRequest{Title: "Source", Path: "beads/source"})
						if err != nil {
							t.Fatal(err)
						}
						// This note's two persisted Link copies plus the owner's retained copy
						// would fit without accounting for repeated owned/top-level acquisition.
						before := workflowState(t, ctx, s)
						_, err = s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/large", TypeURL: s.ScopeURL() + "types/subject-to", SourcePath: "beads/source", TargetPath: "beads/small", ExpectedSourceRevision: source.Revision, Properties: map[string]any{"note": strings.Repeat("n", PreviewCurrentReadByteLimit/5+4096)}})
						if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
							t.Fatalf("owned/top-level growth was not atomic: %v", err)
						}
					}
				})
			}
		})
	}
}

func assertCurrentReadBudgetRefusal(t *testing.T, ctx context.Context, s *Store, path string) {
	t.Helper()
	snapshot, err := s.CurrentSnapshot(ctx)
	if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(snapshot, Snapshot{}) {
		t.Fatalf("inventory must refuse with zero result: %v; records=%d", err, len(snapshot.Records))
	}
	for _, path := range []string{path, "beads/small"} {
		value, err := s.Read(ctx, path)
		if !errors.Is(err, ErrLimitExceeded) || value != nil {
			t.Fatalf("exact read %s must enforce whole-workspace budget: %v; nonnil=%t", path, err, value != nil)
		}
	}
}

// currentReadBudgetContext bounds the operation phase of near-16MiB fixtures
// independently from initialization. Repeated retained/current state checks
// exceed the ordinary fixture window under concurrent hosted race tests; this
// is a test workload allowance, not an acquisition limit or timing assertion.
func currentReadBudgetContext(t *testing.T, fixture context.Context) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(fixture), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

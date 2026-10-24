//go:build cgo

package graphstore

import (
	"context"
	"errors"
	"reflect"
	"testing"

	publicops "github.com/steveyegge/beads/issueops"
)

// A fault after the first catalog row is stronger than rejecting input up
// front: the native batch writer and the first mapping have both run, yet the
// transaction must restore every Issue, retained version and coordination row.
func TestGraphIssueCreateBatchAtomicMappingAndInitialVersions(t *testing.T) {
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
			request := publicops.CreateBatchRequest{Actor: "test-author", Items: []publicops.BatchCreateItem{
				{Issue: plainIssue("First heading").Issue},
				{Issue: plainIssue("Second heading").Issue},
			}}
			before := workflowState(t, ctx, s)
			fault := errors.New("after first Issue catalog mapping")
			catalogWrites := 0
			s.afterWrite = func(stage string) error {
				if stage == "issue-catalog" {
					catalogWrites++
					if catalogWrites == 1 {
						return fault
					}
				}
				return nil
			}
			failed, err := s.CreateIssueBatch(ctx, request)
			s.afterWrite = nil
			if !errors.Is(err, fault) || failed != nil || catalogWrites != 1 {
				t.Fatalf("first-mapping fault: records=%+v err=%v hook calls=%d", failed, err, catalogWrites)
			}
			if after := workflowState(t, ctx, s); !reflect.DeepEqual(before, after) {
				t.Fatal("failed batch left native Issue, History, catalog, retained version, or coordination state")
			}
			for _, table := range []string{"issues", "issue_versions", "graph_preview_catalog", "graph_preview_issue_versions"} {
				if got := graphBatchRowCount(t, ctx, s, table); got != 0 {
					t.Fatalf("failed batch left %d rows in %s", got, table)
				}
			}

			records, err := s.CreateIssueBatch(ctx, request)
			if err != nil || len(records) != 2 {
				t.Fatalf("successful batch: records=%+v err=%v", records, err)
			}
			for _, record := range records {
				path := fixtureResourcePath(t, ctx, s, record.ID)
				var nativeCount, mappingCount, ordinal, current int
				if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_versions WHERE issue_id=?`, record.Properties.ID).Scan(&nativeCount); err != nil {
					t.Fatal(err)
				}
				if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_issue_versions WHERE path=?`, path).Scan(&mappingCount); err != nil {
					t.Fatal(err)
				}
				if err := s.db.QueryRowContext(ctx, `SELECT current_revision FROM issues WHERE id=?`, record.Properties.ID).Scan(&current); err != nil {
					t.Fatal(err)
				}
				if err := s.db.QueryRowContext(ctx, `SELECT issue_revision FROM graph_preview_issue_versions WHERE path=?`, path).Scan(&ordinal); err != nil {
					t.Fatal(err)
				}
				if nativeCount != 1 || mappingCount != 1 || ordinal != current {
					t.Fatalf("Issue %s has native versions=%d mappings=%d mapped ordinal=%d current=%d", record.ID, nativeCount, mappingCount, ordinal, current)
				}
			}
			if got := graphBatchRowCount(t, ctx, s, "issue_versions"); got != 2 {
				t.Fatalf("successful batch wrote %d native initial versions, want 2", got)
			}
		})
	}
}

// A Memory's legacy graph path is also its public ID. A later Issue batch
// cannot claim that spelling, even when an earlier item in the same batch is
// otherwise valid; refusal must leave the whole transaction unchanged.
func TestGraphIssueCreateBatchRefusesReservedMemoryIDAtomically(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		for _, state := range []string{"live", "deleted"} {
			t.Run(backend+"/"+state, func(t *testing.T) {
				ctx, _, store := memoryDeleteStore(t, backend)
				const reservedID = "beads/exp-batch-reservation"
				if _, err := store.Create(ctx, CreateRequest{Path: reservedID, Title: "Reserved Memory identity"}); err != nil {
					t.Fatal(err)
				}
				if state == "deleted" {
					if _, err := store.DeleteMemory(ctx, MemoryDeleteRequest{Path: reservedID, Unconditional: true}); err != nil {
						t.Fatal(err)
					}
				}
				before := workflowState(t, ctx, store)
				first := plainIssue("Valid first Issue")
				collision := plainIssue("Must refuse cross-kind identity")
				collision.Issue.ID = reservedID
				records, err := store.CreateIssueBatch(ctx, publicops.CreateBatchRequest{
					Actor: "test-author", ForceIDPrefix: true,
					Items: []publicops.BatchCreateItem{{Issue: first.Issue}, {Issue: collision.Issue}},
				})
				if !errors.Is(err, ErrAlreadyExists) || records != nil {
					t.Fatalf("batch claimed reserved Memory ID: records=%+v err=%v", records, err)
				}
				if after := workflowState(t, ctx, store); !reflect.DeepEqual(before, after) {
					t.Fatal("refused batch left native Issue, retained History, or catalog effects")
				}
			})
		}
	}
}

func graphBatchRowCount(t *testing.T, ctx context.Context, s *Store, table string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHistoricalIssueRecovery(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			request := plainIssue("Historical")
			request.Issue.Description = "historical body"
			request.Issue.Metadata = json.RawMessage(`{"source":true}`)
			source, err := s.CreateIssue(ctx, "beads/recover", request)
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/recover", Description: issueEditString("last current body"), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			var generation sql.NullInt64
			var nativeOrdinal int64
			if err := s.db.QueryRowContext(ctx, `SELECT participation_generation,current_revision FROM issues WHERE id=?`, source.Properties.ID).Scan(&generation, &nativeOrdinal); err != nil {
				t.Fatal(err)
			}
			deleted, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/recover", ExpectedRevision: current.Issue.Revision, Actor: "deleter"})
			if err != nil {
				t.Fatal(err)
			}
			if deleted.Terminal == nil || deleted.Terminal.Revision == current.Issue.Revision || deleted.Terminal.PreviousVersion != current.Issue.Revision {
				t.Fatalf("missing actual D: %+v", deleted)
			}
			history, err := s.History(ctx, "beads/recover")
			if err != nil || !reflect.DeepEqual(history.CurrentState, deleted.Terminal) || len(history.Versions) != 3 || history.Versions[0].Ordinal != 3 || !history.Versions[0].Removed {
				t.Fatalf("native terminal carrier: %+v %v", history, err)
			}
			if _, err := s.ShowIssue(ctx, "beads/recover"); !errors.Is(err, ErrGone) {
				t.Fatalf("deleted ordinary read: %v", err)
			}
			before := workflowState(t, ctx, s)
			for _, r := range []UpdateIssueRequest{
				{Path: "beads/recover", FromRevision: source.Revision, ExpectedRevision: current.Issue.Revision, Actor: "stale"},
				{Path: "beads/recover", FromRevision: deleted.Terminal.Revision, ExpectedRevision: deleted.Terminal.Revision, Actor: "terminal source"},
			} {
				_, err := s.UpdateIssue(ctx, r)
				if err == nil || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("invalid native recovery wrote: %v", err)
				}
			}
			for _, stage := range []string{"coordination", "issue-recovery-native", "issue-recovery-allocation", "source-catalog", "source-retained"} {
				injected := errors.New("native recovery rollback")
				s.afterWrite = func(at string) error {
					if at == stage {
						return injected
					}
					return nil
				}
				_, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/recover", FromRevision: source.Revision, ExpectedRevision: deleted.Terminal.Revision, Actor: "rollback"})
				s.afterWrite = nil
				if !errors.Is(err, injected) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("%s leaked native recovery: %v", stage, err)
				}
			}
			recovered, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/recover", FromRevision: source.Revision, ExpectedRevision: deleted.Terminal.Revision, Actor: "recoverer", Title: issueEditString("Explicit")})
			if err != nil {
				t.Fatal(err)
			}
			h := recovered.Issue.Attribution.HistoricalUpdate
			if !recovered.Changed || recovered.Issue.ID != source.ID || recovered.Issue.Type != current.Issue.Type || recovered.Issue.Properties.ID != source.Properties.ID || recovered.Issue.Properties.Title != "Explicit" || recovered.Issue.Properties.Description != "historical body" || string(recovered.Issue.Metadata) != string(source.Metadata) || recovered.Issue.Attribution.Inception != "revert" || h == nil || h.FromRevision != source.Revision || h.PreviousRevision != deleted.Terminal.Revision || recovered.Issue.Attribution.Actor != "recoverer" {
				t.Fatalf("unfaithful native recovery: %+v", recovered)
			}
			if recovered.Issue.Properties.RowVersion == 0 || recovered.Issue.Properties.RowVersion == current.Issue.Properties.RowVersion {
				t.Fatal("native recovery did not mint an independent native row-lock token")
			}
			if !recovered.Issue.Properties.CreatedAt.Equal(current.Issue.Properties.CreatedAt) || recovered.Issue.Properties.CreatedBy != current.Issue.Properties.CreatedBy || len(recovered.Issue.Owned) != 0 || len(recovered.Issue.Properties.Labels) != 0 || recovered.Issue.Properties.LeaseExpiresAt != nil {
				t.Fatal("recovery rewound initialization or restored removed structure/lease")
			}
			var nextGeneration sql.NullInt64
			var nextOrdinal int64
			if err := s.db.QueryRowContext(ctx, `SELECT participation_generation,current_revision FROM issues WHERE id=?`, source.Properties.ID).Scan(&nextGeneration, &nextOrdinal); err != nil || nextGeneration != generation || nextOrdinal != nativeOrdinal+1 {
				t.Fatalf("native admission/ordinal changed incorrectly: %v", err)
			}
			assertIssueEditVersion(t, ctx, s, "beads/recover", recovered.Issue)
			before = workflowState(t, ctx, s)
			if _, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/recover", FromRevision: source.Revision, ExpectedRevision: deleted.Terminal.Revision, Actor: "replay"}); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("D replay minted again: %v", err)
			}
			if _, err := s.ReadVersion(ctx, "beads/recover", deleted.Terminal.Revision); !errors.Is(err, ErrGone) {
				t.Fatalf("D became citable native payload: %v", err)
			}
			history, err = s.History(ctx, "beads/recover")
			if err != nil || history.CurrentState != nil || len(history.Versions) != 4 || history.Versions[0].Ordinal != 4 || history.Versions[1].Version != deleted.Terminal.Revision {
				t.Fatalf("native history lost D/order: %+v %v", history, err)
			}
			secondDelete, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/recover", Unconditional: true, Actor: "deleter"})
			if err != nil {
				t.Fatal(err)
			}
			equal, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/recover", FromRevision: recovered.Issue.Revision, Actor: "same content", Unconditional: true})
			if err != nil || !equal.Changed || equal.Issue.Revision == recovered.Issue.Revision || equal.Issue.Attribution.HistoricalUpdate.PreviousRevision != secondDelete.Terminal.Revision {
				t.Fatalf("equal deleted-to-live was treated as no-op: %+v %v", equal, err)
			}
		})
	}
}

func TestHistoricalIssueRecoveryRefusals(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			source, err := s.CreateIssue(ctx, "beads/recover", plainIssue("Source"))
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := s.CreateIssue(ctx, "beads/foreign", plainIssue("Foreign"))
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/recover", Title: issueEditString("Current"), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			var sourceOrdinal, baselineOrdinal int64
			var sourceBody, baselineBody, baselineContext []byte
			for _, row := range []struct {
				version string
				ordinal *int64
				body    *[]byte
			}{{source.Revision, &sourceOrdinal, &sourceBody}, {current.Issue.Revision, &baselineOrdinal, &baselineBody}} {
				if err := s.db.QueryRowContext(ctx, `SELECT m.issue_revision,v.durable_state FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=? AND m.version=?`, "beads/recover", row.version).Scan(row.ordinal, row.body); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.db.QueryRowContext(ctx, `SELECT revision_context FROM graph_preview_issue_versions WHERE path=? AND version=?`, "beads/recover", current.Issue.Revision).Scan(&baselineContext); err != nil {
				t.Fatal(err)
			}
			deleted, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/recover", Unconditional: true, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			var generation sql.NullInt64
			if err := s.db.QueryRowContext(ctx, `SELECT native_participation_generation FROM graph_preview_versions WHERE path=? AND version=?`, "beads/recover", deleted.Terminal.Revision).Scan(&generation); err != nil {
				t.Fatal(err)
			}
			alternate, err := freshToken()
			if err != nil {
				t.Fatal(err)
			}
			run := func(query string, args ...any) {
				t.Helper()
				if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			type testcase struct {
				name          string
				mutate, undo  func()
				source, guard string
				want          error
				descriptor    bool
			}
			cases := []testcase{
				{name: "ambiguous retained token", mutate: func() {
					fake := *deleted.Terminal
					fake.Revision = source.Revision
					fake.Version = source.Revision
					raw, err := canonicalJSON(fake)
					if err != nil {
						t.Fatal(err)
					}
					run(`INSERT INTO graph_preview_versions(path,version,snapshot,actor,ordinal,change_at) VALUES(?,?,?,?,999,UTC_TIMESTAMP(6))`, "beads/recover", source.Revision, raw, fake.Attribution.Actor)
				}, undo: func() {
					run(`DELETE FROM graph_preview_versions WHERE path=? AND version=?`, "beads/recover", source.Revision)
				}, want: ErrInvalidStore},
				{name: "unknown source", source: "unknown", want: ErrVersionUnknown},
				{name: "foreign source", source: foreign.Revision, want: ErrVersionUnknown},
				{name: "terminal source", source: deleted.Terminal.Revision, want: ErrGone},
				{name: "last live is not D", guard: current.Issue.Revision, want: ErrConflict},
				{name: "missing baseline mapping", mutate: func() {
					run(`UPDATE graph_preview_issue_versions SET version=? WHERE path=? AND version=?`, alternate, "beads/recover", current.Issue.Revision)
				}, undo: func() {
					run(`UPDATE graph_preview_issue_versions SET version=? WHERE path=? AND version=?`, current.Issue.Revision, "beads/recover", alternate)
				}, want: ErrInvalidStore, descriptor: true},
				{name: "missing mapped baseline body", mutate: func() {
					run(`UPDATE graph_preview_issue_versions SET issue_revision=? WHERE path=? AND version=?`, int64(900000), "beads/recover", current.Issue.Revision)
				}, undo: func() {
					run(`UPDATE graph_preview_issue_versions SET issue_revision=? WHERE path=? AND version=?`, baselineOrdinal, "beads/recover", current.Issue.Revision)
				}, want: ErrInvalidStore},
				{name: "malformed baseline", mutate: func() {
					run(`UPDATE issue_versions SET durable_state=? WHERE issue_id=? AND revision=?`, []byte(`not JSON`), source.Properties.ID, baselineOrdinal)
				}, undo: func() {
					run(`UPDATE issue_versions SET durable_state=? WHERE issue_id=? AND revision=?`, baselineBody, source.Properties.ID, baselineOrdinal)
				}, want: ErrInvalidStore, descriptor: true},
				{name: "removed baseline", mutate: func() {
					run(`UPDATE issue_versions SET removed_at=CURRENT_TIMESTAMP,removed_reason='baseline unavailable',removed_restriction='gone_erasure' WHERE issue_id=? AND revision=?`, source.Properties.ID, baselineOrdinal)
				}, undo: func() {
					run(`UPDATE issue_versions SET removed_at=NULL,removed_reason=NULL,removed_restriction=NULL WHERE issue_id=? AND revision=?`, source.Properties.ID, baselineOrdinal)
				}, want: ErrVersionUnknown, descriptor: true},
				{name: "missing native admission", mutate: func() {
					run(`UPDATE graph_preview_versions SET native_participation_generation=NULL WHERE path=? AND version=?`, "beads/recover", deleted.Terminal.Revision)
				}, undo: func() {
					run(`UPDATE graph_preview_versions SET native_participation_generation=? WHERE path=? AND version=?`, generation, "beads/recover", deleted.Terminal.Revision)
				}, want: ErrCapabilityUnavailable, descriptor: true},
				{name: "malformed protected context", mutate: func() {
					run(`UPDATE graph_preview_issue_versions SET revision_context=? WHERE path=? AND version=?`, []byte(`bad context`), "beads/recover", current.Issue.Revision)
				}, undo: func() {
					run(`UPDATE graph_preview_issue_versions SET revision_context=? WHERE path=? AND version=?`, baselineContext, "beads/recover", current.Issue.Revision)
				}, want: ErrInvalidStore, descriptor: true},
				{name: "malformed source", mutate: func() {
					run(`UPDATE issue_versions SET durable_state=? WHERE issue_id=? AND revision=?`, []byte(`bad source`), source.Properties.ID, sourceOrdinal)
				}, undo: func() {
					run(`UPDATE issue_versions SET durable_state=? WHERE issue_id=? AND revision=?`, sourceBody, source.Properties.ID, sourceOrdinal)
				}, want: ErrInvalidStore, descriptor: true},
				{name: "removed source", mutate: func() {
					run(`UPDATE issue_versions SET removed_at=CURRENT_TIMESTAMP,removed_reason='selected unavailable',removed_restriction='gone_retention' WHERE issue_id=? AND revision=?`, source.Properties.ID, sourceOrdinal)
				}, undo: func() {
					run(`UPDATE issue_versions SET removed_at=NULL,removed_reason=NULL,removed_restriction=NULL WHERE issue_id=? AND revision=?`, source.Properties.ID, sourceOrdinal)
				}, want: ErrVersionUnknown, descriptor: true},
				{name: "stale guard before malformed source", guard: current.Issue.Revision, mutate: func() {
					run(`UPDATE issue_versions SET durable_state=? WHERE issue_id=? AND revision=?`, []byte(`bad source`), source.Properties.ID, sourceOrdinal)
				}, undo: func() {
					run(`UPDATE issue_versions SET durable_state=? WHERE issue_id=? AND revision=?`, sourceBody, source.Properties.ID, sourceOrdinal)
				}, want: ErrConflict, descriptor: true},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if tc.mutate != nil {
						tc.mutate()
						defer tc.undo()
					}
					selected, guard := tc.source, tc.guard
					if selected == "" {
						selected = source.Revision
					}
					if guard == "" {
						guard = deleted.Terminal.Revision
					}
					before := workflowState(t, ctx, s)
					result, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/recover", FromRevision: selected, ExpectedRevision: guard, Actor: "refused"})
					if !errors.Is(err, tc.want) || !reflect.ValueOf(result).IsZero() || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatalf("native refusal leaked/fell back: %+v %v", result, err)
					}
					if tc.descriptor {
						h, err := s.History(ctx, "beads/recover")
						if err != nil || !reflect.DeepEqual(h.CurrentState, deleted.Terminal) {
							t.Fatalf("private baseline affected D descriptor: %+v %v", h, err)
						}
						if _, err := s.ShowIssue(ctx, "beads/recover"); !errors.Is(err, ErrGone) {
							t.Fatalf("private baseline changed ordinary deleted read: %v", err)
						}
					}
				})
			}
			// Omitted guard stays valid; unavailable inputs never became an upsert.
			if _, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/recover", FromRevision: source.Revision, Actor: "recovery", Unconditional: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHistoricalIssueRecoveryNativeFencesAndLease(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			request := plainIssue("Notes")
			request.Issue.Notes = "historical notes"
			source, err := s.CreateIssue(ctx, "beads/notes", request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/notes", Notes: issueEditString("current notes"), ForceNotesOverwrite: true, Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/notes", Unconditional: true, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/notes", FromRevision: source.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "restore"})
			if !errors.Is(err, storage.ErrNotesOverwrite) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("recovery bypassed native notes fence: %v", err)
			}
			recovered, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/notes", FromRevision: source.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "restore", ForceNotesOverwrite: true, AppendNotes: issueEditString("explicit append")})
			if err != nil || recovered.Issue.Properties.Notes != "historical notes\nexplicit append" {
				t.Fatalf("recovery native notes order/fence: %+v %v", recovered, err)
			}
			request = plainIssue("Assignment")
			request.Issue.Status = types.StatusInProgress
			request.Issue.Assignee = "old-holder"
			assigned, err := s.CreateIssue(ctx, "beads/assigned", request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/assigned", Assignee: issueEditString("current-holder"), Actor: "old-holder", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			active, err := s.HeartbeatIssue(ctx, "beads/assigned", "current-holder")
			if err != nil || active.Properties.LeaseExpiresAt == nil {
				t.Fatalf("missing active native lease fixture: %v", err)
			}
			d, err = s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/assigned", Unconditional: true, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			before = workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/assigned", FromRevision: assigned.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "outsider"})
			if !errors.Is(err, storage.ErrAlreadyClaimed) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("recovery bypassed current native assignment fence: %v", err)
			}
			recovered, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/assigned", FromRevision: assigned.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "current-holder"})
			if err != nil || recovered.Issue.Properties.Assignee != "old-holder" || recovered.Issue.Properties.Status != types.StatusInProgress || recovered.Issue.Properties.LeaseExpiresAt != nil || recovered.Issue.Properties.HeartbeatAt != nil {
				t.Fatalf("recovery replayed deleted lease or lost authorized native setter: %+v %v", recovered, err)
			}
			var leases int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM leases WHERE issue_id=?`, assigned.Properties.ID).Scan(&leases); err != nil || leases != 0 {
				t.Fatalf("recovery restored deleted node-local lease: %v count=%d", err, leases)
			}
		})
	}
}

func TestHistoricalIssueRecoveryDoesNotRestoreOwnedStructure(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			source, err := s.CreateIssue(ctx, "beads/source", plainIssue("Historical"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateIssue(ctx, "beads/target", plainIssue("Target")); err != nil {
				t.Fatal(err)
			}
			link, err := s.AddDependency(ctx, DependencyRequest{SourcePath: "beads/source", TargetPath: "beads/target", Path: "links/old", Type: types.DepBlocks, ExpectedSourceRevision: source.Revision, Actor: "link"})
			if err != nil {
				t.Fatal(err)
			}
			historical, err := s.ShowIssue(ctx, "beads/source")
			if err != nil || len(historical.Owned) != 1 {
				t.Fatalf("missing positive old ownership fixture: %v", err)
			}
			if _, err := s.Unlink(ctx, LinkDeleteRequest{Path: "links/old", Unconditional: true, UnconditionalSource: true, Actor: "unlink"}); err != nil {
				t.Fatal(err)
			}
			d, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/source", Unconditional: true, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/source", FromRevision: historical.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "recover"})
			if err != nil || len(recovered.Issue.Owned) != 0 || len(recovered.Issue.Properties.Labels) != 0 {
				t.Fatalf("native recovery restored removed structure: %+v %v", recovered, err)
			}
			if _, err := s.ShowLink(ctx, "links/old"); !errors.Is(err, ErrGone) {
				t.Fatalf("native recovery revived old owned Link: %v", err)
			}
			if _, err := s.ReadVersion(ctx, "links/old", link.Link.Revision); err != nil {
				t.Fatalf("recovery destroyed retained old Link evidence: %v", err)
			}
		})
	}
}

func TestHistoricalIssueRecoveryUnknownSourceAttribution(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			source, err := s.CreateIssue(ctx, "beads/unknown-actor", plainIssue("Historical"))
			if err != nil {
				t.Fatal(err)
			}
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/unknown-actor", Title: issueEditString("Current"), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE issue_versions SET change_actor=NULL,attribution_status='unknown' WHERE issue_id=? AND revision=(SELECT issue_revision FROM graph_preview_issue_versions WHERE path=? AND version=?)`, source.Properties.ID, "beads/unknown-actor", source.Revision); err != nil {
				t.Fatal(err)
			}
			old, err := s.ReadVersion(ctx, "beads/unknown-actor", source.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if got := old.(IssueRecord).Attribution; got.Actor != "" || got.Status != "unknown" {
				t.Fatalf("historical unknown actor was invented: %+v", got)
			}
			d, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/unknown-actor", ExpectedRevision: current.Issue.Revision, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/unknown-actor", FromRevision: source.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "new actor"})
			if err != nil || result.Issue.Attribution.Actor != "new actor" || result.Issue.Attribution.Status != "claimed" || result.Issue.Attribution.HistoricalUpdate.FromRevision != source.Revision {
				t.Fatalf("source actor leaked into new native operation: %+v %v", result, err)
			}
		})
	}
}

func TestHistoricalIssueRecoveryRefusesLegacyMintAdmission(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			source, err := s.CreateIssue(ctx, "beads/legacy", plainIssue("Legacy admission"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE issues SET participation_generation=NULL,updated_at=updated_at WHERE id=?`, source.Properties.ID); err != nil {
				t.Fatal(err)
			}
			d, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/legacy", Unconditional: true, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/legacy", FromRevision: source.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "restore"})
			if !errors.Is(err, ErrCapabilityUnavailable) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("recovery automatically admitted a legacy native row: %v", err)
			}
		})
	}
}

func TestHistoricalIssueRecoveryConcurrentWriters(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			parent, options, first := memoryDeleteStore(t, engine)
			ctx, cancel := context.WithTimeout(parent, 90*time.Second)
			defer cancel()
			original, err := first.CreateIssue(ctx, "beads/race", plainIssue("Race"))
			if err != nil {
				t.Fatal(err)
			}
			d, err := first.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/race", Unconditional: true})
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
					_, err := s.UpdateIssue(ctx, UpdateIssueRequest{Actor: "recovery", Path: "beads/race", FromRevision: original.Revision, ExpectedRevision: d.Terminal.Revision})
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

func TestHistoricalIssueRecoveryLostCommitResponse(t *testing.T) {
	ctx, options, direct := memoryDeleteStore(t, "server")
	original, err := direct.CreateIssue(ctx, "beads/unknown", plainIssue("Unknown outcome"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := direct.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/unknown", Unconditional: true})
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
	result, err := s.UpdateIssue(ctx, UpdateIssueRequest{Actor: "recovery", Path: "beads/unknown", FromRevision: original.Revision, ExpectedRevision: d.Terminal.Revision})
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
	current, err := direct.ShowIssue(ctx, "beads/unknown")
	if err != nil || current.Attribution.HistoricalUpdate == nil || current.Attribution.HistoricalUpdate.PreviousRevision != d.Terminal.Revision {
		t.Fatalf("witnessed recovery incomplete: %+v %v", current, err)
	}
	before := workflowState(t, ctx, direct)
	if _, err := direct.UpdateIssue(ctx, UpdateIssueRequest{Actor: "recovery", Path: "beads/unknown", FromRevision: original.Revision, ExpectedRevision: d.Terminal.Revision}); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(before, workflowState(t, ctx, direct)) {
		t.Fatalf("guarded replay minted another revision: %v", err)
	}
}

func TestNativeIssueOversizedCreateRefusesBeforeHistory(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			ctx = currentReadBudgetContext(t, ctx)
			before := workflowState(t, ctx, s)
			request := plainIssue("Large historical source")
			request.Issue.Description = strings.Repeat("s", 10<<20)
			if result, err := s.CreateIssue(ctx, "beads/large", request); !errors.Is(err, ErrLimitExceeded) || !reflect.ValueOf(result).IsZero() {
				t.Fatalf("oversized native source was admitted: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("refusal leaked native history")
			}
		})
	}
}

func TestHistoricalIssueHistoryDescriptorCumulativeBudget(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			ctx = currentReadBudgetContext(t, ctx)
			if _, err := s.CreateIssue(ctx, "beads/history", plainIssue("History")); err != nil {
				t.Fatal(err)
			}
			d, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/history", Unconditional: true, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			terminal := *d.Terminal
			terminal.Attribution.Actor = strings.Repeat("a", 6<<20)
			raw, err := canonicalJSON(terminal)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw)+len(terminal.Attribution.Actor) > PreviewCurrentReadByteLimit {
				t.Fatal("terminal alone must fit the byte bound")
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_versions SET snapshot=?,actor=? WHERE path=? AND version=?`, raw, terminal.Attribution.Actor, "beads/history", terminal.Revision); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			if _, err := s.History(ctx, "beads/history"); !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("History list+D not charged cumulatively: %v", err)
			}
		})
	}
}

func TestHistoricalIssueLegacyDeletionHasNoSyntheticD(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			source, err := s.CreateIssue(ctx, "beads/legacy-delete", plainIssue("Legacy deletion"))
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/legacy-delete", Unconditional: true, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_versions WHERE path=? AND version=?`, "beads/legacy-delete", d.Terminal.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_catalog SET revision=? WHERE path=?`, source.Revision, "beads/legacy-delete"); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			h, err := s.History(ctx, "beads/legacy-delete")
			if err != nil || h.CurrentState != nil || len(h.Versions) != 1 {
				t.Fatalf("legacy deletion acquired fake D: %+v %v", h, err)
			}
			if _, err := s.ReadVersion(ctx, "beads/legacy-delete", source.Revision); err != nil {
				t.Fatalf("legacy final live history lost: %v", err)
			}
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/legacy-delete", FromRevision: source.Revision, ExpectedRevision: source.Revision, Actor: "restore"})
			if !errors.Is(err, ErrCapabilityUnavailable) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("legacy live token used as deletion guard: %v", err)
			}
		})
	}
}

func TestHistoricalIssueRecoveryCumulativeInputs(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			ctx = currentReadBudgetContext(t, ctx)
			request := plainIssue("Large historical source")
			request.Issue.Description = "small fixture"
			source, err := s.CreateIssue(ctx, "beads/large", request)
			if err != nil {
				t.Fatal(err)
			}
			// Reproduce a native snapshot retained before public growth
			// admission. Only this fixture bypasses the new writer guard.
			seedLegacyIssueDescription(t, ctx, s, source, strings.Repeat("s", 10<<20))
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", Description: issueEditString(strings.Repeat("c", 7<<20)), Actor: "current", Unconditional: true})
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/large", ExpectedRevision: current.Issue.Revision, Actor: "delete"})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error { return checkCurrentReadBytes(ctx, tx) }); err != nil {
				t.Fatalf("deleted current state must fit without private baseline hydration: %v", err)
			}
			if _, err := s.ReadVersion(ctx, "beads/large", source.Revision); err != nil {
				t.Fatalf("exact source read needlessly hydrated last-current baseline: %v", err)
			}
			for _, unknownActor := range []bool{false, true} {
				if unknownActor {
					if _, err := s.db.ExecContext(ctx, `UPDATE issue_versions SET change_actor=NULL,attribution_status='unknown' WHERE issue_id=? AND revision=(SELECT issue_revision FROM graph_preview_issue_versions WHERE path=? AND version=?)`, source.Properties.ID, "beads/large", source.Revision); err != nil {
						t.Fatal(err)
					}
				}
				before := workflowState(t, ctx, s)
				_, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", FromRevision: source.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "refused"})
				if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("current D+baseline+source not charged cumulatively (unknownActor=%v): %v", unknownActor, err)
				}
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE issue_versions SET removed_at=CURRENT_TIMESTAMP,removed_reason='selected unavailable',removed_restriction='gone_erasure' WHERE issue_id=? AND revision=(SELECT issue_revision FROM graph_preview_issue_versions WHERE path=? AND version=?)`, source.Properties.ID, "beads/large", source.Revision); err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			_, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", FromRevision: source.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "refused"})
			if !errors.Is(err, ErrVersionUnknown) || !strings.Contains(err.Error(), "gone_erasure: selected unavailable") || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("unavailable native payload was acquired or substituted: %v", err)
			}
			// Baseline-as-source is acquired once, not charged again as another body.
			recovered, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", FromRevision: current.Issue.Revision, ExpectedRevision: d.Terminal.Revision, Actor: "recover baseline"})
			if err != nil || !recovered.Changed || recovered.Issue.Properties.Description != current.Issue.Properties.Description {
				t.Fatalf("baseline role double-charge or lost equal-content recovery: %v", err)
			}
		})
	}
}

func seedLegacyIssueDescription(t *testing.T, ctx context.Context, s *Store, source IssueRecord, body string) {
	t.Helper()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "UPDATE issues SET description=?,updated_at=updated_at WHERE id=?", body, source.Properties.ID); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, "SELECT durable_state FROM issue_versions WHERE issue_id=? AND revision=1", source.Properties.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["description"], err = json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = canonicalJSON(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE issue_versions SET durable_state=? WHERE issue_id=? AND revision=1", raw, source.Properties.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

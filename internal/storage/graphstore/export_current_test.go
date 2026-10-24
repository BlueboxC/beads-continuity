//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestExportCurrentSnapshotLifecycle(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options, s := memoryDeleteStore(t, backend)
			empty, err := s.ExportCurrentSnapshot(ctx)
			if err != nil || len(empty.Snapshot.Records) != 0 || len(empty.Snapshot.Types) != len(previewTypeDefinitions()) || empty.IssuePrefix != "exp" || empty.DeletedAllocations != 0 || len(empty.CommentsByIssuePath) != 0 {
				t.Fatalf("empty export: %+v %v", empty, err)
			}
			if err := exportSetPrefix(ctx, s, "web-app-"); err != nil {
				t.Fatal(err)
			}
			_, err = s.Create(ctx, CreateRequest{Path: "beads/plan", TypeURL: s.ScopeURL() + "types/constraint", Title: " Plan 記憶 ", Body: "\t🌱\n", Metadata: json.RawMessage(`{"source":"disposable"}`)})
			if err != nil {
				t.Fatal(err)
			}
			work, err := s.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil || work.Properties == nil || !strings.HasPrefix(work.Properties.ID, "web-app-") {
				t.Fatalf("hyphenated native prefix: %+v %v", work, err)
			}
			prereq, err := s.CreateIssue(ctx, "beads/prereq", plainIssue("Prerequisite"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddDependency(ctx, DependencyRequest{Path: "links/block", SourcePath: "beads/work", TargetPath: "beads/prereq", Actor: "test-author"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/context", TypeURL: s.ScopeURL() + "types/subject-to", SourcePath: "beads/work", TargetPath: "beads/plan", UnconditionalSource: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/issue-context", SourcePath: "beads/prereq", TargetPath: "beads/plan"}); err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"\toriginal 記憶 🌱\n", ""} {
				if err := s.AddIssueComment(ctx, "beads/work", " author \n", text); err != nil {
					t.Fatal(err)
				}
			}
			// Equal timestamps exercise the original comment-ID tie breaker.
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, "UPDATE comments SET created_at='2026-10-10 12:34:56' WHERE issue_id=?", work.Properties.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			native, err := s.IssueComments(ctx, "beads/work")
			if err != nil || len(native) != 2 {
				t.Fatalf("native comments: %+v %v", native, err)
			}
			wantComments := []types.Comment{*native[0], *native[1]}
			// IssueComments exposes graph selectors; export preserves the native
			// Issue ID stored with each comment. Normalize only this copied oracle.
			for i := range wantComments {
				wantComments[i].IssueID = work.Properties.ID
			}
			before := exportSourceState(t, ctx, s)
			current, err := s.ExportCurrentSnapshot(ctx)
			if err != nil || current.IssuePrefix != "web-app-" || current.DeletedAllocations != 0 || len(current.Snapshot.Records) != 6 || !reflect.DeepEqual(current.CommentsByIssuePath[work.Properties.ID], wantComments) {
				t.Fatalf("complete current export: %+v %v", current, err)
			}
			if comments, present := current.CommentsByIssuePath[prereq.Properties.ID]; !present || comments == nil || len(comments) != 0 {
				t.Fatal("Issue with no comments was absent or null")
			}
			ordinary, err := s.CurrentSnapshot(ctx)
			if err != nil || !reflect.DeepEqual(ordinary, current.Snapshot) {
				t.Fatalf("existing projection changed: %v", err)
			}
			wantSnapshot, err := canonicalJSON(ordinary)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
				t.Fatal("read changed source rows")
			}
			// Returned feeds are detached: caller mutations cannot affect records
			// or the next acquisition and survive neither close nor reopen.
			current.CommentsByIssuePath[work.Properties.ID][0].Text = "caller mutation"
			delete(current.CommentsByIssuePath, prereq.Properties.ID)
			mutatedIssue, mutatedOwned := false, false
			for _, raw := range current.Snapshot.Records {
				if issue, ok := raw.(IssueRecord); ok {
					issue.Properties.Title = "caller mutation"
					mutatedIssue = true
					if len(issue.Owned) > 0 && len(issue.Owned[0]) > 0 {
						issue.Owned[0][0] = '!'
						mutatedOwned = true
					}
				}
			}
			if !mutatedIssue || !mutatedOwned {
				t.Fatal("fixture lacks nested Issue properties or owned Link bytes")
			}
			fresh, err := s.ExportCurrentSnapshot(ctx)
			freshSnapshot, snapshotErr := canonicalJSON(fresh.Snapshot)
			if err != nil || snapshotErr != nil || !reflect.DeepEqual(fresh.CommentsByIssuePath[work.Properties.ID], wantComments) || string(freshSnapshot) != string(wantSnapshot) {
				t.Fatalf("same-Store detachment: %+v %v", fresh, err)
			}
			// TypeDescriptor fields are private: JSON fingerprints capture only
			// their count, so compare the descriptors independently as well.
			if !reflect.DeepEqual(fresh.Snapshot.Types, ordinary.Types) {
				t.Fatal("same-Store reacquisition changed installed Types or their order")
			}
			if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
				t.Fatal("same-Store reacquisition changed source rows")
			}
			if comments, present := fresh.CommentsByIssuePath[prereq.Properties.ID]; !present || comments == nil || len(comments) != 0 {
				t.Fatal("caller deletion affected the next acquisition")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			reopened, err := s.ExportCurrentSnapshot(ctx)
			reopenedSnapshot, snapshotErr := canonicalJSON(reopened.Snapshot)
			if err != nil || snapshotErr != nil || !reflect.DeepEqual(reopened.CommentsByIssuePath[work.Properties.ID], wantComments) || string(reopenedSnapshot) != string(wantSnapshot) || reopened.IssuePrefix != "web-app-" {
				t.Fatalf("reopen preservation: %+v %v", reopened, err)
			}
			if !reflect.DeepEqual(reopened.Snapshot.Types, ordinary.Types) {
				t.Fatal("reopened acquisition changed installed Types or their order")
			}
			if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
				t.Fatal("reopened acquisition changed source rows")
			}
			// Native permanent comments cascade on Issue deletion. The retained
			// Issue History remains outside this current-only acquisition.
			removed, err := s.CreateIssue(ctx, "beads/removed", plainIssue("Removed"))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.AddIssueComment(ctx, "beads/removed", "author", "current feed before deletion"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/removed", ExpectedRevision: removed.Revision}); err != nil {
				t.Fatal(err)
			}
			deleted, err := s.ExportCurrentSnapshot(ctx)
			if err != nil || deleted.DeletedAllocations != 1 || len(deleted.Snapshot.Records) != 6 {
				t.Fatalf("deleted allocation disclosure: %+v %v", deleted, err)
			}
			if _, present := deleted.CommentsByIssuePath[removed.Properties.ID]; present {
				t.Fatal("deleted Issue acquired as a current comment owner")
			}
			var surviving uint64
			if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM comments WHERE issue_id=?", removed.Properties.ID).Scan(&surviving); err != nil || surviving != 0 {
				t.Fatalf("native comment deletion boundary: %d %v", surviving, err)
			}
		})
	}
}

func TestExportCurrentSnapshotFailures(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, backend)
			work, err := s.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.AddIssueComment(ctx, "beads/work", "author", "original"); err != nil {
				t.Fatal(err)
			}
			before := exportSourceState(t, ctx, s)
			// These are callback faults, not injected driver cleanup failures.
			for _, stage := range []string{"export-preflight", "export-endpoints", "export-inventory", "export-comments"} {
				t.Run(stage, func(t *testing.T) {
					fault := errors.New("injected export callback failure")
					s.afterWrite = func(at string) error {
						if at == stage {
							return fault
						}
						return nil
					}
					got, err := s.ExportCurrentSnapshot(ctx)
					s.afterWrite = nil
					if !errors.Is(err, fault) || !reflect.DeepEqual(got, ExportCurrentState{}) || !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
						t.Fatalf("callback failure changed/exposed state: %+v %v", got, err)
					}
				})
			}
			canceled, cancel := context.WithCancel(ctx)
			fault := errors.New("hydration error text without a cancellation chain")
			s.afterWrite = func(stage string) error {
				if stage == "export-endpoints" {
					cancel()
					return fault
				}
				return nil
			}
			got, err := s.ExportCurrentSnapshot(canceled)
			s.afterWrite = nil
			cancel()
			if !errors.Is(err, context.Canceled) || !errors.Is(err, fault) || !reflect.DeepEqual(got, ExportCurrentState{}) || !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
				t.Fatalf("lost cancellation cause: %+v %v", got, err)
			}
			cases := []struct {
				name, query, message string
				orphan               bool
			}{
				{"unmapped-comment-owner", "DELETE FROM graph_preview_catalog WHERE path='beads/work'", "comment without a live graph Issue", false},
				{"deleted-comment-owner", "UPDATE graph_preview_catalog SET allocation_state='deleted' WHERE path='beads/work'", "comment without a live graph Issue", false},
				{"legacy-memory-even-empty", "INSERT INTO config (`key`,value) VALUES ('kv.memory.old','')", "legacy KV Memories", false},
				{"wisps", "INSERT INTO wisps (id,title) VALUES ('exp-wisp','disposable')", "wisps", false},
				{"wisp-comments", "INSERT INTO wisp_comments (id,issue_id,text) VALUES ('wisp-comment','orphan','disposable')", "wisp_comments", true},
				{"wisp-labels", "INSERT INTO wisp_labels (issue_id,label) VALUES ('orphan','disposable')", "wisp_labels", true},
				{"wisp-dependencies", "INSERT INTO wisp_dependencies (id,issue_id,depends_on_external,type) VALUES ('wisp-dependency','orphan','external:disposable','blocks')", "wisp_dependencies", true},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					// Temporary corrupt/alternative rows never leave this rollback.
					withFixture := func(fn func(*sql.Tx) error) error { return s.withTx(ctx, false, fn) }
					if test.orphan {
						withFixture = func(fn func(*sql.Tx) error) error { return exportWithOrphanFixture(ctx, s, fn) }
					}
					err := withFixture(func(tx *sql.Tx) error {
						if _, err := tx.ExecContext(ctx, test.query); err != nil {
							return err
						}
						got, err := s.exportCurrentSnapshotInTx(ctx, tx)
						if !errors.Is(err, ErrInvalidStore) || !strings.Contains(err.Error(), test.message) || !reflect.DeepEqual(got, ExportCurrentState{}) {
							return fmt.Errorf("refusal exposed state: %+v %v", got, err)
						}
						return nil
					})
					if err != nil || !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
						t.Fatalf("refusal/rollback: %v", err)
					}
				})
			}
			// The scan path has a second owner check, independently of preflight.
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error {
				comments, err := exportCommentsInTx(ctx, tx, map[string]string{}, 1)
				if !errors.Is(err, ErrInvalidStore) || comments != nil {
					return fmt.Errorf("scan owner refusal: %+v %v", comments, err)
				}
				comments, err = exportCommentsInTx(ctx, tx, map[string]string{work.Properties.ID: "beads/work"}, 0)
				if !errors.Is(err, ErrInvalidStore) || comments != nil {
					return fmt.Errorf("scan count refusal: %+v %v", comments, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// exportWithOrphanFixture bypasses foreign keys only on this owned pinned
// session, so each auxiliary-plane probe can be reached without a parent wisp
// making the earlier wisps probe win. Rollback is attempted before checks are
// restored; rollback or restoration failure fails the test and discards the session.
func exportWithOrphanFixture(ctx context.Context, s *Store, fn func(*sql.Tx) error) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := conn.Close(); !errors.Is(closeErr, sql.ErrConnDone) {
			err = errors.Join(err, closeErr)
		}
	}()
	var enabled int
	if err = conn.QueryRowContext(ctx, "SELECT @@foreign_key_checks").Scan(&enabled); err != nil {
		return err
	}
	if enabled != 1 {
		return fmt.Errorf("orphan fixture requires initially enabled foreign keys, got %d", enabled)
	}
	rollbackFailed := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, restoreErr := conn.ExecContext(cleanupCtx, "SET @@foreign_key_checks = 1")
		if restoreErr == nil {
			restoreErr = conn.QueryRowContext(cleanupCtx, "SELECT @@foreign_key_checks").Scan(&enabled)
			if restoreErr == nil && enabled != 1 {
				restoreErr = fmt.Errorf("orphan fixture did not restore foreign keys: %d", enabled)
			}
		}
		err = errors.Join(err, restoreErr)
		if restoreErr != nil || rollbackFailed {
			discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
			if !errors.Is(discardErr, driver.ErrBadConn) && !errors.Is(discardErr, sql.ErrConnDone) {
				err = errors.Join(err, discardErr)
			}
		}
	}()
	for _, statement := range []string{"SET @@dolt_transaction_commit = 0", "SET @@dolt_force_transaction_commit = 0", "SET @@dolt_allow_commit_conflicts = 0", "SET @@time_zone = '+00:00'", "SET @@foreign_key_checks = 0"} {
		if _, err = conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	var branch string
	if err = conn.QueryRowContext(ctx, "SELECT active_branch()").Scan(&branch); err != nil {
		return err
	}
	if branch != "main" {
		return fmt.Errorf("orphan fixture requires main branch, got %s", branch)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		rollbackErr := tx.Rollback()
		rollbackFailed = rollbackErr != nil
		err = errors.Join(err, rollbackErr)
	}()
	return fn(tx)
}

func TestExportOrphanFixtureRollbackFailureDiscardsConnection(t *testing.T) {
	fault := errors.New("fixture rollback failed without bad-connection sentinel")
	s, d := exportCleanupStore(t, exportCleanupFaults{rollback: fault})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	called := false
	err := exportWithOrphanFixture(ctx, s, func(*sql.Tx) error {
		called = true
		return nil
	})
	if !called || !errors.Is(err, fault) || d.rollbacks.Load() != 1 {
		t.Fatalf("rollback failure not preserved: called=%t rollbacks=%d err=%v", called, d.rollbacks.Load(), err)
	}
	if d.foreignKeyReads.Load() != 2 || d.foreignKeyRestores.Load() != 1 {
		t.Fatalf("did not reach successful FK restoration/readback: reads=%d restores=%d", d.foreignKeyReads.Load(), d.foreignKeyRestores.Load())
	}
	if d.connectionCloses.Load() != 1 || s.db.Stats().InUse != 0 || s.db.Stats().Idle != 0 {
		t.Fatalf("uncertain fixture session returned to pool: closes=%d stats=%+v", d.connectionCloses.Load(), s.db.Stats())
	}
}

func TestExportCurrentSnapshotAcquisitionBounds(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options, s := memoryDeleteStore(t, backend)
			t.Logf("isolated fixture workspace=%s", options.Binding.WorkspaceID)
			work, err := s.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/filler", Title: "Filler", Body: strings.Repeat("m", 12<<10)}); err != nil {
				t.Fatal(err)
			}
			// The small aggregate quotas predate generation12's fixed32 native
			// descriptors. Offset only that catalog cost, never variable payload
			// bytes, preserving the original64/40/88KiB resource windows.
			expectedNative := map[string]bool{}
			for _, kind := range append(append([]types.IssueType(nil), types.AllIssueTypes...), types.TypeEvent) {
				id, err := NativeIssueTypeURL(s.ScopeURL(), string(kind))
				if err != nil {
					t.Fatal(err)
				}
				expectedNative[strings.TrimPrefix(id, s.ScopeURL()+"types/")] = true
			}
			for _, kind := range types.WellKnownDependencyTypes() {
				id, err := NativeDependencyTypeURL(s.ScopeURL(), string(kind))
				if err != nil {
					t.Fatal(err)
				}
				expectedNative[strings.TrimPrefix(id, s.ScopeURL()+"types/")] = true
			}
			if len(expectedNative) != 32 {
				t.Fatalf("fixed native catalog = %d, want32", len(expectedNative))
			}
			rows, err := s.db.QueryContext(ctx, `SELECT name,OCTET_LENGTH(descriptor),OCTET_LENGTH(fingerprint) FROM graph_preview_types WHERE name LIKE 'native-issue/%' OR name LIKE 'native-dependency/%' ORDER BY name LIMIT 33`)
			if err != nil {
				t.Fatal(err)
			}
			var nativeCatalogBytes uint64
			for rows.Next() {
				var name string
				var descriptorBytes, fingerprintBytes uint64
				if err := rows.Scan(&name, &descriptorBytes, &fingerprintBytes); err != nil {
					t.Fatal(err)
				}
				if !expectedNative[name] {
					t.Fatalf("unexpected/duplicate native catalog Type %q", name)
				}
				delete(expectedNative, name)
				// The reader charges one fixed row overhead plus these persisted
				// fields for each Type. No Resource row contributes to this offset.
				nativeCatalogBytes += exportReadRowOverhead + uint64(len(name)) + descriptorBytes + fingerprintBytes
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				t.Fatal(err)
			}
			if len(expectedNative) != 0 || nativeCatalogBytes == 0 {
				t.Fatalf("incomplete fixed catalog: %v bytes=%d", expectedNative, nativeCatalogBytes)
			}
			byteLimit := uint64(64<<10) + nativeCatalogBytes
			if byteLimit >= PreviewCurrentReadByteLimit {
				t.Fatal("fixture quota reached production aggregate limit")
			}
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error {
				_, err := checkExportReadBoundsAtLimit(ctx, tx, exportCommentLimit, byteLimit)
				return err
			}); err != nil {
				t.Fatalf("baseline must fit small test budget: %v", err)
			}
			// Each comment fits the small budget by itself. Its aggregate with
			// the complete record acquisition does not; the feed is never read.
			if err := s.AddIssueComment(ctx, "beads/work", "author", strings.Repeat("c", 40<<10)); err != nil {
				t.Fatal(err)
			}
			before := exportSourceState(t, ctx, s)
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error {
				if _, err := checkExportReadBoundsAtLimit(ctx, tx, exportCommentLimit, byteLimit); !errors.Is(err, ErrLimitExceeded) {
					return fmt.Errorf("aggregate comment bytes not bounded: %v", err)
				}
				if _, err := checkExportReadBoundsAtLimit(ctx, tx, 0, PreviewCurrentReadByteLimit); !errors.Is(err, ErrLimitExceeded) {
					return fmt.Errorf("comment count not bounded before acquisition: %v", err)
				}
				if _, err := tx.ExecContext(ctx, "DELETE FROM graph_preview_catalog WHERE backing_key=?", work.Properties.ID); err != nil {
					return err
				}
				if _, err := checkExportReadBoundsAtLimit(ctx, tx, exportCommentLimit, byteLimit); !errors.Is(err, ErrLimitExceeded) {
					return fmt.Errorf("orphan comment escaped byte budget: %v", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
				t.Fatal("bounded refusal changed state")
			}
			deletedHeadByteLimit := uint64(40<<10) + nativeCatalogBytes
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error {
				// Match the isolated state used below: the retained filler head
				// remains charged once, without its live payload or comment feed.
				if _, err := tx.ExecContext(ctx, "DELETE FROM comments"); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, "DELETE FROM graph_preview_payloads WHERE path='beads/filler'"); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_catalog SET allocation_state='deleted' WHERE path='beads/filler'"); err != nil {
					return err
				}
				_, err := checkExportReadBoundsAtLimit(ctx, tx, exportCommentLimit, deletedHeadByteLimit)
				return err
			}); err != nil {
				t.Fatalf("baseline must fit deleted-head test budget: %v", err)
			}
			if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
				t.Fatal("deleted-head baseline isolation escaped rollback")
			}
			// A real native D is acquired without its retained predecessor.
			// Legacy deletion instead validates its final native live head, so
			// only that legacy shape acquires and charges the large body below.
			request := plainIssue("Final retained head")
			request.Issue.Description = strings.Repeat("d", 40<<10)
			deleted, err := s.CreateIssue(ctx, "beads/removed", request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/removed", ExpectedRevision: deleted.Revision}); err != nil {
				t.Fatal(err)
			}
			beforeDeletedHeadCheck := exportSourceState(t, ctx, s)
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error {
				// Isolate this admission check from unrelated live filler/feed.
				if _, err := tx.ExecContext(ctx, "DELETE FROM comments"); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, "DELETE FROM graph_preview_payloads WHERE path='beads/filler'"); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_catalog SET allocation_state='deleted' WHERE path='beads/filler'"); err != nil {
					return err
				}
				if _, err := checkExportReadBoundsAtLimit(ctx, tx, exportCommentLimit, deletedHeadByteLimit); err != nil {
					return fmt.Errorf("unacquired native D predecessor consumed export budget: %v", err)
				}
				if err := s.validateDeletedIssueInTx(ctx, tx, "beads/removed"); err != nil {
					return fmt.Errorf("validate real native D: %w", err)
				}
				// Controlled compatibility fixture: point the deleted allocation
				// at the final native live mapping instead of its separate D.
				if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_catalog SET revision=? WHERE path='beads/removed'", deleted.Revision); err != nil {
					return err
				}
				if err := s.validateDeletedIssueInTx(ctx, tx, "beads/removed"); err != nil {
					return fmt.Errorf("validate legacy deleted native head: %w", err)
				}
				// After the fixed catalog offset, the <=40KiB baseline plus one ~40KiB legacy head fits
				// this window; charging that head twice does not. Keep the
				// stricter refusal below to protect aggregate accounting too.
				if _, err := checkExportReadBoundsAtLimit(ctx, tx, exportCommentLimit, uint64(88<<10)+nativeCatalogBytes); err != nil {
					return fmt.Errorf("legacy deleted final head charged more than once: %v", err)
				}
				if _, err := checkExportReadBoundsAtLimit(ctx, tx, exportCommentLimit, deletedHeadByteLimit); !errors.Is(err, ErrLimitExceeded) {
					return fmt.Errorf("acquired legacy deleted final head escaped aggregate budget: %v", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeDeletedHeadCheck, exportSourceState(t, ctx, s)) {
				t.Fatal("deleted-head admission isolation escaped rollback")
			}
		})
	}
}

func TestExportCurrentSnapshotConcurrentWriter(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			base, options, reader := memoryDeleteStore(t, backend)
			ctx, cancel := context.WithTimeout(base, time.Minute)
			defer cancel()
			_, err := reader.Create(ctx, CreateRequest{Path: "beads/source", TypeURL: reader.ScopeURL() + "types/constraint", Title: "Source", Body: "original"})
			if err != nil {
				t.Fatal(err)
			}
			drop, err := reader.Create(ctx, CreateRequest{Path: "beads/drop", Title: "Drop", Body: "Disposable"})
			if err != nil {
				t.Fatal(err)
			}
			work, err := reader.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil {
				t.Fatal(err)
			}
			before, err := reader.ExportCurrentSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Embedded uses its supported serialized connector. Server must
			// permit the controlled writer to commit while this read is paused.
			writer := &Store{db: reader.db, options: options}
			if backend == "server" {
				writer, err = OpenExisting(ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := writer.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			reached, release := make(chan struct{}, 1), make(chan struct{})
			var releaseOnce sync.Once
			reader.afterWrite = func(stage string) error {
				if stage != "export-inventory" {
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
			type outcome struct {
				state ExportCurrentState
				err   error
			}
			readDone := make(chan outcome, 1)
			writeDone := make(chan error, 1)
			started := make(chan struct{})
			var workers sync.WaitGroup
			defer func() {
				cancel()
				releaseOnce.Do(func() { close(release) })
				workers.Wait()
				reader.afterWrite = nil
			}()
			workers.Add(1)
			go func() {
				defer workers.Done()
				state, err := reader.ExportCurrentSnapshot(ctx)
				readDone <- outcome{state, err}
			}()
			select {
			case <-reached:
			case result := <-readDone:
				t.Fatalf("reader exited before overlap: %+v", result)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				close(started)
				if err := writer.AddIssueComment(ctx, "beads/work", "writer", "new comment"); err != nil {
					writeDone <- err
					return
				}
				if _, err := writer.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/new", TypeURL: writer.ScopeURL() + "types/subject-to", SourcePath: "beads/work", TargetPath: "beads/source", UnconditionalSource: true}); err != nil {
					writeDone <- err
					return
				}
				if _, err := writer.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/drop", ExpectedRevision: drop.Revision}); err != nil {
					writeDone <- err
					return
				}
				writeDone <- exportSetPrefix(ctx, writer, "web-app-")
			}()
			<-started
			if backend == "server" {
				select {
				case err := <-writeDone:
					if err != nil {
						t.Fatalf("writer failed before reader release: %v", err)
					}
				case <-ctx.Done():
					t.Fatalf("writer did not commit during forced overlap: %v", ctx.Err())
				}
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case result := <-readDone:
				if result.err != nil || !reflect.DeepEqual(result.state, before) {
					t.Fatalf("torn current-state acquisition: %+v %v", result.state, result.err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if backend == "embedded" {
				select {
				case err := <-writeDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			workers.Wait()
			reader.afterWrite = nil
			after, err := reader.ExportCurrentSnapshot(ctx)
			if err != nil || after.Snapshot.WriterToken == before.Snapshot.WriterToken || after.IssuePrefix != "web-app-" || after.DeletedAllocations != 1 || len(after.CommentsByIssuePath[work.Properties.ID]) != 1 {
				t.Fatalf("next acquisition did not observe complete writer state: %+v %v", after, err)
			}
			var owned int
			for _, record := range after.Snapshot.Records {
				if memory, ok := record.(IssueRecord); ok && memory.ID == work.ID {
					owned = len(memory.Owned)
				}
			}
			if owned != 1 {
				t.Fatalf("new owner/Link state missing: %d", owned)
			}
		})
	}
}

func exportSetPrefix(ctx context.Context, s *Store, prefix string) error {
	return s.withTx(ctx, true, func(tx *sql.Tx) error {
		if err := s.touchCoordination(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE config SET value=? WHERE `key`='issue_prefix'", prefix)
		return err
	})
}

func exportSourceState(t *testing.T, ctx context.Context, s *Store) map[string]string {
	t.Helper()
	state := workflowState(t, ctx, s)
	for _, table := range []string{"comments", "config", "graph_preview_types", "labels", "wisps", "wisp_comments", "wisp_labels", "wisp_dependencies"} {
		rows, err := s.db.QueryContext(ctx, "SELECT * FROM "+table)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(errors.Join(err, rows.Close()))
		}
		var values []string
		for rows.Next() {
			fields := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range fields {
				pointers[i] = &fields[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(errors.Join(err, rows.Close()))
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(errors.Join(err, rows.Close()))
			}
			values = append(values, string(raw))
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		sort.Strings(values)
		state[table] = strings.Join(values, "\n")
	}
	return state
}

// This exercises the real transaction and persistence boundary; the internal
// inventory preserves exact References without including historical endpoints.
func TestExportEndpointPinsCurrentAndRetained(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, backend)
			source, err := s.Create(ctx, CreateRequest{Path: "beads/source", Title: "Source", Body: "Owner"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := s.Create(ctx, CreateRequest{Path: "beads/target", TypeURL: s.ScopeURL() + "types/constraint", Title: "Target", Body: "First"})
			if err != nil {
				t.Fatal(err)
			}
			owned, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/owned", TypeURL: s.ScopeURL() + "types/subject-to", SourcePath: "beads/source", TargetPath: "beads/target", TargetPin: EndpointPin{Revision: target.Revision}, ExpectedSourceRevision: source.Revision})
			if err != nil {
				t.Fatal(err)
			}
			citation, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/citation", SourcePath: "beads/target", TargetPath: "beads/source", SourcePin: EndpointPin{Revision: target.Revision}, TargetPin: EndpointPin{Revision: owned.Source.(Record).Revision}})
			if err != nil {
				t.Fatal(err)
			}
			floating, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/floating", SourcePath: "beads/target", TargetPath: "beads/source"})
			if err != nil {
				t.Fatal(err)
			}
			oldOwner, _ := canonicalJSON(owned.Source)
			oldLink, _ := canonicalJSON(owned.Link)
			body := "Second"
			next, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: &body, ExpectedRevision: target.Revision})
			if err != nil {
				t.Fatal(err)
			}
			check := func(want LinkRecord) {
				t.Helper()
				before := exportSourceState(t, ctx, s)
				state, err := s.ExportCurrentSnapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
					t.Fatal("export mutated source")
				}
				if len(state.Snapshot.Records) != 5 {
					t.Fatalf("historical payload leaked into current inventory: %d", len(state.Snapshot.Records))
				}
				found := map[string]LinkRecord{}
				for _, value := range state.Snapshot.Records {
					switch record := value.(type) {
					case LinkRecord:
						found[record.ID] = record
					case Record:
						if record.ID == target.ID && record.Revision != next.Memory.Revision {
							t.Fatal("exact endpoint replaced current record")
						}
						if record.ID == source.ID {
							if len(record.Owned) != 1 {
								t.Fatal("missing owned Link")
							}
							raw, err := canonicalJSON(want)
							if err != nil || string(raw) != string(record.Owned[0]) {
								t.Fatalf("owned pin mismatch: %v", err)
							}
						}
					}
				}
				for _, link := range []LinkRecord{want, citation.Link, floating.Link} {
					if !reflect.DeepEqual(found[link.ID], link) {
						t.Fatalf("lost Reference: %+v != %+v", found[link.ID], link)
					}
					raw, err := json.Marshal(found[link.ID])
					if err != nil {
						t.Fatal(err)
					}
					var round LinkRecord
					if err := json.Unmarshal(raw, &round); err != nil || !reflect.DeepEqual(round, link) {
						t.Fatalf("carrier roundtrip: %s %v", raw, err)
					}
				}
			}
			check(owned.Link)
			repinned, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/owned", TargetPin: &EndpointPin{Revision: next.Memory.Revision}, ExpectedRevision: owned.Link.Revision, ExpectedSourceRevision: owned.Source.(Record).Revision})
			if err != nil {
				t.Fatal(err)
			}
			if repinned.Link.ID != owned.Link.ID {
				t.Fatal("repin changed identity")
			}
			check(repinned.Link)
			retainedOwner, err := s.ReadVersion(ctx, "beads/source", owned.Source.(Record).Revision)
			if err != nil {
				t.Fatal(err)
			}
			rawOwner, _ := canonicalJSON(retainedOwner)
			retainedLink, err := s.ReadVersion(ctx, "links/owned", owned.Link.Revision)
			if err != nil {
				t.Fatal(err)
			}
			rawLink, _ := canonicalJSON(retainedLink)
			if string(rawOwner) != string(oldOwner) || string(rawLink) != string(oldLink) {
				t.Fatal("repin rewrote retained owner/Link")
			}
			// Corrupt only an older selected payload, while current rows still fit.
			// The byte preflight must refuse before decoding that historical JSON.
			reachedEndpoints, reachedPreflight := false, false
			s.afterWrite = func(stage string) error {
				if stage == "export-preflight" {
					reachedPreflight = true
				}
				if stage == "export-endpoints" {
					reachedEndpoints = true
				}
				return nil
			}
			budgetErr := s.withTx(ctx, false, func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_versions SET snapshot=REPEAT('x',?) WHERE path=? AND version=?", PreviewCurrentReadByteLimit, "beads/target", target.Revision); err != nil {
					return err
				}
				got, err := s.exportCurrentSnapshotInTx(ctx, tx)
				if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(got, ExportCurrentState{}) {
					return fmt.Errorf("historical payload decoded before aggregate bound: %+v %v", got, err)
				}
				return nil
			})
			s.afterWrite = nil
			if budgetErr != nil || reachedEndpoints || reachedPreflight {
				t.Fatalf("historical preflight budget: endpoints=%t preflight=%t %v", reachedEndpoints, reachedPreflight, budgetErr)
			}
			// Corruption/unavailability robustness: keep valid current heads but remove
			// a selected older exact endpoint; this is not a normal writer action.
			// A reader that substitutes the current target would incorrectly succeed.
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, "DELETE FROM graph_preview_versions WHERE path=? AND version=?", "beads/target", target.Revision)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			got, err := s.ExportCurrentSnapshot(ctx)
			if !errors.Is(err, ErrVersionUnknown) || !reflect.DeepEqual(got, ExportCurrentState{}) {
				t.Fatalf("missing exact endpoint fell back/leaked state: %+v %v", got, err)
			}
		})
	}
}

// Manual removal-state corruption robustness; no normal product writer currently
// sets removed_at on these retained Issue editions.
func TestExportEndpointRemovedIssueVersionRefuses(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, backend)
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/source", Title: "Source", Body: "Memory"}); err != nil {
				t.Fatal(err)
			}
			issue, err := s.CreateIssue(ctx, "beads/issue", plainIssue("First"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/citation", SourcePath: "beads/source", TargetPath: "beads/issue", TargetPin: EndpointPin{Revision: issue.Revision}}); err != nil {
				t.Fatal(err)
			}
			title := "Current"
			if _, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/issue", Actor: "test-author", Title: &title, ExpectedRevision: issue.Revision}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ExportCurrentSnapshot(ctx); err != nil {
				t.Fatal(err)
			}
			// Corruption fixtures, not admitted writer states: each old Issue
			// payload arm must be charged before any current/historical decoding.
			beforeBudget := exportSourceState(t, ctx, s)
			for name, query := range map[string]string{
				"durable-state": `UPDATE issue_versions v JOIN graph_preview_issue_versions m ON m.issue_id=v.issue_id AND m.issue_revision=v.revision SET v.durable_state=REPEAT('x',?) WHERE m.path=? AND m.version=?`,
				"owned":         `UPDATE graph_preview_issue_versions m JOIN issue_versions v ON m.issue_id=v.issue_id AND m.issue_revision=v.revision SET m.owned=REPEAT('x',?) WHERE m.path=? AND m.version=?`,
			} {
				t.Run("budget-"+name, func(t *testing.T) {
					preflight, endpoints := false, false
					s.afterWrite = func(stage string) error {
						if stage == "export-preflight" {
							preflight = true
						}
						if stage == "export-endpoints" {
							endpoints = true
						}
						return nil
					}
					bounded := s.withTx(ctx, false, func(tx *sql.Tx) error {
						if _, err := tx.ExecContext(ctx, query, PreviewCurrentReadByteLimit, "beads/issue", issue.Revision); err != nil {
							return err
						}
						got, err := s.exportCurrentSnapshotInTx(ctx, tx)
						if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(got, ExportCurrentState{}) {
							return fmt.Errorf("retained Issue budget did not refuse before decoding: %+v %v", got, err)
						}
						return nil
					})
					s.afterWrite = nil
					if bounded != nil || preflight || endpoints {
						t.Fatalf("retained Issue %s bound: preflight=%t endpoints=%t %v", name, preflight, endpoints, bounded)
					}
					if !reflect.DeepEqual(beforeBudget, exportSourceState(t, ctx, s)) {
						t.Fatal("rollback-only Issue corruption fixture changed source")
					}
				})
			}
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE issue_versions v JOIN graph_preview_issue_versions m ON m.issue_id=v.issue_id AND m.issue_revision=v.revision SET v.removed_at=CURRENT_TIMESTAMP WHERE m.path=? AND m.version=?`, "beads/issue", issue.Revision)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			got, err := s.ExportCurrentSnapshot(ctx)
			if !errors.Is(err, ErrVersionUnknown) || !reflect.DeepEqual(got, ExportCurrentState{}) {
				t.Fatalf("removed retained Issue version admitted/leaked state: %+v %v", got, err)
			}
		})
	}
}

func TestExportEndpointDistinctValidationBudget(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, backend)
			ctx = currentReadBudgetContext(t, ctx)
			source, err := s.Create(ctx, CreateRequest{Path: "beads/source", Title: "Source", Body: "Original"})
			if err != nil {
				t.Fatal(err)
			}
			small := "Current"
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/source", Body: &small, ExpectedRevision: source.Revision}); err != nil {
				t.Fatal(err)
			}
			old, err := s.Create(ctx, CreateRequest{Path: "beads/target", Title: "Target", Body: strings.Repeat("x", 3<<20)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/target", Body: &small, ExpectedRevision: old.Revision}); err != nil {
				t.Fatal(err)
			}
			filler := plainIssue("Current bytes")
			filler.Issue.Description = strings.Repeat("f", 1<<20)
			fillerIssue, err := s.CreateIssue(ctx, "beads/filler", filler)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				request := LinkCreateRequest{Path: fmt.Sprintf("links/pin%d", i), SourcePath: "beads/source", TargetPath: "beads/target", TargetPin: EndpointPin{Revision: old.Revision}}
				request.SourcePin = EndpointPin{Revision: source.Revision}
				if _, err := s.AddInformationalLink(ctx, request); err != nil {
					t.Fatalf("writer-admitted repeated pin %d: %v", i, err)
				}
			}
			// This nonincident native Issue is outside selected Type validation,
			// while current acquisition reads both its live body and retained head.
			// Prove the old per-Link predicate's lower bound really exceeds 16 MiB.
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error {
				var repeated, live, retained uint64
				if err := tx.QueryRowContext(ctx, `SELECT CAST(COALESCE(SUM(OCTET_LENGTH(v.snapshot)),0) AS UNSIGNED) FROM graph_preview_links l JOIN graph_preview_versions v ON v.path=l.target_path AND v.version=l.target_revision WHERE l.target_revision<>''`).Scan(&repeated); err != nil {
					return err
				}
				if err := tx.QueryRowContext(ctx, "SELECT OCTET_LENGTH(description) FROM issues WHERE id=?", fillerIssue.Properties.ID).Scan(&live); err != nil {
					return err
				}
				if err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(v.durable_state) FROM graph_preview_issue_versions m JOIN graph_preview_catalog c ON c.path=m.path AND c.revision=m.version JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=?`, "beads/filler").Scan(&retained); err != nil {
					return err
				}
				t.Logf("old per-Link charge lower bound=%d; export limit=%d", repeated+live+retained, PreviewCurrentReadByteLimit)
				if repeated+live+retained <= PreviewCurrentReadByteLimit {
					return fmt.Errorf("fixture does not defeat old per-Link charge: %d", repeated+live+retained)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// Each independent export proof gets the existing bounded operation
			// window; fixture setup and earlier proofs must not consume it.
			ctx = currentReadBudgetContext(t, ctx)
			before := exportSourceState(t, ctx, s)
			got, err := s.ExportCurrentSnapshot(ctx)
			if err != nil || len(got.Snapshot.Records) != 8 {
				t.Fatalf("distinct decode falsely charged five times: %d %v", len(got.Snapshot.Records), err)
			}
			ctx = currentReadBudgetContext(t, ctx)
			fault := errors.New("after exact endpoint validation")
			s.afterWrite = func(stage string) error {
				if stage == "export-endpoints" {
					return fault
				}
				return nil
			}
			got, err = s.ExportCurrentSnapshot(ctx)
			s.afterWrite = nil
			if !errors.Is(err, fault) || !reflect.DeepEqual(got, ExportCurrentState{}) {
				t.Fatalf("endpoint-stage fault: %+v %v", got, err)
			}
			ctx = currentReadBudgetContext(t, ctx)
			canceled, cancel := context.WithCancel(ctx)
			cancellationReached := false
			s.afterWrite = func(stage string) error {
				if stage == "export-endpoints" {
					cancellationReached = true
					cancel()
				}
				return nil
			}
			got, err = s.ExportCurrentSnapshot(canceled)
			s.afterWrite = nil
			cancel()
			if !cancellationReached || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, ExportCurrentState{}) {
				t.Fatalf("endpoint-stage cancellation: reached=%t %+v %v", cancellationReached, got, err)
			}
			ctx = currentReadBudgetContext(t, ctx)
			reached, reachedPreflight := false, false
			s.afterWrite = func(stage string) error {
				if stage == "export-preflight" {
					reachedPreflight = true
				}
				if stage == "export-endpoints" {
					reached = true
				}
				return nil
			}
			// Deliberate corruption/unadmittable state: two distinct retained
			// payloads individually fit, but their combined validation does not.
			bounded := s.withTx(ctx, false, func(tx *sql.Tx) error {
				for _, address := range []struct{ path, revision string }{{"beads/source", source.Revision}, {"beads/target", old.Revision}} {
					if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_versions SET snapshot=REPEAT('x',?) WHERE path=? AND version=?", 9<<20, address.path, address.revision); err != nil {
						return err
					}
				}
				got, err := s.exportCurrentSnapshotInTx(ctx, tx)
				if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(got, ExportCurrentState{}) {
					return fmt.Errorf("distinct aggregate not bounded before decode: %+v %v", got, err)
				}
				return nil
			})
			s.afterWrite = nil
			if bounded != nil || reached || reachedPreflight {
				t.Fatalf("over-budget exact validation ran: endpoints=%t preflight=%t %v", reached, reachedPreflight, bounded)
			}
			if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
				t.Fatal("validation/fault/bounds changed source")
			}
		})
	}
}

// Deliberate oversized-context corruption robustness. Current state and each
// selected native edition fit separately; all acquired bytes share one budget.
func TestExportEndpointNativeContextAggregateBound(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options, s := memoryDeleteStore(t, backend)
			ctx = currentReadBudgetContext(t, ctx)
			t.Logf("isolated fixture workspace=%s", options.Binding.WorkspaceID)
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/source", Title: "Source"}); err != nil {
				t.Fatal(err)
			}
			old, err := s.CreateIssue(ctx, "beads/native", plainIssue("Old native"))
			if err != nil {
				t.Fatal(err)
			}
			title := "Current native"
			current, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/native", Title: &title, Actor: "test-author", ExpectedRevision: old.Revision})
			if err != nil {
				t.Fatal(err)
			}
			// The same exact native address occurs in both endpoint roles. Its
			// context must be charged once, not once per Link or endpoint role.
			for _, request := range []LinkCreateRequest{
				{Path: "links/target", SourcePath: "beads/source", TargetPath: "beads/native", TargetPin: EndpointPin{Revision: old.Revision}},
				{Path: "links/source", SourcePath: "beads/native", TargetPath: "beads/source", SourcePin: EndpointPin{Revision: old.Revision}},
			} {
				if _, err := s.AddInformationalLink(ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			filler := plainIssue("Current filler")
			filler.Issue.Description = strings.Repeat("f", 4<<20)
			if _, err := s.CreateIssue(ctx, "beads/filler", filler); err != nil {
				t.Fatal(err)
			}
			before := exportSourceState(t, ctx, s)
			for _, contextBytes := range []int{6 << 20, 9 << 20} {
				wantLimit := contextBytes == 9<<20
				reachedPreflight := false
				err := s.withTx(ctx, false, func(tx *sql.Tx) error {
					raw, err := canonicalJSON(issueRevisionContext{Inception: strings.Repeat("c", contextBytes)})
					if err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_issue_versions SET revision_context=? WHERE path=? AND version=?", raw, "beads/native", old.Revision); err != nil {
						return err
					}
					// Exercise both real individual readers before the aggregate
					// export. Neither may be the reason for the negative result.
					snapshot, err := s.currentSnapshotInTx(ctx, tx)
					if err != nil || len(snapshot.Records) != 5 {
						return fmt.Errorf("current state must fit individually: records=%d err=%v", len(snapshot.Records), err)
					}
					retained, err := s.readIssueVersionInTx(ctx, tx, "beads/native", old.Revision, current.Issue.Revision, old.Properties.ID)
					if err != nil || len(retained.Attribution.Inception) != contextBytes {
						return fmt.Errorf("selected native edition must fit individually: context=%d err=%v", len(retained.Attribution.Inception), err)
					}
					s.afterWrite = func(stage string) error {
						if stage == "export-preflight" {
							reachedPreflight = true
						}
						return nil
					}
					defer func() { s.afterWrite = nil }()
					got, err := s.exportCurrentSnapshotInTx(ctx, tx)
					if wantLimit {
						if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(got, ExportCurrentState{}) || reachedPreflight {
							return fmt.Errorf("aggregate native context must refuse before export-preflight: err=%v zero=%t reached=%t", err, reflect.DeepEqual(got, ExportCurrentState{}), reachedPreflight)
						}
					} else if err != nil || len(got.Snapshot.Records) != 5 || !reachedPreflight {
						return fmt.Errorf("deduplicated native context must fit: err=%v records=%d reached=%t", err, len(got.Snapshot.Records), reachedPreflight)
					}
					t.Logf("native context=%d: individual current/retained fit; aggregate refusal=%t", contextBytes, wantLimit)
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
					t.Fatal("export or rollback changed source state")
				}
			}
		})
	}
}

// Export validates a real native terminal D without acquiring its baseline.
// Legacy deletion instead reads its native head, whose context is already in
// the shared current budget and must not be charged a second time by export.
func TestExportNativeDeletionContextProjection(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options, s := memoryDeleteStore(t, backend)
			ctx = currentReadBudgetContext(t, ctx)
			t.Logf("isolated fixture workspace=%s", options.Binding.WorkspaceID)
			issue, err := s.CreateIssue(ctx, "beads/deleted-native", plainIssue("Deleted"))
			if err != nil {
				t.Fatal(err)
			}
			deleted, err := s.DeleteIssue(ctx, IssueDeleteRequest{Path: "beads/deleted-native", ExpectedRevision: issue.Revision, Actor: "test-author"})
			if err != nil || deleted.Terminal == nil {
				t.Fatalf("create real native D: %v", err)
			}
			filler := plainIssue("Current filler")
			filler.Issue.Description = strings.Repeat("f", 3<<20)
			if _, err := s.CreateIssue(ctx, "beads/filler", filler); err != nil {
				t.Fatal(err)
			}
			before := exportSourceState(t, ctx, s)
			for _, legacy := range []bool{false, true} {
				err := s.withTx(ctx, false, func(tx *sql.Tx) error {
					if legacy {
						// Controlled legacy-shape fixture; the real export path reads
						// this valid context once, alongside ~6 MiB of current body.
						if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_catalog SET revision=? WHERE path=?", issue.Revision, "beads/deleted-native"); err != nil {
							return err
						}
						if _, err := tx.ExecContext(ctx, "DELETE FROM graph_preview_versions WHERE path=? AND version=?", "beads/deleted-native", deleted.Terminal.Revision); err != nil {
							return err
						}
						raw, err := canonicalJSON(issueRevisionContext{Inception: strings.Repeat("c", 6<<20)})
						if err != nil {
							return err
						}
						if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_issue_versions SET revision_context=? WHERE path=? AND version=?", raw, "beads/deleted-native", issue.Revision); err != nil {
							return err
						}
					} else {
						// Deliberate unselected-history corruption. Neither its bytes
						// nor JSON are acquired while validating the real terminal D.
						if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_issue_versions SET revision_context=REPEAT('x',?) WHERE path=? AND version=?", 18<<20, "beads/deleted-native", issue.Revision); err != nil {
							return err
						}
					}
					got, err := s.exportCurrentSnapshotInTx(ctx, tx)
					if err != nil || len(got.Snapshot.Records) != 1 {
						return fmt.Errorf("native deletion projection legacy=%t: records=%d err=%v", legacy, len(got.Snapshot.Records), err)
					}
					t.Logf("native deletion legacy=%t: unacquired baseline excluded / acquired context charged once", legacy)
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, exportSourceState(t, ctx, s)) {
					t.Fatal("export or rollback changed native deletion source state")
				}
			}
		})
	}
}

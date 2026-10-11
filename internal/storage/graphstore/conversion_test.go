//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/schema"
	"github.com/steveyegge/beads/memoryops"
)

func nativeConversionFixture(t *testing.T) (context.Context, Options) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	o := testOptions(t)
	if err := os.Mkdir(o.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := openBackend(ctx, o, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE DATABASE `" + o.Database + "`")
	exec("USE `" + o.Database + "`")
	if _, err := schema.MigrateUp(ctx, conn); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO config (`key`,value) VALUES ('issue_prefix','preserved')")
	for _, id := range []string{"parent", "child", "solution", "next"} {
		exec(`INSERT INTO issues(id,title,description,design,acceptance_criteria,notes,status,priority,issue_type,created_at,created_by,updated_at,row_lock,metadata) VALUES (?,?,?,'design','acceptance','diagnosis','open',2,'task','2025-01-02 03:04:05','original-author','2025-02-03 04:05:06',42,'{"review":"verified"}')`, id, "Historical "+id, "Solved implementation")
	}
	exec("UPDATE issues SET status='closed',closed_at='2025-02-03 04:05:06',close_reason='Regression test verified',updated_at=updated_at WHERE id='solution'")
	exec("INSERT INTO labels(issue_id,label) VALUES ('solution','verified')")
	exec("INSERT INTO comments(id,issue_id,author,text) VALUES ('comment-preserved','solution','original-author','Original diagnostic comment')")
	for i, edge := range [][3]string{{"child", "parent", "parent-child"}, {"parent", "solution", "blocks"}, {"next", "solution", "discovered-from"}, {"next", "child", "related"}} {
		exec(`INSERT INTO dependencies(id,issue_id,depends_on_issue_id,type,created_at,created_by,metadata,thread_id) VALUES (?,?,?,?,'2025-02-03 04:05:06','original-author','{"cause":"diagnosis"}','thread-記憶')`, []string{"dep0", "dep1", "dep2", "dep3"}[i], edge[0], edge[1], edge[2])
	}
	exec("INSERT INTO events(id,issue_id,event_type,actor,old_value,new_value,comment,created_at) VALUES ('historical-event','solution','closed','original-author','open','closed','Historical regression verified','2025-02-03 04:05:06')")
	exec("UPDATE issues SET status='in_progress',assignee='existing-holder',updated_at=updated_at WHERE id='next'")
	exec("INSERT INTO leases(issue_id,holder,granted_at,lease_expires_at,heartbeat_at) VALUES ('next','existing-holder','2025-02-03 04:05:06','2099-02-03 04:05:06','2025-02-03 04:05:06')")
	exec("CREATE TABLE user_history (id VARCHAR(32) PRIMARY KEY, value TEXT)")
	exec("INSERT INTO user_history VALUES ('committed','original history')")
	for key, value := range map[string]string{"kv.memory.plain": "Saved solution — 記憶", "kv.memory.empty": "", "kv.memory.@knowledge/record/r1": "{\"status\":\"accepted\",\"scope\":\"tested\"}", "kv.memory.@knowledge/code/file": "exact derived blob", "kv.memory.@activity/event/1": "observed session, not acceptance"} {
		exec("INSERT INTO config (`key`,value) VALUES (?,?)", key, value)
	}
	exec("CALL DOLT_ADD('.')")
	exec("CALL DOLT_COMMIT('--author','Fixture <fixture@example.invalid>','-m','Historical baseline')")
	exec("CALL DOLT_BRANCH('feature')")
	exec("CALL DOLT_TAG('baseline')")
	exec("CREATE VIEW user_history_view AS SELECT id,value FROM user_history")
	exec("INSERT INTO user_history VALUES ('staged','staged work')")
	exec("CALL DOLT_ADD('.')")
	exec("INSERT INTO user_history VALUES ('working','uncommitted work')")
	if err := errors.Join(conn.Close(), s.Close()); err != nil {
		t.Fatal(err)
	}
	return ctx, o
}

func mutateNativeFixture(t *testing.T, ctx context.Context, o Options, query string) {
	t.Helper()
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.ExecContext(ctx, query)
	if err = errors.Join(err, s.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestConversionCheckpointAdoptsExactNativeState(t *testing.T) {
	ctx, o := nativeConversionFixture(t)
	plan, err := PlanConversion(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Resources != 11 || len(plan.HumanMemories) != 3 {
		t.Fatalf("incomplete inventory: %+v", plan)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backup, candidate := filepath.Join(root, "backup"), filepath.Join(root, "candidate")
	if err := CheckpointConversion(ctx, o, plan, backup, candidate); err != nil {
		t.Fatal(err)
	}
	co := o
	co.DataDir = candidate
	if err := AdoptConversionCandidate(ctx, co, plan, "conversion-operator"); err != nil {
		t.Fatal(err)
	}
	after, err := PlanConversion(ctx, o)
	if err != nil || !reflect.DeepEqual(plan, after) {
		t.Fatalf("original changed: %v", err)
	}
	s, err := OpenExisting(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	snapshot, err := s.CurrentSnapshot(ctx)
	if err != nil || len(snapshot.Records) != 11 {
		t.Fatalf("complete canonical snapshot: %d %v", len(snapshot.Records), err)
	}
	for key, value := range map[string]string{"plain": "Saved solution — 記憶", "empty": "", "@knowledge/record/r1": "{\"status\":\"accepted\",\"scope\":\"tested\"}", "@knowledge/code/file": "exact derived blob", "@activity/event/1": "observed session, not acceptance"} {
		got, err := s.Memories("reader").Recall(ctx, memoryops.RecallRequest{Key: key})
		if err != nil || !got.Found || got.Value != value {
			t.Fatalf("lost memory %q: %+v %v", key, got, err)
		}
	}
	solution, err := s.ShowIssue(ctx, "beads/solution")
	if err != nil {
		t.Fatal(err)
	}
	if solution.Properties.Status != "closed" || solution.Properties.CreatedBy != "original-author" || solution.Attribution.Actor != "conversion-operator" {
		t.Fatalf("native state/provenance rewritten: %+v", solution)
	}
	var rowLock int64
	if err := s.db.QueryRowContext(ctx, "SELECT row_lock FROM issues WHERE id='solution'").Scan(&rowLock); err != nil || rowLock != 42 {
		t.Fatalf("row_lock changed: %d %v", rowLock, err)
	}
	if err := s.withTx(ctx, false, func(tx *sql.Tx) error { return verifyConversionPreservation(ctx, tx, plan) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := AdoptConversionCandidate(ctx, co, plan, "conversion-operator"); err == nil {
		t.Fatal("repeat adoption allocated duplicate history")
	}
}

func TestConversionRefusesUnsupportedDataAndDrift(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"retained-table-collision", "CREATE TABLE issue_versions (issue_id VARCHAR(255) PRIMARY KEY)"},
		{"oversized-native", "UPDATE issues SET description=REPEAT('x',16777217),updated_at=updated_at WHERE id='parent'"},
		{"populated-native-field", "UPDATE issues SET hook_bead='prior-hook',updated_at=updated_at WHERE id='parent'"},
		{"unknown-relation", "UPDATE dependencies SET type='custom' WHERE id='dep0'"},
		{"unsafe-number", "UPDATE issues SET metadata='{\"n\":9007199254740993}',updated_at=updated_at WHERE id='parent'"},
		{"no-history", "UPDATE issues SET no_history=1,updated_at=updated_at WHERE id='parent'"},
		{"unknown-dependency-column", "ALTER TABLE dependencies ADD COLUMN unknown_payload TEXT"},
		{"unknown-column", "ALTER TABLE issues ADD COLUMN unknown_payload TEXT"},
		{"external-target", "UPDATE dependencies SET depends_on_issue_id=NULL,depends_on_external='external:project:capability' WHERE id='dep0'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, o := nativeConversionFixture(t)
			mutateNativeFixture(t, ctx, o, tc.query)
			if _, err := PlanConversion(ctx, o); err == nil {
				t.Fatal("unsupported data admitted")
			}
		})
	}
	t.Run("changed-source", func(t *testing.T) {
		ctx, o := nativeConversionFixture(t)
		plan, err := PlanConversion(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		mutateNativeFixture(t, ctx, o, "UPDATE issues SET title='changed',updated_at=updated_at WHERE id='parent'")
		root, _ := filepath.EvalSymlinks(t.TempDir())
		backup := filepath.Join(root, "backup")
		if err := CheckpointConversion(ctx, o, plan, backup, filepath.Join(root, "candidate")); !errors.Is(err, ErrConflict) {
			t.Fatalf("drift not refused: %v", err)
		}
		if _, err := os.Lstat(backup); !os.IsNotExist(err) {
			t.Fatal("stale plan created recovery material")
		}
	})
	t.Run("source-as-candidate", func(t *testing.T) {
		ctx, o := nativeConversionFixture(t)
		plan, err := PlanConversion(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		if err := AdoptConversionCandidate(ctx, o, plan, "operator"); err == nil || (!strings.Contains(err.Error(), "overlaps source") && !strings.Contains(err.Error(), "outside final workspace")) {
			t.Fatalf("in-place adoption admitted: %v", err)
		}
	})
}

func TestConversionSeedFailureRetainsOriginalAndRollsBackCandidate(t *testing.T) {
	ctx, o := nativeConversionFixture(t)
	plan, err := PlanConversion(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	candidate := filepath.Join(root, "candidate")
	if err := CheckpointConversion(ctx, o, plan, filepath.Join(root, "backup"), candidate); err != nil {
		t.Fatal(err)
	}
	co := o
	co.DataDir = candidate
	fault := errors.New("conversion interruption")
	err = adoptConversionCandidate(ctx, co, plan, "operator", func(stage string) error {
		if stage == "conversion-seeded" {
			return fault
		}
		return nil
	})
	if !errors.Is(err, fault) {
		t.Fatalf("fault not surfaced: %v", err)
	}
	got, err := PlanConversion(ctx, o)
	if err != nil || !reflect.DeepEqual(got, plan) {
		t.Fatalf("original changed: %v", err)
	}
	s, err := openBackend(ctx, co, co.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var allocations, versions, issues int
	if err := s.db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM graph_preview_catalog),(SELECT COUNT(*) FROM issue_versions),(SELECT COUNT(*) FROM issues)").Scan(&allocations, &versions, &issues); err != nil {
		t.Fatal(err)
	}
	if allocations != 0 || versions != 0 || issues != len(plan.IssueIDs) {
		t.Fatalf("partial seed committed: %d %d %d", allocations, versions, issues)
	}
	var body string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM config WHERE `key`='kv.memory.plain'").Scan(&body); err != nil || body != "Saved solution — 記憶" {
		t.Fatalf("pointer leaked on rollback: %q %v", body, err)
	}
}

func TestConversionResourceAndSeedByteLimitsPreserveOriginal(t *testing.T) {
	t.Run("2049", func(t *testing.T) {
		ctx, o := nativeConversionFixture(t)
		var rows []string
		for i := 0; i < 2038; i++ {
			rows = append(rows, fmt.Sprintf("('kv.memory.extra-%d','value')", i))
		}
		mutateNativeFixture(t, ctx, o, "INSERT INTO config (`key`,value) VALUES "+strings.Join(rows, ","))
		before, err := InspectConversionState(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := PlanConversion(ctx, o); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("2049 admitted: %v", err)
		}
		after, err := InspectConversionState(ctx, o)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("refused plan changed native state: %v", err)
		}
	})
	t.Run("seed-bytes", func(t *testing.T) {
		ctx, o := nativeConversionFixture(t)
		var rows []string
		for i := 0; i < 400; i++ {
			rows = append(rows, fmt.Sprintf("('kv.memory.large-%d',REPEAT('x',49152))", i))
		}
		mutateNativeFixture(t, ctx, o, "INSERT INTO config (`key`,value) VALUES "+strings.Join(rows, ","))
		plan, err := PlanConversion(ctx, o)
		if err != nil {
			t.Fatal(err)
		}
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		candidate := filepath.Join(root, "candidate")
		if err := CheckpointConversion(ctx, o, plan, filepath.Join(root, "backup"), candidate); err != nil {
			t.Fatal(err)
		}
		co := o
		co.DataDir = candidate
		if err := AdoptConversionCandidate(ctx, co, plan, "operator"); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("oversized seed admitted: %v", err)
		}
		original, err := PlanConversion(ctx, o)
		if err != nil || !reflect.DeepEqual(original, plan) {
			t.Fatalf("seed failure changed source: %v", err)
		}
		s, err := openBackend(ctx, co, co.Database)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		for _, table := range []string{"graph_preview_catalog", "issue_versions"} {
			var count int
			if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("partial %s count=%d err=%v", table, count, err)
			}
		}
		var prefix string
		if err := s.db.QueryRowContext(ctx, "SELECT LEFT(value,1) FROM config WHERE `key`='kv.memory.large-0'").Scan(&prefix); err != nil || prefix != "x" {
			t.Fatalf("seed failure changed original memory: %v", err)
		}
	})
}

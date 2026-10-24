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
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/legacyimport"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestLegacyImportPrefixAdmission(t *testing.T) {
	for _, id := range []string{"web-app-123", "web-app-a1b", "web-app-abc", "web-app-123.1.2"} {
		t.Run(id, func(t *testing.T) {
			batch := legacyimport.Batch{Issues: []*types.Issue{{ID: id, Title: "Imported"}}}
			if _, err := prepareLegacyImport(batch, "importer"); err != nil {
				t.Fatal(err)
			}
			if got := legacyImportPrefix(id); got != "web-app" {
				t.Fatalf("prefix=%q", got)
			}
		})
	}
	for _, ids := range [][]string{{"web-app-123", "web-api-456"}, {"web-app-"}, {"missing"}, {"-123"}} {
		t.Run(strings.Join(ids, ","), func(t *testing.T) {
			var batch legacyimport.Batch
			for _, id := range ids {
				batch.Issues = append(batch.Issues, &types.Issue{ID: id, Title: "Imported"})
			}
			if _, err := prepareLegacyImport(batch, "importer"); err == nil {
				t.Fatal("invalid or mixed inferred prefixes admitted")
			}
		})
	}
	for _, ids := range [][]string{{"web-app-123", "web-app-abcdef"}, {"web-app-abcdef", "web-app-123"}} {
		var batch legacyimport.Batch
		for _, id := range ids {
			batch.Issues = append(batch.Issues, &types.Issue{ID: id, Title: "Imported"})
		}
		prepared, err := prepareLegacyImportWithPrefix(batch, "importer", "")
		if err != nil || prepared.prefix != "web-app" {
			t.Fatalf("whole-batch prefix %v = %q, %v", ids, prepared.prefix, err)
		}
	}
	ambiguous := legacyimport.Batch{Issues: []*types.Issue{{ID: "web-app-abcdef", Title: "Imported"}}}
	if _, err := prepareLegacyImport(ambiguous, "importer"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix not refused: %v", err)
	}
	for _, prefix := range []string{"web-app", "web"} {
		prepared, err := prepareLegacyImportWithPrefix(ambiguous, "importer", prefix)
		if err != nil || prepared.prefix != prefix {
			t.Fatalf("explicit prefix %q = %q, %v", prefix, prepared.prefix, err)
		}
	}
	for _, prefix := range []string{"web-", " web-app", "web-app ", "other"} {
		if _, err := prepareLegacyImportWithPrefix(ambiguous, "importer", prefix); err == nil {
			t.Fatalf("invalid override %q admitted", prefix)
		}
	}
	if _, err := prepareLegacyImportWithPrefix(legacyimport.Batch{Memories: []legacyimport.Memory{{Key: "key"}}}, "importer", "web-app"); err == nil {
		t.Fatal("memory-only source prefix admitted")
	}
}

func TestLegacyImportLabelSetAndInternalState(t *testing.T) {
	issue := &types.Issue{ID: "old-a", Title: "Imported", Labels: []string{"one", "two", "one", "three"}}
	prepared, err := prepareLegacyImport(legacyimport.Batch{Issues: []*types.Issue{issue}}, "importer")
	if err != nil || !reflect.DeepEqual(prepared.Issues[0].Labels, []string{"one", "two", "three"}) || !reflect.DeepEqual(issue.Labels, []string{"one", "two", "one", "three"}) {
		t.Fatalf("label set or caller changed: %+v %+v %v", prepared, issue, err)
	}
	for field, internal := range legacyImportOmittedStateFixtures() {
		tag, ok := reflect.TypeOf(types.Issue{}).FieldByName(field)
		if !ok || tag.Tag.Get("json") != "-" {
			t.Fatalf("internal field %s entered legacy JSON: %v", field, tag)
		}
		internal.ID, internal.Title = "old-a", "Imported"
		if _, err := prepareLegacyImport(legacyimport.Batch{Issues: []*types.Issue{internal}}, "importer"); !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), field) {
			t.Fatalf("nonserialized state silently ignored: %+v", internal)
		}
	}
}

func legacyImportOmittedStateFixtures() map[string]*types.Issue {
	plane := false
	return map[string]*types.Issue{
		"WispPlaneOverride": {WispPlaneOverride: &plane}, "IDPrefix": {IDPrefix: "custom"}, "PrefixOverride": {PrefixOverride: "other"}, "SourceRepo": {SourceRepo: "another"}, "IsLitePartial": {IsLitePartial: true},
	}
}

func TestLegacyImportCommentBudgetAndOriginalUTF8(t *testing.T) {
	issue := &types.Issue{ID: "old-a", Title: "Imported"}
	for i := range LegacyImportCommentLimit {
		issue.Comments = append(issue.Comments, &types.Comment{ID: fmt.Sprint(i), Text: "Comment"})
	}
	batch := legacyimport.Batch{Issues: []*types.Issue{issue}}
	if _, err := prepareLegacyImport(batch, "importer"); err != nil {
		t.Fatal(err)
	}
	issue.Comments = append(issue.Comments, &types.Comment{ID: "one-too-many"})
	if _, err := prepareLegacyImport(batch, "importer"); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("comment ceiling: %v", err)
	}
	for _, invalid := range []*types.Issue{
		{ID: "old-a", Title: "bad\xff"},
		{ID: "old-a", Title: "Imported", Description: "bad\xff"},
		{ID: "old-a", Title: "Imported", Labels: []string{"bad\xff"}},
		{ID: "old-a", Title: "Imported", Comments: []*types.Comment{{Text: "bad\xff"}}},
		{ID: "old-a", Title: "Imported", Dependencies: []*types.Dependency{{DependsOnID: "bad\xff"}}},
		{ID: "old-a", Title: "Imported", Metadata: json.RawMessage("{\"text\":\"bad\xff\"}")},
		{ID: "old-a", Title: "Imported", Metadata: json.RawMessage("{\"bad\xff\":true}")},
	} {
		if _, err := prepareLegacyImport(legacyimport.Batch{Issues: []*types.Issue{invalid}}, "importer"); !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), "UTF-8") {
			t.Fatalf("invalid original UTF-8 was normalized instead of refused: %v", err)
		}
	}
}

func TestLegacyImportUnsupportedRecordState(t *testing.T) {
	if _, err := prepareLegacyImport(parseLegacyFixture(t, `{"id":"old-a","title":"Control"}`), "importer"); err != nil {
		t.Fatalf("valid prefixed control: %v", err)
	}
	for _, test := range []struct {
		name, fields, message string
	}{
		{"ephemeral", `"ephemeral":true`, "unsupported ephemeral, no-history, explicit storage-class or live lease data"},
		{"no-history", `"no_history":true`, "unsupported ephemeral, no-history, explicit storage-class or live lease data"},
		{"lease-expiry", `"lease_expires_at":"2099-01-01T00:00:00Z"`, "unsupported ephemeral, no-history, explicit storage-class or live lease data"},
		{"heartbeat", `"heartbeat_at":"2026-01-01T00:00:00Z"`, "unsupported ephemeral, no-history, explicit storage-class or live lease data"},
		{"lease-node", `"lease_granted_node":"previous-node"`, "unsupported ephemeral, no-history, explicit storage-class or live lease data"},
		{"tombstone", `"status":"tombstone"`, "tombstone old-a is unsupported"},
		{"absent-local-target", `"dependencies":[{"depends_on_id":"old-missing","type":"blocks"}]`, "dependency old-a -> old-missing does not name an Issue in this import"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := `{"id":"old-a","title":"Refused",` + test.fields + `}`
			_, err := prepareLegacyImport(parseLegacyFixture(t, input), "importer")
			if !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("wrong refusal for %s: %v", test.name, err)
			}
		})
	}
}

// An ordinary export can contain nonblocking and custom native Dependency
// kinds. Import must preserve that kind rather than reject or reinterpret it.
func TestLegacyImportDependencyKinds(t *testing.T) {
	kinds := append(types.WellKnownDependencyTypes(), types.DependencyType("custom-kind"), types.DependencyType(strings.Repeat("x", types.MaxDependencyTypeLen)))
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			dep := &types.Dependency{IssueID: "scratch-35t", DependsOnID: "scratch-8b8", Type: kind, Metadata: "{}"}
			input := legacyimport.Batch{Issues: []*types.Issue{
				{ID: "scratch-35t", Title: "Source", Dependencies: []*types.Dependency{dep}},
				{ID: "scratch-8b8", Title: "Target"},
			}}
			prepared, err := prepareLegacyImport(input, "importer")
			if err != nil {
				t.Fatalf("ordinary native Dependency %q refused: %v", kind, err)
			}
			if got := prepared.Issues[0].Dependencies[0]; got.Type != kind || got.DependsOnID != dep.DependsOnID || got.IssueID != dep.IssueID {
				t.Fatalf("native dependency changed: got %+v, want %+v", got, dep)
			}
		})
	}
	for _, kind := range []types.DependencyType{"", types.DependencyType(strings.Repeat("x", types.MaxDependencyTypeLen+1))} {
		input := legacyimport.Batch{Issues: []*types.Issue{
			{ID: "scratch-35t", Title: "Source", Dependencies: []*types.Dependency{{IssueID: "scratch-35t", DependsOnID: "scratch-8b8", Type: kind}}},
			{ID: "scratch-8b8", Title: "Target"},
		}}
		if _, err := prepareLegacyImport(input, "importer"); !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), "dependency kind") {
			t.Fatalf("wrong refusal for invalid native kind %q: %v", kind, err)
		}
	}
}

func TestLegacyImportMemoryPaths(t *testing.T) {
	// Previously encoded, hashed or renamed keys now have an explicit
	// compatibility disposition. Accepted canonical spellings stay exact.
	for _, key := range []string{"safe", "has%20space", "%E9%9B%AA", "a/b", "Case", "case", "beads/literal", strings.Repeat("long", 300)} {
		prepared, err := prepareLegacyImportWithPrefix(legacyimport.Batch{Memories: []legacyimport.Memory{{Key: key}}}, "importer", "")
		if err != nil {
			t.Fatal(err)
		}
		mounted, err := mountLegacyImport(prepared, "")
		if err != nil || mounted.memoryIDs[key] != key {
			t.Fatalf("canonical key %q changed: %+v %v", key, mounted, err)
		}
	}
	for _, key := range []string{"old-a", "has space", "雪", "%", ".", "..", "a//b", "back\\slash", "control\x00", " leading "} {
		input := legacyimport.Batch{Issues: []*types.Issue{{ID: "old-a", Title: "Issue"}}, Memories: []legacyimport.Memory{{Key: key}}}
		prepared, err := prepareLegacyImportWithPrefix(input, "importer", "old")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mountLegacyImport(prepared, ""); !errors.Is(err, storage.ErrValidation) {
			t.Fatalf("incompatible or colliding key %q admitted: %v", key, err)
		}
	}
	duplicate := legacyimport.Batch{Memories: []legacyimport.Memory{{Key: "safe"}, {Key: "safe", Value: "different"}}}
	if _, err := prepareLegacyImport(duplicate, "importer"); err == nil {
		t.Fatal("duplicate memory key admitted")
	}
	for _, memory := range []legacyimport.Memory{{Key: ""}, {Key: "bad\xff"}, {Key: "valid", Value: "bad\xff"}} {
		if _, err := prepareLegacyImport(legacyimport.Batch{Memories: []legacyimport.Memory{memory}}, "importer"); err == nil {
			t.Fatalf("invalid memory admitted: %+v", memory)
		}
	}
}

func TestLegacyImportCommentVerification(t *testing.T) {
	want := &types.Issue{ID: "old-a", Title: "Issue"}
	got := &types.Issue{ID: "old-a", Title: "Issue"}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 10000 {
		c := types.Comment{ID: fmt.Sprintf("comment-%d", i), IssueID: want.ID, Author: "author", Text: "text", CreatedAt: stamp}
		want.Comments = append(want.Comments, &c)
		copy := c
		got.Comments = append(got.Comments, &copy)
	}
	// Missing timestamps stay wildcards; native creation has already assigned
	// IDs before verification. Stored feed ordering is irrelevant to identity.
	want.Comments[0].CreatedAt = time.Time{}
	for i, j := 0, len(got.Comments)-1; i < j; i, j = i+1, j-1 {
		got.Comments[i], got.Comments[j] = got.Comments[j], got.Comments[i]
	}
	if err := verifyLegacyIssue(want, got); err != nil {
		t.Fatal(err)
	}
	got.Comments[0].Text = "changed"
	if err := verifyLegacyIssue(want, got); err == nil {
		t.Fatal("changed comment text admitted")
	}
}

// Benchmark helper verification separately from native persistence. Sizes up
// to the admitted ceiling make verification cost observable without timing
// assertions that depend on the test machine or database engine.
func BenchmarkLegacyImportCommentVerification(b *testing.B) {
	for _, count := range []int{10, 100, LegacyImportCommentLimit} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			want := &types.Issue{ID: "old-a", Title: "Issue"}
			got := &types.Issue{ID: "old-a", Title: "Issue"}
			stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			for i := range count {
				c := types.Comment{ID: fmt.Sprint(i), IssueID: want.ID, Author: "author", Text: "text", CreatedAt: stamp}
				want.Comments = append(want.Comments, &c)
				copy := c
				got.Comments = append(got.Comments, &copy)
			}
			for i, j := 0, len(got.Comments)-1; i < j; i, j = i+1, j-1 {
				got.Comments[i], got.Comments[j] = got.Comments[j], got.Comments[i]
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := verifyLegacyIssue(want, got); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

const legacyImportFixture = `{"_type":"issue","id":"old-a","title":"Prerequisite","priority":0,"status":"closed","issue_type":"task","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","closed_at":"2026-01-02T00:00:00Z","close_reason":"done","created_by":"original","owner":"owner@example.invalid"}
{"_type":"issue","id":"old-b","title":"Work","priority":2,"status":"open","issue_type":"bug","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","description":"A body","design":"Design","acceptance_criteria":"Tests","notes":"Notes","labels":["two","one"],"metadata":{"custom":{"flag":true}},"dependencies":[{"issue_id":"old-b","depends_on_id":"old-a","type":"blocks","created_at":"2026-01-01T00:00:00Z","created_by":"original","metadata":"{ }"}],"comments":[{"id":"old-comment","issue_id":"old-b","author":"original","text":"A note","created_at":"2026-01-01T00:00:00Z"}]}
{"_type":"memory","key":"context","value":"A remembered fact"}
`

func parseLegacyFixture(t *testing.T, input string) legacyimport.Batch {
	t.Helper()
	batch, err := legacyimport.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func legacyImportState(t *testing.T, ctx context.Context, s *Store) map[string]string {
	t.Helper()
	state := reopenState(t, ctx, s)
	for _, table := range []string{"graph_preview_types", "labels", "comments", "child_counters", "wisps", "wisp_dependencies", "wisp_comments", "wisp_labels", "wisp_events", "wisp_child_counters", "provenance_events", "issue_snapshots", "compaction_snapshots", "issue_counter", "epoch_minted_addresses", "store_epoch", "interactions", "config"} {
		rows, err := s.db.QueryContext(ctx, "SELECT * FROM "+table)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var values []string
		for rows.Next() {
			fields := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range fields {
				pointers[i] = &fields[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, string(raw))
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			t.Fatal(err)
		}
		sort.Strings(values)
		state[table] = strings.Join(values, "\n")
	}

	var token string
	if err := s.db.QueryRowContext(ctx, `SELECT writer_token FROM graph_preview_scope`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	state["writer_token"] = token
	var prefix string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM config WHERE `key`='issue_prefix'").Scan(&prefix); err != nil {
		t.Fatal(err)
	}
	state["issue_prefix"] = prefix
	return state
}

// Controlled contamination is confined to disposable test stores. Foreign-key
// checks are disabled on one borrowed connection only for orphan fixtures and
// restored before that connection returns to the pool.
func legacyImportContaminate(ctx context.Context, s *Store, query string, orphan bool) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	if orphan {
		if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
			return err
		}
		defer func() {
			restore, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, resetErr := conn.ExecContext(restore, "SET FOREIGN_KEY_CHECKS=1")
			if resetErr != nil {
				// A failed reset must not return a permissive session to the pool.
				discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
				resetErr = errors.Join(resetErr, discardErr)
			}
			err = errors.Join(err, resetErr)
		}()
	}
	_, err = conn.ExecContext(ctx, query)
	return err
}

func TestLegacyImportFreshDataPlanes(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			// Initialization and earlier probes must not consume later operation
			// or verification budgets. Preserve values but renew each bound.
			operationContext := func(t *testing.T) context.Context {
				t.Helper()
				operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
				t.Cleanup(cancel)
				return operation
			}
			s, err := OpenExisting(operationContext(t), options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			batch := parseLegacyFixture(t, legacyImportFixture)
			var epochSeed int
			if err := s.db.QueryRowContext(operationContext(t), "SELECT COUNT(*) FROM store_epoch").Scan(&epochSeed); err != nil {
				t.Fatal(err)
			}
			var changedEpoch int
			if err := s.db.QueryRowContext(operationContext(t), legacyFreshnessChangedEpochQuery).Scan(&changedEpoch); err != nil || changedEpoch != 0 {
				t.Fatalf("fresh Init has nonpristine epoch state: count=%d err=%v", changedEpoch, err)
			}
			restoreEpoch := "DELETE FROM store_epoch WHERE id=1"
			if epochSeed != 0 {
				restoreEpoch = "UPDATE store_epoch SET epoch=1,bumped_at=NULL,bumped_reason=NULL WHERE id=1"
			}
			cases := []struct {
				name, insert, remove, message string
				orphan                        bool
			}{
				{"issue", "INSERT INTO issues(id,title,description,design,acceptance_criteria,notes,created_at,updated_at) VALUES('retained-issue','Retained','','','','','2026-01-01','2026-01-01')", "DELETE FROM issues WHERE id='retained-issue'", "issues", false},
				{"dependency", "INSERT INTO dependencies(id,issue_id,depends_on_external,created_by) VALUES('orphan-native-dep','absent','external:target','previous-author')", "DELETE FROM dependencies WHERE id='orphan-native-dep'", "dependencies", true},
				{"comment", "INSERT INTO comments(id,issue_id,author,text) VALUES('orphan-native-comment','absent','previous-author','retained')", "DELETE FROM comments WHERE id='orphan-native-comment'", "comments", true},
				{"wisp", "INSERT INTO wisps(id,title,description,design,acceptance_criteria,notes,created_at,updated_at) VALUES('retained-wisp','Retained','','','','','2026-01-01','2026-01-01')", "DELETE FROM wisps WHERE id='retained-wisp'", "wisps", false},
				{"issue-version", "INSERT INTO issue_versions(issue_id,revision,epoch,durable_state,change_at,attribution_status) VALUES('absent',1,1,'{}','2026-01-01','supplied')", "DELETE FROM issue_versions WHERE issue_id='absent' AND revision=1", "issue_versions", false},
				{"graph-version", "INSERT INTO graph_preview_versions(path,version,snapshot,actor,ordinal,change_at) VALUES('beads/orphan','0123456789abcdef0123456789abcdef','{}','previous-author',1,'2026-01-01')", "DELETE FROM graph_preview_versions WHERE path='beads/orphan'", "graph_preview_versions", false},
				{"graph-issue-version", "INSERT INTO graph_preview_issue_versions(path,version,issue_id,issue_revision,owned) VALUES('beads/orphan','0123456789abcdef0123456789abcdef','absent',1,'[]')", "DELETE FROM graph_preview_issue_versions WHERE path='beads/orphan'", "graph_preview_issue_versions", false},
				{"catalog", "INSERT INTO graph_preview_catalog(path,resource_kind,type_url,revision,allocation_state,backing) VALUES('beads/orphan','bead','https://example.invalid/types/preview-memory-v2','0123456789abcdef0123456789abcdef','live','memory')", "DELETE FROM graph_preview_catalog WHERE path='beads/orphan'", "graph_preview_catalog", false},
				{"payload", "INSERT INTO graph_preview_payloads(path,properties) VALUES('beads/orphan','{}')", "DELETE FROM graph_preview_payloads WHERE path='beads/orphan'", "graph_preview_payloads", false},
				{"link", "INSERT INTO graph_preview_links(path,source_path,target_path,properties,attribution) VALUES('links/orphan','beads/absent','beads/absent','{}','{}')", "DELETE FROM graph_preview_links WHERE path='links/orphan'", "graph_preview_links", false},
				{"label", "INSERT INTO labels(issue_id,label) VALUES('absent','retained')", "DELETE FROM labels WHERE issue_id='absent'", "labels", true},
				{"wisp-dependency", "INSERT INTO wisp_dependencies(id,issue_id,depends_on_external) VALUES('orphan-dep','absent','external:target')", "DELETE FROM wisp_dependencies WHERE id='orphan-dep'", "wisp_dependencies", true},
				{"wisp-comment", "INSERT INTO wisp_comments(id,issue_id,text) VALUES('orphan-comment','absent','retained')", "DELETE FROM wisp_comments WHERE id='orphan-comment'", "wisp_comments", true},
				{"wisp-label", "INSERT INTO wisp_labels(issue_id,label) VALUES('absent','retained')", "DELETE FROM wisp_labels WHERE issue_id='absent'", "wisp_labels", true},
				{"interaction", "INSERT INTO interactions(id,kind,created_at,issue_id,actor) VALUES('orphan-interaction','tool','2026-01-01','old-b','previous-author')", "DELETE FROM interactions WHERE id='orphan-interaction'", "interactions", false},
				{"event", "INSERT INTO events(id,issue_id,event_type,actor) VALUES('orphan-event','old-b','created','previous-author')", "DELETE FROM events WHERE id='orphan-event'", "events", true},
				{"provenance", "INSERT INTO provenance_events(id,issue_id,kind,source) VALUES('orphan-provenance','old-b','commit','previous-source')", "DELETE FROM provenance_events WHERE id='orphan-provenance'", "provenance_events", true},
				{"counter", "INSERT INTO child_counters(parent_id,last_child) VALUES('old-b',42)", "DELETE FROM child_counters WHERE parent_id='old-b'", "child_counters", true},
				{"issue-snapshot", "INSERT INTO issue_snapshots(id,issue_id,snapshot_time,compaction_level,original_size,compressed_size,original_content) VALUES('orphan-snapshot','old-b','2026-01-01',1,1,1,'previous content')", "DELETE FROM issue_snapshots WHERE id='orphan-snapshot'", "issue_snapshots", true},
				{"compaction-snapshot", "INSERT INTO compaction_snapshots(id,issue_id,compaction_level,snapshot_json) VALUES('orphan-compaction','old-b',1,'{}')", "DELETE FROM compaction_snapshots WHERE id='orphan-compaction'", "compaction_snapshots", true},
				{"wisp-event", "INSERT INTO wisp_events(id,issue_id,event_type) VALUES('orphan-wisp-event','absent','created')", "DELETE FROM wisp_events WHERE id='orphan-wisp-event'", "wisp_events", true},
				{"wisp-counter", "INSERT INTO wisp_child_counters(parent_id,last_child) VALUES('absent',42)", "DELETE FROM wisp_child_counters WHERE parent_id='absent'", "wisp_child_counters", true},
				{"journal", "INSERT INTO bd_events_journal(seq,ts,op,issue_id,issue_json) VALUES(42,'2026-01-01','create','old-b','{}')", "DELETE FROM bd_events_journal WHERE seq=42", "bd_events_journal", false},
				{"lease", "INSERT INTO leases(issue_id,holder,granted_at,lease_expires_at,heartbeat_at) VALUES('absent','previous-holder','2026-01-01','2099-01-01','2026-01-01')", "DELETE FROM leases WHERE issue_id='absent'", "leases", false},
				{"allocator", "INSERT INTO issue_counter(prefix,last_id) VALUES('old',42)", "DELETE FROM issue_counter WHERE prefix='old'", "issue_counter", false},
				{"journal-sequence", "UPDATE bd_events_seq SET next_seq=42 WHERE id=0", "UPDATE bd_events_seq SET next_seq=0 WHERE id=0", "bd_events_seq", false},
				{"epoch-address", "INSERT INTO epoch_minted_addresses(address,store_id,minted_id,minted_epoch,minted_at) VALUES('previous-address','previous-store','old-b',1,'2026-01-01')", "DELETE FROM epoch_minted_addresses WHERE address='previous-address'", "epoch_minted_addresses", false},
				{"epoch-generation", "INSERT INTO store_epoch(id,epoch) VALUES(1,2) ON DUPLICATE KEY UPDATE epoch=2", restoreEpoch, "store_epoch", false},
				{"epoch-bump-time", "INSERT INTO store_epoch(id,epoch,bumped_at) VALUES(1,1,'2026-01-01') ON DUPLICATE KEY UPDATE bumped_at='2026-01-01'", restoreEpoch, "store_epoch", false},
				{"epoch-bump-reason", "INSERT INTO store_epoch(id,epoch,bumped_reason) VALUES(1,1,'') ON DUPLICATE KEY UPDATE bumped_reason=''", restoreEpoch, "store_epoch", false},
				{"kv-memory", "INSERT INTO config (`key`,value) VALUES('kv.memory.retained','original')", "DELETE FROM config WHERE `key`='kv.memory.retained'", "legacy KV Memories", false},
				{"empty-kv-memory", "INSERT INTO config (`key`,value) VALUES('kv.memory.retained','')", "DELETE FROM config WHERE `key`='kv.memory.retained'", "legacy KV Memories", false},
			}
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					// Register before insertion so Fatal and an expired case still
					// remove its fixture using a separate, live cleanup context.
					t.Cleanup(func() {
						cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
						defer stop()
						if _, err := s.db.ExecContext(cleanup, test.remove); err != nil {
							t.Errorf("remove %s fixture: %v", test.name, err)
						}
					})
					if err := legacyImportContaminate(operationContext(t), s, test.insert, test.orphan); err != nil {
						t.Fatal(err)
					}
					before := legacyImportState(t, operationContext(t), s)
					for _, dry := range []bool{true, false} {
						t.Run(fmt.Sprintf("dry=%t", dry), func(t *testing.T) {
							got, err := s.ImportLegacy(operationContext(t), batch, "importer", dry)
							message := "; " + test.message + " is occupied"
							if strings.Contains(test.message, "KV Memories") {
								message = test.message + " are present in config"
							}
							if !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), message) || !reflect.DeepEqual(got, LegacyImportResult{}) {
								t.Fatalf("occupied plane admitted or result leaked: %+v %v", got, err)
							}
							if !reflect.DeepEqual(before, legacyImportState(t, operationContext(t), s)) {
								t.Fatal("refusal changed existing rows, prefix or writer token")
							}
						})
					}
				})
			}
			t.Run("pristine-epoch-seed", func(t *testing.T) {
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					defer stop()
					if _, err := s.db.ExecContext(cleanup, restoreEpoch); err != nil {
						t.Error(err)
					}
				})
				if _, err := s.db.ExecContext(operationContext(t), "INSERT IGNORE INTO store_epoch(id,epoch) VALUES(1,1)"); err != nil {
					t.Fatal(err)
				}
				before := legacyImportState(t, operationContext(t), s)
				if _, err := s.ImportLegacy(operationContext(t), batch, "importer", true); err != nil {
					t.Fatalf("pristine epoch seed disqualified fresh destination: %v", err)
				}
				if !reflect.DeepEqual(before, legacyImportState(t, operationContext(t), s)) {
					t.Fatal("dry-run changed pristine epoch seed")
				}
			})
			// Similar configuration keys are not Memories. Ordinary config does
			// not disqualify the fresh destination or get overwritten by dry-run.
			t.Run("ordinary-config", func(t *testing.T) {
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					defer stop()
					if _, err := s.db.ExecContext(cleanup, "DELETE FROM config WHERE `key` IN ('kv.memoryx.retained','iris.test.setting')"); err != nil {
						t.Errorf("remove ordinary config fixtures: %v", err)
					}
				})
				if _, err := s.db.ExecContext(operationContext(t), "INSERT INTO config (`key`,value) VALUES('kv.memoryx.retained','ordinary config'),('iris.test.setting','kept')"); err != nil {
					t.Fatal(err)
				}
				before := legacyImportState(t, operationContext(t), s)
				if _, err := s.ImportLegacy(operationContext(t), batch, "importer", true); err != nil {
					t.Fatalf("ordinary configuration disqualified fresh destination: %v", err)
				}
				if !reflect.DeepEqual(before, legacyImportState(t, operationContext(t), s)) {
					t.Fatal("dry-run changed ordinary configuration")
				}
				if _, err := s.ImportLegacy(operationContext(t), batch, "importer", false); err != nil {
					t.Fatalf("ordinary configuration disqualified apply: %v", err)
				}
				for key, want := range map[string]string{"kv.memoryx.retained": "ordinary config", "iris.test.setting": "kept", "issue_prefix": "old"} {
					var value string
					if err := s.db.QueryRowContext(operationContext(t), "SELECT value FROM config WHERE `key`=?", key).Scan(&value); err != nil || value != want {
						t.Fatalf("apply changed allowed configuration %q: %q %v", key, value, err)
					}
				}
			})
		})
	}
}

// Parent-child admission reaches native hierarchy validation; its refusal
// must remain a data error with no partial import, not an unavailable route.
func TestLegacyImportNativeHierarchyAndKinds(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			store, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			conflict := parseLegacyFixture(t, `{"id":"old-a","title":"Ancestor"}
{"id":"old-b","title":"Child","dependencies":[{"depends_on_id":"old-a","type":"parent-child"}]}
{"id":"old-c","title":"Grandchild","dependencies":[{"depends_on_id":"old-b","type":"parent-child"},{"depends_on_id":"old-a","type":"blocks"}]}`)
			before := legacyImportState(t, ctx, store)
			for _, dryRun := range []bool{true, false} {
				result, err := store.ImportLegacy(ctx, conflict, "importer", dryRun)
				if !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), "ancestor") {
					t.Fatalf("hierarchy conflict must be a validation refusal: result=%+v error=%v", result, err)
				}
				if !reflect.DeepEqual(result, LegacyImportResult{}) || !reflect.DeepEqual(before, legacyImportState(t, ctx, store)) {
					t.Fatal("hierarchy refusal returned partial results or changed data")
				}
			}
			valid := parseLegacyFixture(t, `{"id":"old-a","title":"Child","dependencies":[{"depends_on_id":"old-b","type":"parent-child"},{"depends_on_id":"old-c","type":"blocks"}]}
{"id":"old-b","title":"Parent"}
{"id":"old-c","title":"Other","dependencies":[{"depends_on_id":"old-b","type":"custom-kind"}]}`)
			result, err := store.ImportLegacy(ctx, valid, "importer", false)
			if err != nil {
				t.Fatal(err)
			}
			for pair, kind := range map[string]string{
				"old-a -> old-b": "parent-child", "old-a -> old-c": "blocks", "old-c -> old-b": "custom-kind",
			} {
				identity, err := store.ResolveCanonicalPublicResourceIdentity(ctx, result.DependencyIDs[pair])
				if err != nil {
					t.Fatal(err)
				}
				link, err := store.ShowLink(ctx, identity.PrivatePath)
				base, classification, concrete := NativeTypeClassification(store.ScopeURL(), link.Type)
				if err != nil || !concrete || base != DependencyTypeURL(store.ScopeURL()) || classification != kind || link.Properties["dep_type"] != kind {
					t.Fatalf("native kind %s not preserved: %+v, %v", pair, link, err)
				}
			}
		})
	}
}

func TestLegacyImportAtomicityAndPersistence(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, backend)
			// Initialization's deadline must not become the cumulative fault-matrix
			// budget. Each operation and rollback inspection gets a fresh bound;
			// WithoutCancel retains the initialization context's values.
			operationContext := func(t *testing.T) context.Context {
				t.Helper()
				operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
				t.Cleanup(cancel)
				return operation
			}
			s, err := OpenExisting(operationContext(t), o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			state := func(t *testing.T) map[string]string {
				t.Helper()
				return legacyImportState(t, operationContext(t), s)
			}
			batch := parseLegacyFixture(t, legacyImportFixture)
			before := state(t)
			// A matching prefix update may report zero changed rows. It still
			// validates and dry-run must preserve the configuration.
			samePrefix := strings.ReplaceAll(legacyImportFixture, "old-", before["issue_prefix"]+"-")
			if _, err := s.ImportLegacy(operationContext(t), parseLegacyFixture(t, samePrefix), "importer", true); err != nil {
				t.Fatalf("same-prefix dry-run: %v", err)
			}
			if !reflect.DeepEqual(before, state(t)) {
				t.Fatal("same-prefix dry-run changed configuration")
			}
			for _, dry := range []bool{false, true} {
				t.Run(fmt.Sprintf("missing-prefix/dry=%t", dry), func(t *testing.T) {
					restorePrefix := func() {
						if _, err := s.db.ExecContext(operationContext(t), "INSERT INTO config (`key`,value) VALUES ('issue_prefix',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", before["issue_prefix"]); err != nil {
							t.Fatal(err)
						}
					}
					t.Cleanup(restorePrefix)
					if _, err := s.db.ExecContext(operationContext(t), "DELETE FROM config WHERE `key`='issue_prefix'"); err != nil {
						t.Fatal(err)
					}
					// Binding validation reads the prefix first through ReadConfigPrefix,
					// which classifies absence as not initialized. checkBinding keeps
					// ErrInvalidStore and the configuration diagnostic, not sql.ErrNoRows.
					// The import-specific inspection helper's raw SQL cause contract is
					// covered separately in TestLegacyImportInterruptedInspection.
					var writeStages []string
					previousHook := s.afterWrite
					s.afterWrite = func(stage string) error {
						writeStages = append(writeStages, stage)
						return errors.New("missing-prefix refusal reached a write stage")
					}
					t.Cleanup(func() { s.afterWrite = previousHook })
					result, err := s.ImportLegacy(operationContext(t), batch, "importer", dry)
					s.afterWrite = previousHook
					if !errors.Is(err, ErrInvalidStore) || !strings.Contains(err.Error(), "Issue configuration:") || !strings.Contains(err.Error(), "issue_prefix config is missing") || !reflect.DeepEqual(result, LegacyImportResult{}) || len(writeStages) != 0 {
						t.Fatalf("missing prefix lost binding diagnostic, reached writes or leaked a result: result=%+v stages=%v err=%v", result, writeStages, err)
					}
					// Restore the deliberately removed prerequisite before comparing
					// all other planes, including transaction coordination writes.
					restorePrefix()
					if !reflect.DeepEqual(before, state(t)) {
						t.Fatal("missing-prefix refusal left database changes")
					}
				})
			}
			for _, stage := range []string{"coordination", "import-prefix", "import-native", "import-issues", "import-links", "source-catalog", "source-retained", "allocation", "payload", "retained", "import-retained"} {
				t.Run(stage, func(t *testing.T) {
					fault := errors.New("injected late import failure")
					previousHook := s.afterWrite
					t.Cleanup(func() { s.afterWrite = previousHook })
					s.afterWrite = func(at string) error {
						if at == stage {
							return fault
						}
						return nil
					}
					_, err := s.ImportLegacy(operationContext(t), batch, "importer", false)
					s.afterWrite = previousHook
					if !errors.Is(err, fault) {
						t.Fatalf("stage %s: %v", stage, err)
					}
					if !reflect.DeepEqual(before, state(t)) {
						t.Fatal("failed import left database changes")
					}
				})
			}
			for _, dry := range []bool{false, true} {
				for _, deadline := range []bool{false, true} {
					t.Run(fmt.Sprintf("dry=%t/late-context-deadline=%t", dry, deadline), func(t *testing.T) {
						var operation context.Context
						var cancel context.CancelFunc
						var expected error
						if deadline {
							operation, cancel = context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
							expected = context.DeadlineExceeded
						} else {
							operation, cancel = context.WithCancel(operationContext(t))
							expected = context.Canceled
						}
						defer cancel()
						reached := false
						previousHook := s.afterWrite
						t.Cleanup(func() { s.afterWrite = previousHook })
						s.afterWrite = func(stage string) error {
							if stage == "import-retained" {
								reached = true
								if deadline {
									<-operation.Done()
								} else {
									cancel()
								}
							}
							return nil
						}
						failed, err := s.ImportLegacy(operation, batch, "importer", dry)
						s.afterWrite = previousHook
						// Inspect through the independent live context, after driver
						// cancellation and transaction cleanup have completed.
						// A driver watcher may already have rolled back the transaction.
						// ErrTxDone cleanup must keep outcome_unknown precedence;
						// other driver cleanup errors may also be uncertain.
						if errors.Is(err, ErrLimitExceeded) {
							t.Fatalf("cancellation was mislabeled as a size overflow: %v", err)
						}
						unknown := errors.Is(err, ErrOutcomeUnknown)
						alreadyDone := errors.Is(err, sql.ErrTxDone)
						if !reached || !errors.Is(err, expected) || (alreadyDone && !unknown) || !reflect.DeepEqual(failed, LegacyImportResult{}) || !reflect.DeepEqual(before, state(t)) {
							t.Fatalf("late context classification or rollback failed: reached=%v unknown=%v alreadyDone=%v err=%v", reached, unknown, alreadyDone, err)
						}
					})
				}
			}
			preview, err := s.ImportLegacy(operationContext(t), batch, "importer", true)
			if err != nil || !preview.DryRun {
				t.Fatalf("dry run: %+v %v", preview, err)
			}
			if !reflect.DeepEqual(before, state(t)) {
				t.Fatal("dry run changed graph")
			}
			result, err := s.ImportLegacy(operationContext(t), batch, "importer", false)
			if err != nil {
				t.Fatal(err)
			}
			if result.Issues != 2 || result.Memories != 1 || result.Dependencies != 1 || result.Comments != 1 {
				t.Fatalf("result=%+v", result)
			}
			b, err := s.ShowIssue(operationContext(t), "beads/old-b")
			if err != nil || len(b.Owned) != 1 || b.Properties.ID != "old-b" {
				t.Fatalf("issue=%+v err=%v", b, err)
			}
			comments, err := s.IssueComments(operationContext(t), "beads/old-b")
			if err != nil || len(comments) != 1 || comments[0].ID != "old-comment" {
				t.Fatalf("comments=%+v err=%v", comments, err)
			}
			_, versions, err := s.Versions(operationContext(t), "beads/old-b")
			if err != nil || len(versions) != 1 {
				t.Fatalf("initial versions=%+v %v", versions, err)
			}
			if b.Attribution.Inception != "" || b.Attribution.HistoricalUpdate != nil {
				t.Fatalf("legacy import invented operation context: %+v", b.Attribution)
			}
			retained, err := s.ReadVersion(operationContext(t), "beads/old-b", versions[0].Version)
			if err != nil {
				t.Fatal(err)
			}
			if got := retained.(IssueRecord); got.ID != b.ID || got.Revision != b.Revision || !reflect.DeepEqual(got.Attribution, b.Attribution) {
				t.Fatalf("legacy initial retained context differs: %+v", got.Attribution)
			}
			var absent bool
			if err := s.db.QueryRowContext(operationContext(t), `SELECT revision_context IS NULL FROM graph_preview_issue_versions WHERE path=? AND version=?`, "beads/old-b", b.Revision).Scan(&absent); err != nil || !absent {
				t.Fatalf("legacy absent context is not SQL NULL: %v", err)
			}
			imported := state(t)
			for _, dry := range []bool{false, true} {
				if _, err := s.ImportLegacy(operationContext(t), batch, "importer", dry); err == nil {
					t.Fatal("repeat import accepted")
				}
				if !reflect.DeepEqual(imported, state(t)) {
					t.Fatal("repeat import changed graph")
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = OpenExisting(operationContext(t), o)
			if err != nil {
				t.Fatal(err)
			}
			after, err := s.ShowIssue(operationContext(t), "beads/old-b")
			if err != nil || !reflect.DeepEqual(b, after) {
				t.Fatalf("reopen lost data: %v", err)
			}
			fresh, err := s.CreateIssue(operationContext(t), "beads/new-work", publicops.CreateRequest{Actor: "importer", Issue: &types.Issue{Title: "After import", Priority: 2, Status: types.StatusOpen, IssueType: types.TypeTask}})
			if err != nil || types.ExtractPrefix(fresh.Properties.ID) != "old-" {
				t.Fatalf("post-import ID prefix: %+v %v", fresh, err)
			}
			for _, pair := range [][2]string{{"beads/new-work", "beads/old-a"}, {"beads/old-b", "beads/new-work"}} {
				if _, err := s.AddDependency(operationContext(t), DependencyRequest{SourcePath: pair[0], TargetPath: pair[1], Actor: "importer"}); err != nil {
					t.Fatalf("post-import dependency %v: %v", pair, err)
				}
			}

		})
	}
}

func TestLegacyImportValidationRollsBack(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, backend)
			operationContext := func(t *testing.T) context.Context {
				t.Helper()
				operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
				t.Cleanup(cancel)
				return operation
			}
			s, err := OpenExisting(operationContext(t), o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			before := legacyImportState(t, operationContext(t), s)
			for _, input := range []string{
				strings.Replace(legacyImportFixture, `"type":"blocks"`, `"type":"`+strings.Repeat("x", types.MaxDependencyTypeLen+1)+`"`, 1),
				strings.Replace(legacyImportFixture, `"depends_on_id":"old-a"`, `"depends_on_id":"old-missing"`, 1),
				strings.Replace(legacyImportFixture, `"metadata":{"custom":{"flag":true}}`, `"metadata":[1]`, 1),
				strings.Replace(legacyImportFixture, `"closed_at":"2026-01-02T00:00:00Z"`, `"closed_at":"2026-01-02T00:00:00.123Z"`, 1),
				legacyImportFixture + `{"id":"old-a","title":"duplicate"}`,
				`{"id":"old-a","title":"x","ephemeral":true}`,
				`{"id":"old-a","title":"x","no_history":true}`,
				`{"id":"old-a","title":"x","lease_expires_at":"2099-01-01T00:00:00Z"}`,
				`{"id":"old-a","title":"x","heartbeat_at":"2026-01-01T00:00:00Z"}`,
				`{"id":"old-a","title":"x","lease_granted_node":"previous-node"}`,
				`{"id":"old-a","title":"x","status":"tombstone"}`,
				`{"id":"old-a","title":"a","dependencies":[{"depends_on_id":"old-b","type":"blocks"}]}
{"id":"old-b","title":"b","dependencies":[{"depends_on_id":"old-a","type":"blocks"}]}`,
				`{"id":"one-a","title":"a"}` + "\n" + `{"id":"two-b","title":"b"}`,
				`{"id":"old-a","title":"a","bonded_from":[{"proto_id":"p"}]}`,
				`{"id":"old-a","title":"a","source_formula":"formula"}`,
				`{"id":"old-a","title":"a","source_location":"steps[0]"}`,
				`{"id":"old-a","title":"a","storage_class":"versioned"}`,
				"",
				`{"_schema":"beads-jsonl/1"}`,
			} {
				if _, err := s.ImportLegacy(operationContext(t), parseLegacyFixture(t, input), "importer", false); !errors.Is(err, storage.ErrValidation) {
					t.Fatalf("wrong validation refusal for %s: %v", input, err)
				}
				if !reflect.DeepEqual(before, legacyImportState(t, operationContext(t), s)) {
					t.Fatal("invalid import changed graph")
				}
			}
		})
	}
}

// These fields appear in ordinary exports but are not exercised by the small
// producer fixture. Import itself verifies the hydrated native row and retained
// graph projection before returning. Exercise each family's rollback first,
// then apply all families together and compare current/retained reads on reopen.
func TestLegacyImportFieldFamilies(t *testing.T) {
	families := map[string]string{
		"assignment":     `"assignee":"iris","estimated_minutes":20,"external_ref":"gh-123","spec_id":"spec","source_system":"adapter"`,
		"schedule":       `"status":"deferred","started_at":"2026-01-01T00:00:00Z","defer_until":"2099-01-01T00:00:00Z","due_at":"2099-01-02T00:00:00Z"`,
		"closing":        `"status":"closed","closed_at":"2026-01-02T00:00:00Z","closed_by_session":"session","close_reason":"finished"`,
		"context":        `"pinned":true,"is_template":true`,
		"compaction":     `"compaction_level":1,"compacted_at":"2026-01-02T00:00:00Z","compacted_at_commit":"abc123","original_size":100`,
		"classification": `"mol_type":"swarm","work_type":"open_competition","wisp_type":"patrol","sender":"original"`,
		"gate":           `"await_type":"timer","await_id":"timer-1","timeout":60000000000,"waiters":["iris@example.invalid"]`,
		"event":          `"event_kind":"agent.started","actor":"actor","target":"target","payload":"{\"flag\":true}"`,
	}
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, backend)
			// Field dry-runs, apply and reopened reads must not share Init's
			// deadline or consume one another's operation budgets.
			operationContext := func(t *testing.T) context.Context {
				t.Helper()
				operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
				t.Cleanup(cancel)
				return operation
			}
			s, err := OpenExisting(operationContext(t), o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			before := legacyImportState(t, operationContext(t), s)
			for name, fields := range families {
				t.Run(name, func(t *testing.T) {
					input := `{"id":"old-a","title":"Fields","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",` + fields + `}`
					if _, err := s.ImportLegacy(operationContext(t), parseLegacyFixture(t, input), "importer", true); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, legacyImportState(t, operationContext(t), s)) {
						t.Fatal("field dry-run changed database")
					}
				})
			}
			var names []string
			for name := range families {
				names = append(names, name)
			}
			sort.Strings(names)
			var input strings.Builder
			for i, name := range names {
				fmt.Fprintf(&input, `{"id":"fields-%d","title":"%s","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",%s}`+"\n", i+1, name, families[name])
			}
			if _, err := s.ImportLegacy(operationContext(t), parseLegacyFixture(t, input.String()), "importer", false); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = OpenExisting(operationContext(t), o)
			if err != nil {
				t.Fatal(err)
			}
			for i, name := range names {
				path := fmt.Sprintf("beads/fields-%d", i+1)
				current, err := s.ShowIssue(operationContext(t), path)
				if err != nil {
					t.Fatal(err)
				}
				payload, err := json.Marshal(current.Properties)
				if err != nil {
					t.Fatal(err)
				}
				var got, want map[string]json.RawMessage
				if err := json.Unmarshal(payload, &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte("{"+families[name]+"}"), &want); err != nil {
					t.Fatal(err)
				}
				for field, value := range want {
					if string(got[field]) != string(value) {
						t.Errorf("%s field %s=%s, want %s", name, field, got[field], value)
					}
				}
				_, versions, err := s.Versions(operationContext(t), path)
				if err != nil || len(versions) != 1 {
					t.Fatalf("%s initial versions=%v err=%v", name, versions, err)
				}
				retained, err := s.ReadVersion(operationContext(t), path, versions[0].Version)
				if err != nil {
					t.Fatal(err)
				}
				initial, ok := retained.(IssueRecord)
				if !ok {
					t.Fatalf("%s retained type=%T", name, retained)
				}
				// Native row locks and content digests are deliberately excluded
				// from retained Issue history; compare only the durable payload.
				current.Properties.RowVersion, current.Properties.ContentHash = 0, ""
				if !reflect.DeepEqual(current, initial) {
					t.Fatalf("%s current/initial retained mismatch: %v", name, err)
				}
			}
		})
	}
}

// Exercise native comment normalization and persistence, not just the verifier.
func TestLegacyImportCommentFeedPersistence(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, backend)
			operationContext := func(t *testing.T) context.Context {
				t.Helper()
				operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
				t.Cleanup(cancel)
				return operation
			}
			s, err := OpenExisting(operationContext(t), o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			before := legacyImportState(t, operationContext(t), s)
			collapsed := &types.Issue{ID: "comments-a1b", Title: "Distinct source comments", Comments: []*types.Comment{
				{ID: "first", Author: "author", Text: "same", CreatedAt: stamp},
				{ID: "second", Author: "author", Text: "same", CreatedAt: stamp},
			}}
			if _, err := s.ImportLegacy(operationContext(t), legacyimport.Batch{Issues: []*types.Issue{collapsed}}, "importer", false); err == nil {
				t.Fatal("native duplicate-content collapse was silently accepted")
			}
			if !reflect.DeepEqual(before, legacyImportState(t, operationContext(t), s)) {
				t.Fatal("late comment refusal changed the database")
			}
			issue := &types.Issue{ID: "comments-a1b", Title: "Comment-heavy import"}
			for i := range 80 {
				comment := &types.Comment{Author: "author", Text: fmt.Sprintf("Comment %d", i)}
				if i%2 == 0 {
					comment.ID = fmt.Sprintf("source-%d", i)
					comment.CreatedAt = stamp.Add(time.Duration(i) * time.Second)
				}
				issue.Comments = append(issue.Comments, comment)
			}
			result, err := s.ImportLegacy(operationContext(t), legacyimport.Batch{Issues: []*types.Issue{issue}}, "importer", false)
			if err != nil || result.Comments != 80 {
				t.Fatalf("comment-heavy import: %+v %v", result, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = OpenExisting(operationContext(t), o)
			if err != nil {
				t.Fatal(err)
			}
			feed, err := s.IssueComments(operationContext(t), "beads/comments-a1b")
			if err != nil || len(feed) != 80 {
				t.Fatalf("reopened comment feed: %d %v", len(feed), err)
			}
			byText, ids := map[string]*types.Comment{}, map[string]bool{}
			for _, comment := range feed {
				if comment.ID == "" || ids[comment.ID] || comment.IssueID != issue.ID || comment.Author != "author" || comment.CreatedAt.IsZero() {
					t.Fatalf("invalid normalized comment: %+v", comment)
				}
				ids[comment.ID] = true
				byText[comment.Text] = comment
			}
			for _, original := range issue.Comments {
				stored := byText[original.Text]
				if stored == nil || (original.ID != "" && stored.ID != original.ID) || (!original.CreatedAt.IsZero() && !original.CreatedAt.Equal(stored.CreatedAt)) {
					t.Fatalf("source comment changed: %+v -> %+v", original, stored)
				}
			}
		})
	}
}

func TestLegacyImportCurrentReadLimitRollsBack(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, backend)
			// Large-write refusal and its full-state inspections each need their
			// own bound after initialization, as does the later count refusal.
			operationContext := func(t *testing.T) context.Context {
				t.Helper()
				operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
				t.Cleanup(cancel)
				return operation
			}
			s, err := OpenExisting(operationContext(t), o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			before := legacyImportState(t, operationContext(t), s)
			batch := parseLegacyFixture(t, `{"id":"old-a","title":"Large","description":"`+strings.Repeat("x", 9*1024*1024)+`"}`)
			_, err = s.ImportLegacy(operationContext(t), batch, "importer", false)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("read-budget error: %v", err)
			}
			if !reflect.DeepEqual(before, legacyImportState(t, operationContext(t), s)) {
				t.Fatal("read-budget refusal left data or prefix")
			}
			many := legacyimport.Batch{}
			for i := 0; i <= PreviewSnapshotLimit; i++ {
				many.Memories = append(many.Memories, legacyimport.Memory{Key: fmt.Sprintf("key-%d", i), Value: "value"})
			}
			if _, err := s.ImportLegacy(operationContext(t), many, "importer", false); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("count-budget error: %v", err)
			}
		})
	}
}

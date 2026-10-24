package graphstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/types"
)

// These tests use database/sql's real cleanup paths with a test-owned connector,
// not an engine or network server. The fixture is an empty controlled workspace
// with the complete persisted Type installation. It proves error propagation, not
// engine consistency, SQL query correctness, or the publication contract.
func TestExportCurrentSnapshotRollbackCleanup(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("failure=%t", failure), func(t *testing.T) {
			fault := errors.New("driver transaction rollback failed")
			faults := exportCleanupFaults{}
			if failure {
				faults.rollback = fault
			}
			s, d := exportCleanupStore(t, faults)
			acquired := false
			s.afterWrite = func(stage string) error {
				if stage == "export-comments" {
					acquired = true
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			state, err := s.ExportCurrentSnapshot(ctx)
			if !acquired {
				t.Fatalf("failure occurred before complete detached acquisition: %v", err)
			}
			if failure {
				if !errors.Is(err, fault) || !reflect.DeepEqual(state, ExportCurrentState{}) {
					t.Fatalf("rollback failure leaked state/lost error: %+v %v", state, err)
				}
			} else {
				want := ExportCurrentState{IssuePrefix: "web-app-", CommentsByIssuePath: map[string][]types.Comment{}, Snapshot: Snapshot{WriterToken: exportCleanupToken, Records: []any{}, Types: []graph.TypeDescriptor{}, OwnedLinkTypes: map[string][]string{}, OwnedLinkTypesByResource: map[string][]string{}}}
				for _, descriptor := range d.descriptors {
					parsed, parseErr := graph.ParseTypeDescriptor(descriptor.CanonicalJSON())
					if parseErr != nil {
						t.Fatal(parseErr)
					}
					want.Snapshot.Types = append(want.Snapshot.Types, parsed)
					if parsed.Describes() == graph.KindBead {
						owned, ownedErr := s.SelectedOwnedLinkTypes(parsed.ID())
						if ownedErr != nil {
							t.Fatal(ownedErr)
						}
						want.Snapshot.OwnedLinkTypes[parsed.ID()] = owned
					}
				}
				sort.Slice(want.Snapshot.Types, func(i, j int) bool { return want.Snapshot.Types[i].ID() < want.Snapshot.Types[j].ID() })
				if err != nil || len(want.Snapshot.Types) != 55 || !reflect.DeepEqual(state, want) {
					t.Fatalf("successful complete acquisition differs: got=%+v want=%+v error=%v", state, want, err)
				}
			}
			exportCleanupTransactionCount(t, s, d)
		})
	}
}

func TestExportCurrentSnapshotCommentRowsCleanup(t *testing.T) {
	rowsFault := errors.New("driver comment rows close failed")
	rollbackFault := errors.New("driver transaction rollback also failed")
	s, d := exportCleanupStore(t, exportCleanupFaults{commentClose: rowsFault, rollback: rollbackFault})
	inventoryAcquired, commentsAcquired := false, false
	s.afterWrite = func(stage string) error {
		switch stage {
		case "export-inventory":
			inventoryAcquired = true
		case "export-comments":
			commentsAcquired = true
		}
		return nil
	}
	state, err := s.ExportCurrentSnapshot(context.Background())
	if !errors.Is(err, rowsFault) || !errors.Is(err, rollbackFault) || !reflect.DeepEqual(state, ExportCurrentState{}) {
		t.Fatalf("actual rows/rollback cleanup failure leaked state/lost chain: %+v %v", state, err)
	}
	if !inventoryAcquired || commentsAcquired || d.commentCloses.Load() != 1 {
		t.Fatalf("did not exercise comment EOF cleanup: inventory=%t comments=%t closes=%d", inventoryAcquired, commentsAcquired, d.commentCloses.Load())
	}
	exportCleanupTransactionCount(t, s, d)
}

func TestExportCurrentSnapshotBadConnectionCleanup(t *testing.T) {
	fault := errors.New("rollback lost its driver connection")
	s, d := exportCleanupStore(t, exportCleanupFaults{rollback: errors.Join(fault, driver.ErrBadConn)})
	state, err := s.ExportCurrentSnapshot(context.Background())
	// An ErrBadConn transaction result automatically closes the logical Conn.
	// withTx's subsequent checked Conn.Close therefore returns ErrConnDone.
	// This tests logical connection cleanup separately from driver.Conn.Close.
	if !errors.Is(err, fault) || !errors.Is(err, driver.ErrBadConn) || !errors.Is(err, sql.ErrConnDone) || !reflect.DeepEqual(state, ExportCurrentState{}) {
		t.Fatalf("bad connection cleanup lost chain/exposed state: %+v %v", state, err)
	}
	if d.connectionCloses.Load() != 1 {
		t.Fatalf("bad driver connection was not discarded: %d", d.connectionCloses.Load())
	}
	exportCleanupTransactionCount(t, s, d)
}

func TestExportCurrentSnapshotPhysicalCloseBoundary(t *testing.T) {
	fault := errors.New("physical driver connection close failed")
	s, d := exportCleanupStore(t, exportCleanupFaults{connectionClose: fault})
	state, err := s.ExportCurrentSnapshot(context.Background())
	// Logical Conn.Close returns a healthy connection to the pool. It does not
	// call driver.Conn.Close; the reader still leaves Store.Close to its caller.
	if err != nil || state.CommentsByIssuePath == nil || d.connectionCloses.Load() != 0 {
		t.Fatalf("logical connection return confused with physical close: %+v %v closes=%d", state, err, d.connectionCloses.Load())
	}
	exportCleanupTransactionCount(t, s, d)
	if err := s.Close(); !errors.Is(err, fault) {
		t.Fatalf("caller-owned Store.Close lost physical cleanup failure: %v", err)
	}
	if err := s.Close(); !errors.Is(err, fault) || d.connectionCloses.Load() != 1 {
		t.Fatalf("repeated cleanup lost failure or closed twice: %v closes=%d", err, d.connectionCloses.Load())
	}
}

func TestExportCommentsScanAndCloseCleanup(t *testing.T) {
	rowsFault := errors.New("driver rows close failed after scan error")
	rollbackFault := errors.New("driver rollback failed after scan error")
	s, d := exportCleanupStore(t, exportCleanupFaults{
		commentClose: rowsFault, rollback: rollbackFault,
		// A string cannot be scanned into the feed's time.Time destination.
		commentValues: [][]driver.Value{{"comment-a", "web-app-a1b", "author", "original", "invalid driver timestamp"}},
	})
	var leaked bool
	err := s.withTx(context.Background(), false, func(tx *sql.Tx) error {
		comments, err := exportCommentsInTx(context.Background(), tx, map[string]string{"web-app-a1b": "beads/work"}, 1)
		leaked = comments != nil
		return err
	})
	if err == nil || leaked || !strings.Contains(err.Error(), "Scan error") || !errors.Is(err, rowsFault) || !errors.Is(err, rollbackFault) || d.commentCloses.Load() != 1 {
		t.Fatalf("scan/rows-close/rollback error chain: leaked=%t closes=%d err=%v", leaked, d.commentCloses.Load(), err)
	}
	exportCleanupTransactionCount(t, s, d)
}

// Direct feeds exercise nonempty results without fabricating the native Issue
// hydration queried by the full reader. Real mixed-store acquisition remains an
// engine gate; database/sql still owns every rows/transaction cleanup here.
func TestExportCommentsFeedCleanup(t *testing.T) {
	stamp := time.Date(2026, time.October, 10, 12, 30, 45, 123000000, time.UTC)
	first := []driver.Value{"comment-a", "web-app-a1b", "author", "original", stamp}
	second := []driver.Value{"comment-b", "web-app-a1b", "another author", "next", stamp.Add(time.Second)}
	bad := func(field int, value string) []driver.Value {
		row := append([]driver.Value(nil), second...)
		row[field] = value
		return row
	}
	for _, tc := range []struct {
		name       string
		values     [][]driver.Value
		count      uint64
		message    string
		closeFault bool
	}{
		{name: "valid", values: [][]driver.Value{first, second}, count: 2},
		{name: "invalid-id-utf8", values: [][]driver.Value{first, bad(0, "comment-\xff")}, count: 2, message: "not UTF-8", closeFault: true},
		{name: "invalid-owner-utf8", values: [][]driver.Value{first, bad(1, "web-app-\xff")}, count: 2, message: "not UTF-8", closeFault: true},
		{name: "invalid-author-utf8", values: [][]driver.Value{first, bad(2, "author-\xff")}, count: 2, message: "not UTF-8", closeFault: true},
		{name: "invalid-text-utf8", values: [][]driver.Value{first, bad(3, "orig\xffinal")}, count: 2, message: "not UTF-8", closeFault: true},
		{name: "unknown-owner", values: [][]driver.Value{first, bad(1, "web-app-other")}, count: 2, message: "owner missing", closeFault: true},
		{name: "count-overflow", values: [][]driver.Value{first, second}, count: 1, message: "differs from its bounded inventory", closeFault: true},
		// A Close error at EOF takes precedence over incomplete-feed validation.
		// Keep Close healthy here to exercise that later refusal itself.
		{name: "incomplete-feed", values: [][]driver.Value{first}, count: 2, message: "incomplete comment feed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rowsFault := errors.New("direct feed rows close failed")
			rollbackFault := errors.New("direct feed rollback failed")
			faults := exportCleanupFaults{commentValues: tc.values}
			if tc.message != "" {
				faults.rollback = rollbackFault
			}
			if tc.closeFault {
				faults.commentClose = rowsFault
			}
			s, d := exportCleanupStore(t, faults)
			var feed map[string][]types.Comment
			err := s.withTx(context.Background(), false, func(tx *sql.Tx) error {
				var err error
				feed, err = exportCommentsInTx(context.Background(), tx, map[string]string{
					"web-app-a1b": "beads/work", "web-app-empty": "beads/empty",
				}, tc.count)
				return err
			})
			if tc.message == "" {
				want := map[string][]types.Comment{
					"beads/work": {
						{ID: "comment-a", IssueID: "web-app-a1b", Author: "author", Text: "original", CreatedAt: stamp},
						{ID: "comment-b", IssueID: "web-app-a1b", Author: "another author", Text: "next", CreatedAt: stamp.Add(time.Second)},
					},
					"beads/empty": {},
				}
				if err != nil || !reflect.DeepEqual(feed, want) {
					t.Fatalf("valid comment feed changed fields/ownership/order: %+v err=%v", feed, err)
				}
			} else if feed != nil || !errors.Is(err, ErrInvalidStore) || !errors.Is(err, rollbackFault) || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("failed feed leaked partial map/lost refusal/rollback: %+v err=%v", feed, err)
			}
			if tc.closeFault && !errors.Is(err, rowsFault) {
				t.Fatalf("lost explicit rows-close failure: %v", err)
			}
			if d.commentCloses.Load() != 1 {
				t.Fatalf("direct feed physically closed %d times", d.commentCloses.Load())
			}
			exportCleanupTransactionCount(t, s, d)
		})
	}
}

func TestExportCommentsNextCleanup(t *testing.T) {
	for _, failClose := range []bool{false, true} {
		t.Run(fmt.Sprintf("close-failure=%t", failClose), func(t *testing.T) {
			nextFault := errors.New("driver comment Next failed after partial feed")
			rowsFault := errors.New("driver comment Close also failed")
			rollbackFault := errors.New("driver rollback failed after Next error")
			faults := exportCleanupFaults{
				commentNext: nextFault, rollback: rollbackFault,
				commentValues: [][]driver.Value{{"comment-a", "web-app-a1b", "author", "original", time.Date(2026, time.October, 10, 12, 30, 45, 0, time.UTC)}},
			}
			if failClose {
				faults.commentClose = rowsFault
			}
			s, d := exportCleanupStore(t, faults)
			var feed map[string][]types.Comment
			err := s.withTx(context.Background(), false, func(tx *sql.Tx) error {
				var err error
				feed, err = exportCommentsInTx(context.Background(), tx, map[string]string{"web-app-a1b": "beads/work"}, 2)
				return err
			})
			if feed != nil || !errors.Is(err, nextFault) || !errors.Is(err, rollbackFault) || d.commentCloses.Load() != 1 {
				t.Fatalf("Next failure leaked partial map/lost cleanup chain: %+v closes=%d err=%v", feed, d.commentCloses.Load(), err)
			}
			// database/sql auto-closes on non-EOF Next errors. Rows.Err keeps the
			// Next cause, while the later explicit Rows.Close is idempotent. A
			// simultaneous driver Close fault is suppressed by that API boundary;
			// do not claim the reader can propagate a cause SQL never exposes.
			if errors.Is(err, rowsFault) {
				t.Fatalf("unexpected auto-close error propagation alongside Next: %v", err)
			}
			exportCleanupTransactionCount(t, s, d)
		})
	}
}

func exportCleanupTransactionCount(t *testing.T, s *Store, d *exportCleanupDriver) {
	t.Helper()
	if d.begins.Load() != 1 || d.rollbacks.Load() != 1 || d.commits.Load() != 0 || s.db.Stats().InUse != 0 {
		t.Fatalf("cleanup was not one rollback-only transaction: begin=%d rollback=%d commit=%d in-use=%d", d.begins.Load(), d.rollbacks.Load(), d.commits.Load(), s.db.Stats().InUse)
	}
}

const exportCleanupToken = "0123456789abcdef0123456789abcdef"

type exportCleanupFaults struct {
	rollback, commentClose, commentNext, connectionClose error
	commentValues                                        [][]driver.Value
}

type exportCleanupDriver struct {
	faults                                                      exportCleanupFaults
	options                                                     Options
	descriptors                                                 map[string]graph.TypeDescriptor
	begins, rollbacks, commits, commentCloses, connectionCloses atomic.Int64
	foreignKeyReads, foreignKeyRestores                         atomic.Int64
}

func exportCleanupStore(t *testing.T, faults exportCleanupFaults) (*Store, *exportCleanupDriver) {
	t.Helper()
	d := &exportCleanupDriver{
		faults: faults,
		options: Options{Binding: Binding{
			WorkspaceID: "/disposable/export-cleanup-fixture", ScopeURL: "https://example.test/cleanup/",
			AuthorityID: exportCleanupToken, SchemaVersion: SchemaVersion,
		}},
		descriptors: make(map[string]graph.TypeDescriptor),
	}
	for _, definition := range previewTypeDefinitions() {
		descriptor, err := definition.build(d.options.Binding.ScopeURL)
		if err != nil {
			t.Fatal(err)
		}
		d.descriptors[definition.name] = descriptor
	}
	db := sql.OpenDB(d)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, options: d.options, cleanup: db.Close}
	t.Cleanup(func() {
		if err := s.Close(); err != nil && !errors.Is(err, faults.connectionClose) {
			t.Error(err)
		}
	})
	return s, d
}

func (d *exportCleanupDriver) Driver() driver.Driver { return d }
func (d *exportCleanupDriver) Open(string) (driver.Conn, error) {
	return d.Connect(context.Background())
}
func (d *exportCleanupDriver) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &exportCleanupConn{d}, nil
}

type exportCleanupConn struct{ owner *exportCleanupDriver }

func (c *exportCleanupConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("cleanup fixture does not prepare statements")
}
func (c *exportCleanupConn) Close() error {
	c.owner.connectionCloses.Add(1)
	return c.owner.faults.connectionClose
}
func (c *exportCleanupConn) Begin() (driver.Tx, error) {
	c.owner.begins.Add(1)
	return &exportCleanupTx{c.owner}, nil
}
func (c *exportCleanupConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	switch strings.Join(strings.Fields(strings.ToLower(query)), " ") {
	case "set @@dolt_transaction_commit = 0", "set @@dolt_force_transaction_commit = 0", "set @@dolt_allow_commit_conflicts = 0", "set @@time_zone = '+00:00'":
		return driver.RowsAffected(0), nil
	case "set @@foreign_key_checks = 0":
		return driver.RowsAffected(0), nil
	case "set @@foreign_key_checks = 1":
		c.owner.foreignKeyRestores.Add(1)
		return driver.RowsAffected(0), nil
	default:
		return nil, fmt.Errorf("cleanup fixture refused non-session SQL: %s", query)
	}
}

func (c *exportCleanupConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := strings.Join(strings.Fields(strings.ToLower(query)), " ")
	d := c.owner
	row := func(columns []string, values ...driver.Value) driver.Rows {
		return &exportCleanupRows{columns: columns, values: [][]driver.Value{values}}
	}
	switch {
	case q == "select @@foreign_key_checks":
		d.foreignKeyReads.Add(1)
		return row([]string{"foreign_key_checks"}, int64(1)), nil
	case q == "select active_branch()":
		return row([]string{"branch"}, "main"), nil
	case q == "select count(*) from graph_preview_types":
		return row([]string{"count"}, int64(len(d.descriptors))), nil
	case q == "select name,case when octet_length(descriptor)<=? then descriptor else null end,fingerprint from graph_preview_types":
		if len(args) != 1 || args[0].Value != int64(len(d.options.Binding.ScopeURL)*4+8192) {
			return nil, fmt.Errorf("cleanup bounded descriptor arguments: %v", args)
		}
		rows := &exportCleanupRows{columns: []string{"name", "descriptor", "fingerprint"}}
		for name, descriptor := range d.descriptors {
			var raw driver.Value
			if int64(len(descriptor.CanonicalJSON())) <= args[0].Value.(int64) {
				raw = descriptor.CanonicalJSON()
			}
			rows.values = append(rows.values, []driver.Value{name, raw, descriptor.Fingerprint()})
		}
		return rows, nil
	case strings.HasPrefix(q, "select descriptor, fingerprint from graph_preview_types where name=?"):
		if len(args) != 2 {
			return nil, fmt.Errorf("cleanup descriptor lookup arguments: %v", args)
		}
		descriptor, ok := d.descriptors[fmt.Sprint(args[0].Value)]
		if !ok || args[1].Value != int64(len(descriptor.CanonicalJSON())) {
			return nil, errors.New("cleanup descriptor lookup did not match persisted Type")
		}
		return row([]string{"descriptor", "fingerprint"}, descriptor.CanonicalJSON(), descriptor.Fingerprint()), nil
	case strings.HasPrefix(q, "select workspace, scope_url, authority_id, schema_version, writer_token from graph_preview_scope"):
		b := d.options.Binding
		return row([]string{"workspace", "scope", "authority", "schema", "token"}, b.WorkspaceID, b.ScopeURL, b.AuthorityID, int64(b.SchemaVersion), exportCleanupToken), nil
	case q == "select writer_token from graph_preview_scope where singleton=1":
		return row([]string{"token"}, exportCleanupToken), nil
	case q == "select value from config where `key`='issue_prefix'":
		if len(args) != 0 {
			return nil, errors.New("literal prefix lookup had arguments")
		}
		return row([]string{"value"}, "web-app-"), nil
	case q == "select value from config where `key` = ?":
		if len(args) != 1 {
			return nil, fmt.Errorf("cleanup config lookup arguments: %v", args)
		}
		switch args[0].Value {
		case "issue_prefix":
			return row([]string{"value"}, "web-app-"), nil
		case "types.custom":
			return &exportCleanupRows{columns: []string{"value"}}, nil
		default:
			return nil, fmt.Errorf("cleanup fixture unknown config key: %v", args[0].Value)
		}
	case q == "select name from custom_types order by name":
		return &exportCleanupRows{columns: []string{"name"}}, nil
	case strings.HasPrefix(q, "select id,issue_id,author,text,created_at from comments order by"):
		return &exportCleanupRows{columns: []string{"id", "issue_id", "author", "text", "created_at"}, values: d.faults.commentValues, closeErr: d.faults.commentClose, nextErr: d.faults.commentNext, closes: &d.commentCloses}, nil
	case strings.HasSuffix(q, "limit 0"):
		return &exportCleanupRows{columns: []string{"schema-guard"}}, nil
	case strings.HasPrefix(q, "select path,backing,resource_kind from graph_preview_catalog"):
		return &exportCleanupRows{columns: []string{"path", "backing", "kind"}}, nil
	case strings.HasPrefix(q, "select path from graph_preview_catalog"):
		return &exportCleanupRows{columns: []string{"path"}}, nil
	case strings.HasPrefix(q, "select 1 from graph_preview_catalog"):
		return &exportCleanupRows{columns: []string{"invalid"}}, nil
	case strings.HasPrefix(q, "select count(*)"), strings.HasPrefix(q, "select exists("), strings.HasPrefix(q, "select cast(least("):
		// Empty current tables: no allocations, relations, comments, alternative
		// planes or mapping failures. Accounting values are irrelevant to this
		// cleanup test; real engine tests own acquisition-bound correctness.
		return row([]string{"count"}, int64(0)), nil
	default:
		return nil, fmt.Errorf("unsupported cleanup fixture query: %s", query)
	}
}

type exportCleanupTx struct{ owner *exportCleanupDriver }

func (tx *exportCleanupTx) Commit() error {
	tx.owner.commits.Add(1)
	return errors.New("cleanup fixture forbids committing an export")
}
func (tx *exportCleanupTx) Rollback() error {
	tx.owner.rollbacks.Add(1)
	return tx.owner.faults.rollback
}

type exportCleanupRows struct {
	columns  []string
	values   [][]driver.Value
	position int
	closeErr error
	nextErr  error
	closes   *atomic.Int64
}

func (r *exportCleanupRows) Columns() []string { return r.columns }
func (r *exportCleanupRows) Close() error {
	if r.closes != nil {
		r.closes.Add(1)
	}
	return r.closeErr
}
func (r *exportCleanupRows) Next(dest []driver.Value) error {
	if r.position == len(r.values) {
		if r.nextErr != nil {
			return r.nextErr
		}
		return io.EOF
	}
	copy(dest, r.values[r.position])
	r.position++
	return nil
}

var _ driver.Connector = (*exportCleanupDriver)(nil)
var _ driver.QueryerContext = (*exportCleanupConn)(nil)
var _ driver.ExecerContext = (*exportCleanupConn)(nil)

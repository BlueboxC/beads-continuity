//go:build cgo

package embeddeddolt_test

import (
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
	"testing"
)

// A conflict-free companion table must reach the merge commit, not just WORKING.
func TestEmbeddedConflictMergeStagesCompanionTables(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		name := "clean"
		if dirty {
			name = "unrelated-dirty-table"
		}
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t, "companions")
			ctx := t.Context()
			conn := openSettleConn(t, ctx, te)
			exec := func(q string) {
				t.Helper()
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			exec("CREATE TABLE private_notes (id INT PRIMARY KEY, value INT)")
			exec("INSERT INTO private_notes VALUES (1,0)")
			exec("INSERT INTO issues (id,title,description,design,acceptance_criteria,notes,status,priority,issue_type,updated_at) VALUES ('shared','shared','','','','','open',2,'task','2020-01-01')")
			exec("CALL DOLT_COMMIT('-Am','base')")
			exec("CALL DOLT_BRANCH('peer')")
			exec("INSERT INTO comments (id,issue_id,author,text,created_at) VALUES ('comment-a','shared','A','from A',NOW())")
			exec("INSERT INTO labels (issue_id,label) VALUES ('shared','from-a')")
			exec("UPDATE issues SET status='in_progress',updated_at='2030-01-01' WHERE id='shared'")
			exec("CALL DOLT_COMMIT('-Am','A')")
			exec("CALL DOLT_CHECKOUT('peer')")
			exec("INSERT INTO comments (id,issue_id,author,text,created_at) VALUES ('comment-b','shared','B','from B',NOW())")
			exec("INSERT INTO labels (issue_id,label) VALUES ('shared','from-b')")
			exec("UPDATE issues SET title='renamed',updated_at='2031-01-01' WHERE id='shared'")
			exec("CALL DOLT_COMMIT('-Am','B')")
			exec("CALL DOLT_CHECKOUT('main')")
			if dirty {
				exec("UPDATE private_notes SET value=99 WHERE id=1")
			}
			if err := versioncontrolops.MergeAndSettle(ctx, conn, "peer"); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"comments", "labels"} {
				for _, rev := range []string{"HEAD", "STAGED", "WORKING"} {
					var count int
					if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" AS OF '"+rev+"'").Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count != 2 {
						t.Errorf("%s %s has %d rows, want both replicas' rows", table, rev, count)
					}
				}
			}
			if dirty {
				for rev, want := range map[string]int{"HEAD": 0, "STAGED": 0, "WORKING": 99} {
					var got int
					if err := conn.QueryRowContext(ctx, "SELECT value FROM private_notes AS OF '"+rev+"' WHERE id=1").Scan(&got); err != nil {
						t.Fatal(err)
					}
					if got != want {
						t.Errorf("unrelated notes at %s = %d, want %d", rev, got, want)
					}
				}
			}
		})
	}
}

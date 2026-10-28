//go:build cgo

package embeddeddolt_test

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestReportSQL uses only a fresh test-owned database per reported SQL case.
func TestReportSQL(t *testing.T) {
	cases := []struct {
		name  string
		setup []string
		query string
		want  [][]string
	}{
		{"12218", []string{"CREATE TABLE v (b VARCHAR(4))", "INSERT IGNORE INTO v VALUES ('éééééé')", "INSERT IGNORE INTO v VALUES (CAST('2020-01-01' AS DATETIME))", "INSERT IGNORE INTO v VALUES (123456)", "INSERT IGNORE INTO v VALUES (X'616263646566')"}, "SELECT b FROM v ORDER BY b", [][]string{{"1234"}, {"2020"}, {"abcd"}, {"éééé"}}},
		{"12219", []string{"CREATE TABLE r6 (a INT, b INT, c INT, UNIQUE(a), UNIQUE(b))", "INSERT INTO r6 VALUES (1,1,1)", "REPLACE INTO r6 VALUES (2,1,1)"}, "SELECT * FROM r6", [][]string{{"2", "1", "1"}}},
		{"12220", []string{"CREATE TABLE ig4 (a INT PRIMARY KEY, n INT NOT NULL, m INT, g INT AS (n+1) STORED)", "INSERT IGNORE INTO ig4(a,m) VALUES (1,1)"}, "SELECT * FROM ig4", [][]string{{"1", "0", "1", "1"}}},
		{"12221", []string{"CREATE TABLE b (x BIT(8))", "INSERT INTO b SELECT 1E0 UNION ALL SELECT NULL"}, "SELECT HEX(x) FROM b ORDER BY x", [][]string{{"<nil>"}, {"1"}}},
		{"12222", []string{"CREATE TABLE z (d DATETIME, dd DATE)", "INSERT IGNORE INTO z VALUES ('0000-00-00 00:00:00', '0000-00-00')"}, "SELECT d+0, dd+0, CAST(d AS SIGNED), CAST(d AS DECIMAL(20,0)) FROM z", [][]string{{"0", "0", "0", "0"}}},
		{"12223", []string{"CREATE TABLE y (y YEAR, e ENUM('x','y'), s SET('x','y'))", "INSERT INTO y(y) VALUES (TRUE)", "INSERT INTO y(e) VALUES (TRUE)", "INSERT INTO y(s) VALUES (TRUE)", "INSERT INTO y(y) VALUES (FALSE)", "INSERT INTO y(s) VALUES (FALSE)"}, "SELECT COUNT(*) FROM y", [][]string{{"5"}}},
		{"12218update", []string{"CREATE TABLE v (b VARCHAR(4))", "INSERT INTO v VALUES ('old'),('data')", "UPDATE IGNORE v SET b=CAST('2020-01-01' AS DATETIME)"}, "SELECT b FROM v", [][]string{{"2020"}, {"2020"}}},
		{"12219indexes", []string{"CREATE TABLE t (a INT, b INT, c INT, UNIQUE(a), UNIQUE(b))", "INSERT INTO t VALUES (1,1,1)", "INSERT IGNORE INTO t VALUES (2,1,2),(2,2,2)", "REPLACE INTO t VALUES (3,2,4)", "REPLACE INTO t VALUES (3,2,4)"}, "SELECT * FROM t FORCE INDEX(a) WHERE a IN (1,2,3) ORDER BY a", [][]string{{"1", "1", "1"}, {"3", "2", "4"}}},
		{"12220functional", []string{"CREATE TABLE ig2 (a INT PRIMARY KEY, n INT NOT NULL, m INT)", "CREATE INDEX fx ON ig2 ((n+1))", "INSERT IGNORE INTO ig2(a,m) VALUES (1,1)"}, "SELECT a,n,m FROM ig2 WHERE n+1=1", [][]string{{"1", "0", "1"}}},
		{"12221types", []string{"CREATE TABLE u1 AS SELECT 1E0 AS x UNION ALL SELECT NULL", "CREATE TABLE u2 AS SELECT CAST('2020-01-01' AS DATETIME) AS x UNION ALL SELECT NULL"}, "SELECT column_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name IN ('u1','u2') ORDER BY table_name", [][]string{{"double"}, {"datetime"}}},
		{"12221names", []string{"CREATE TABLE u1 AS SELECT NULL AS authored UNION ALL SELECT 1E0", "CREATE TABLE u2 AS SELECT NULL AS authored UNION ALL SELECT CAST('2020-01-01' AS DATETIME)", "ALTER TABLE u1 ADD INDEX authored_idx(authored)", "ALTER TABLE u2 ADD INDEX authored_idx(authored)"}, "SELECT column_name,column_type FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name IN ('u1','u2') ORDER BY table_name", [][]string{{"authored", "double"}, {"authored", "datetime"}}},
		{"12222ordinary", []string{"CREATE TABLE z (d DATETIME, dd DATE)", "INSERT INTO z VALUES ('2020-01-02 03:04:05','2020-01-02')"}, "SELECT d+0,dd+0,CAST(d AS SIGNED),CAST(d AS DECIMAL(20,0)) FROM z", [][]string{{"20200102030405", "20200102", "20200102030405", "20200102030405"}}},
		{"12223values", []string{"CREATE TABLE y (y YEAR, e ENUM('x','y'), s SET('x','y'))", "INSERT INTO y VALUES (TRUE,TRUE,TRUE),(FALSE,TRUE,FALSE)"}, "SELECT y,e,s FROM y ORDER BY y", [][]string{{"0", "x", ""}, {"2001", "x", "x"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
					defer cancel()
					db, closeDB, err := embeddeddolt.OpenSQL(ctx, t.TempDir(), "", "")
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := closeDB(); err != nil {
							t.Error(err)
						}
					}()
					conn, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					for _, q := range append([]string{"CREATE DATABASE report_test", "USE report_test"}, tc.setup...) {
						if prepared && (strings.HasPrefix(q, "INSERT") || strings.HasPrefix(q, "UPDATE") || strings.HasPrefix(q, "REPLACE")) {
							stmt, e := conn.PrepareContext(ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							_, err = stmt.ExecContext(ctx)
							_ = stmt.Close()
						} else {
							_, err = conn.ExecContext(ctx, q)
						}
						if err != nil {
							t.Fatalf("%s: %v", q, err)
						}
					}
					query := tc.query
					if prepared {
						stmt, e := conn.PrepareContext(ctx, query)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						rows, err := stmt.QueryContext(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						assertReportRows(t, rows, tc.want)
						return
					}
					rows, err := conn.QueryContext(ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					assertReportRows(t, rows, tc.want)
				})
			}
		})
	}
}

func assertReportRows(t *testing.T, rows *sql.Rows, want [][]string) {
	t.Helper()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var got [][]string
	for rows.Next() {
		v := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range v {
			ptr[i] = &v[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			t.Fatal(err)
		}
		s := make([]string, len(v))
		for i, x := range v {
			if b, ok := x.([]byte); ok {
				s[i] = string(b)
			} else {
				s[i] = fmt.Sprint(x)
			}
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

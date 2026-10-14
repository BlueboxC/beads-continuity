package uow

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

// This records driver dispatch, so a refused statement cannot have reached Dolt.
func TestReadOnlyConnectorRejectsWritesBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	base := &readOnlyRecordingConn{}
	db := sql.OpenDB(readOnlyConnector{Connector: readOnlyRecordingConnector{base}})
	defer db.Close()
	for _, query := range []string{
		"UPDATE issues SET title='changed'", "CREATE TABLE forbidden (id INT)", "CALL DOLT_ADD('.')",
		"SELECT 1; DELETE FROM issues", "SELECT 1 INTO OUTFILE '/tmp/forbidden'",
		"SELECT 1 UNION SELECT 2 INTO OUTFILE '/tmp/forbidden'", "COMMIT", "START TRANSACTION", "SET @x=1", "USE other",
	} {
		before := base.calls
		if _, err := db.ExecContext(ctx, query); err == nil {
			t.Errorf("Exec accepted %s", query)
		}
		if rows, err := db.QueryContext(ctx, query); err == nil {
			rows.Close()
			t.Errorf("Query accepted %s", query)
		}
		if stmt, err := db.PrepareContext(ctx, query); err == nil {
			stmt.Close()
			t.Errorf("Prepare accepted %s", query)
		}
		if base.calls != before {
			t.Fatalf("write dispatched: %s", query)
		}
	}
	if _, err := db.BeginTx(ctx, nil); err == nil {
		t.Fatal("writable transaction accepted")
	}
	for _, query := range []string{"START TRANSACTION READ ONLY;", "ROLLBACK;"} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT ?", 42).Scan(&n); err != nil || n != 42 {
		t.Fatalf("bound read: %d %v", n, err)
	}
	stmt, err := db.PrepareContext(ctx, "SELECT ?")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if err := stmt.QueryRowContext(ctx, 42).Scan(&n); err != nil || n != 42 {
		t.Fatalf("prepared read: %d %v", n, err)
	}
	base.invalid = true
	if db.Stats().OpenConnections == 0 {
		t.Fatal("fixture has no pooled connection")
	}
	if _, err := db.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if db.Stats().OpenConnections != 0 {
		t.Fatal("wrapper hid invalid connection from database/sql")
	}
}

type readOnlyRecordingConnector struct{ conn *readOnlyRecordingConn }

func (c readOnlyRecordingConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c readOnlyRecordingConnector) Driver() driver.Driver                        { return nil }

type readOnlyRecordingConn struct {
	calls   int
	invalid bool
}

func (c *readOnlyRecordingConn) Prepare(string) (driver.Stmt, error) {
	c.calls++
	return readOnlyRecordingStmt{}, nil
}
func (c *readOnlyRecordingConn) Close() error { return nil }
func (c *readOnlyRecordingConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected Begin")
}
func (c *readOnlyRecordingConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.calls++
	return driver.RowsAffected(0), nil
}
func (c *readOnlyRecordingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.calls++
	return &readOnlyRecordingRows{}, nil
}
func (c *readOnlyRecordingConn) IsValid() bool { return !c.invalid }

type readOnlyRecordingStmt struct{}

func (readOnlyRecordingStmt) Close() error  { return nil }
func (readOnlyRecordingStmt) NumInput() int { return 1 }
func (readOnlyRecordingStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("unexpected prepared Exec")
}
func (readOnlyRecordingStmt) Query([]driver.Value) (driver.Rows, error) {
	return &readOnlyRecordingRows{}, nil
}

type readOnlyRecordingRows struct{ done bool }

func (*readOnlyRecordingRows) Columns() []string { return []string{"value"} }
func (*readOnlyRecordingRows) Close() error      { return nil }
func (r *readOnlyRecordingRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	values[0] = int64(42)
	return nil
}

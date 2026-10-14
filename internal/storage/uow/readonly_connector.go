package uow

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"

	"github.com/steveyegge/beads/internal/storage/sqlclass"
)

// Dolt's READ ONLY transaction does not reject every DDL or procedure. Guard
// each driver entry point as well, including database/sql's prepared fallback.
type readOnlyConnector struct{ driver.Connector }

func (c readOnlyConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &readOnlyConn{Conn: conn}, nil
}

type readOnlyConn struct{ driver.Conn }

func checkReadOnlySQL(query string) error {
	control := strings.ToUpper(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";")))
	if control == "START TRANSACTION READ ONLY" || control == "ROLLBACK" {
		return nil
	}
	if sqlclass.Classify(query) != sqlclass.Read {
		return errors.New("strict readonly connection refuses SQL with possible write effects")
	}
	return nil
}

func (c *readOnlyConn) Prepare(query string) (driver.Stmt, error) {
	if err := checkReadOnlySQL(query); err != nil {
		return nil, err
	}
	return c.Conn.Prepare(query)
}

func (c *readOnlyConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := checkReadOnlySQL(query); err != nil {
		return nil, err
	}
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return conn.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}

func (c *readOnlyConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := checkReadOnlySQL(query); err != nil {
		return nil, err
	}
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *readOnlyConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := checkReadOnlySQL(query); err != nil {
		return nil, err
	}
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

func (c *readOnlyConn) Begin() (driver.Tx, error) {
	return nil, errors.New("strict readonly connection requires an explicit READ ONLY transaction")
}

func (c *readOnlyConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if !opts.ReadOnly {
		return c.Begin()
	}
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		return conn.BeginTx(ctx, opts)
	}
	return c.Begin()
}

func (c *readOnlyConn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	return nil
}

func (c *readOnlyConn) ResetSession(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.SessionResetter); ok {
		return conn.ResetSession(ctx)
	}
	return nil
}

func (c *readOnlyConn) IsValid() bool {
	if conn, ok := c.Conn.(driver.Validator); ok {
		return conn.IsValid()
	}
	return true
}

func (c *readOnlyConn) CheckNamedValue(value *driver.NamedValue) error {
	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

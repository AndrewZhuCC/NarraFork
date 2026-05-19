package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

const (
	postgresRebindDriverName       = "narrafork-pgx"
	postgresSearchPathDSNSeparator = "\x00narrafork_search_path="
)

func init() {
	sql.Register(postgresRebindDriverName, postgresRebindDriver{inner: stdlib.GetDefaultDriver()})
}

func postgresDataSourceName(dsn string, schema string) string {
	if strings.TrimSpace(schema) == "" {
		return dsn
	}
	return dsn + postgresSearchPathDSNSeparator + postgresSearchPathIdentifier(schema)
}

func splitPostgresDataSourceName(name string) (string, string) {
	idx := strings.LastIndex(name, postgresSearchPathDSNSeparator)
	if idx < 0 {
		return name, ""
	}
	return name[:idx], name[idx+len(postgresSearchPathDSNSeparator):]
}

type postgresRebindDriver struct {
	inner driver.Driver
}

func (d postgresRebindDriver) Open(name string) (driver.Conn, error) {
	dsn, searchPath := splitPostgresDataSourceName(name)
	conn, err := d.inner.Open(dsn)
	if err != nil {
		return nil, err
	}
	wrapped := postgresRebindConn{Conn: conn, searchPath: searchPath}
	if err := wrapped.applySearchPath(context.Background()); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return wrapped, nil
}

type postgresRebindConn struct {
	driver.Conn
	searchPath string
}

func (c postgresRebindConn) Prepare(query string) (driver.Stmt, error) {
	return c.Conn.Prepare(rebindPostgres(query))
}

func (c postgresRebindConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if conn, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return conn.PrepareContext(ctx, rebindPostgres(query))
	}
	return c.Prepare(query)
}

func (c postgresRebindConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, rebindPostgres(query), args)
	}
	return nil, driver.ErrSkip
}

func (c postgresRebindConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, rebindPostgres(query), args)
	}
	return nil, driver.ErrSkip
}

func (c postgresRebindConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		return conn.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c postgresRebindConn) Ping(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.Pinger); ok {
		return conn.Ping(ctx)
	}
	return nil
}

func (c postgresRebindConn) CheckNamedValue(value *driver.NamedValue) error {
	if conn, ok := c.Conn.(driver.NamedValueChecker); ok {
		return conn.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (c postgresRebindConn) ResetSession(ctx context.Context) error {
	if conn, ok := c.Conn.(driver.SessionResetter); ok {
		if err := conn.ResetSession(ctx); err != nil {
			return err
		}
	}
	return c.applySearchPath(ctx)
}

func (c postgresRebindConn) applySearchPath(ctx context.Context) error {
	if strings.TrimSpace(c.searchPath) == "" {
		return nil
	}
	stmt := "SET search_path TO " + c.searchPath
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		_, err := conn.ExecContext(ctx, stmt, nil)
		return err
	}
	prepared, err := c.Conn.Prepare(stmt)
	if err != nil {
		return err
	}
	defer prepared.Close()
	_, err = prepared.Exec(nil)
	return err
}

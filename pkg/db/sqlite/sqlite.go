package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

// Executor runs queries. Both *sql.DB and *sql.Tx satisfy it, so repository
// methods work the same inside and outside a transaction.
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Client is a handle to a SQLite database file.
type Client struct {
	*sql.DB
}

// NewClient opens the SQLite database at path, creating the file if needed.
// Every connection uses WAL journaling, a 5s busy timeout and foreign keys;
// transactions take the write lock up front (BEGIN IMMEDIATE) so concurrent
// writers wait instead of failing with SQLITE_BUSY mid-transaction.
func NewClient(ctx context.Context, path string) (*Client, error) {
	if path == "" {
		panic("sqlite: path must not be empty")
	}

	params := url.Values{}
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "foreign_keys(ON)")
	params.Set("_txlock", "immediate")

	db, err := sql.Open("sqlite", "file:"+path+"?"+params.Encode())
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to sqlite at %s: %w", path, err)
	}

	return &Client{DB: db}, nil
}

// WithTx runs fn in a transaction. It commits when fn returns nil and rolls
// back otherwise.
func (c *Client) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once committed

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

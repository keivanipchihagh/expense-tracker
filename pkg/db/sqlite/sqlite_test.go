package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestNewClient_Pragmas(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	var journalMode string
	require.NoError(t, client.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode))
	assert.Equal(t, "wal", journalMode)

	var busyTimeout int
	require.NoError(t, client.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout))
	assert.Equal(t, 5000, busyTimeout)

	var foreignKeys int
	require.NoError(t, client.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys))
	assert.Equal(t, 1, foreignKeys)
}

func TestNewClient_BadPath(t *testing.T) {
	_, err := NewClient(context.Background(), filepath.Join(t.TempDir(), "missing-dir", "test.db"))
	assert.Error(t, err)
}

func TestNewClient_PanicsOnEmptyPath(t *testing.T) {
	assert.Panics(t, func() { _, _ = NewClient(context.Background(), "") })
}

func TestWithTx(t *testing.T) {
	errFail := errors.New("fail")

	tests := []struct {
		name      string
		fn        func(tx *sql.Tx) error
		wantErr   error
		wantCount int
	}{
		{
			name: "commits on success",
			fn: func(tx *sql.Tx) error {
				_, err := tx.Exec("INSERT INTO items (name) VALUES ('a')")
				return err
			},
			wantCount: 1,
		},
		{
			name: "rolls back on error",
			fn: func(tx *sql.Tx) error {
				if _, err := tx.Exec("INSERT INTO items (name) VALUES ('a')"); err != nil {
					return err
				}
				return errFail
			},
			wantErr:   errFail,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t)
			ctx := context.Background()
			_, err := client.ExecContext(ctx, "CREATE TABLE items (name TEXT)")
			require.NoError(t, err)

			err = client.WithTx(ctx, tt.fn)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			var count int
			require.NoError(t, client.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count))
			assert.Equal(t, tt.wantCount, count)
		})
	}
}

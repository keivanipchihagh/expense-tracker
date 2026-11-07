package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) *sqlite.Client {
	t.Helper()
	client, err := sqlite.NewClient(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

type logRow struct {
	expenseId string
	action    string
	before    sql.NullString
	after     sql.NullString
	createdAt string
}

func readLogs(t *testing.T, client *sqlite.Client) []logRow {
	t.Helper()
	rows, err := client.Query(`SELECT expense_id, action, "before", "after", created_at FROM logs ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()

	var out []logRow
	for rows.Next() {
		var r logRow
		require.NoError(t, rows.Scan(&r.expenseId, &r.action, &r.before, &r.after, &r.createdAt))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestNewLogRepository_Idempotent(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	_, err := NewLogRepository(ctx, client)
	require.NoError(t, err)
	_, err = NewLogRepository(ctx, client)
	require.NoError(t, err)
}

func TestNewLogRepository_PanicsOnNilClient(t *testing.T) {
	assert.Panics(t, func() { _, _ = NewLogRepository(context.Background(), nil) })
}

func TestLogRepository_Create(t *testing.T) {
	snapshot := json.RawMessage(`{"id":"e1","amount":"12.50"}`)

	tests := []struct {
		name       string
		log        models.Log
		wantErr    bool
		wantAction string
		wantBefore sql.NullString
		wantAfter  sql.NullString
	}{
		{
			name:       "create stores after and a null before",
			log:        *models.NewLog("e1", models.LogActionCreate, nil, snapshot),
			wantAction: "create",
			wantAfter:  sql.NullString{String: string(snapshot), Valid: true},
		},
		{
			name:       "update stores both snapshots",
			log:        *models.NewLog("e1", models.LogActionUpdate, snapshot, snapshot),
			wantAction: "update",
			wantBefore: sql.NullString{String: string(snapshot), Valid: true},
			wantAfter:  sql.NullString{String: string(snapshot), Valid: true},
		},
		{
			name:       "delete stores before and a null after",
			log:        *models.NewLog("e1", models.LogActionDelete, snapshot, nil),
			wantAction: "delete",
			wantBefore: sql.NullString{String: string(snapshot), Valid: true},
		},
		{
			name:    "unspecified action is rejected by the table",
			log:     models.Log{ExpenseId: "e1", Action: models.LogActionUnspecified, CreatedAt: time.Now()},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t)
			ctx := context.Background()
			repo, err := NewLogRepository(ctx, client)
			require.NoError(t, err)

			err = repo.Create(ctx, client, tt.log)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, readLogs(t, client))
				return
			}
			require.NoError(t, err)

			rows := readLogs(t, client)
			require.Len(t, rows, 1)
			assert.Equal(t, "e1", rows[0].expenseId)
			assert.Equal(t, tt.wantAction, rows[0].action)
			assert.Equal(t, tt.wantBefore, rows[0].before)
			assert.Equal(t, tt.wantAfter, rows[0].after)

			createdAt, err := time.Parse(time.RFC3339Nano, rows[0].createdAt)
			require.NoError(t, err)
			assert.WithinDuration(t, tt.log.CreatedAt, createdAt, time.Microsecond)
		})
	}
}

func TestLogRepository_Create_JoinsTransaction(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	repo, err := NewLogRepository(ctx, client)
	require.NoError(t, err)

	errAbort := errors.New("abort")
	err = client.WithTx(ctx, func(tx *sql.Tx) error {
		log := models.NewLog("e1", models.LogActionCreate, nil, json.RawMessage(`{}`))
		require.NoError(t, repo.Create(ctx, tx, *log))
		return errAbort
	})
	require.ErrorIs(t, err, errAbort)

	assert.Empty(t, readLogs(t, client), "rolled-back transaction must not leave a log")
}

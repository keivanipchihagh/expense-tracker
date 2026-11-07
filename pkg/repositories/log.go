package repositories

//go:generate go tool mockgen -source=log.go -destination=log_mock.go -package=repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

type LogRepository interface {
	// Create inserts log through exec, so it can join the caller's transaction.
	Create(ctx context.Context, exec sqlite.Executor, log models.Log) error
}

type LogRepositoryImpl struct{}

// "before" and "after" are SQLite keywords, so they are always quoted.
const logTableSchema = `
	CREATE TABLE IF NOT EXISTS logs (
		id         INTEGER  PRIMARY KEY AUTOINCREMENT,
		expense_id TEXT     NOT NULL,
		action     TEXT     NOT NULL CHECK (action IN ('create', 'update', 'delete')),
		"before"   TEXT,
		"after"    TEXT,
		created_at DATETIME NOT NULL
	)
`

// NewLogRepository creates the logs table if it does not exist.
func NewLogRepository(ctx context.Context, client *sqlite.Client) (LogRepository, error) {
	if client == nil {
		panic("log repository: sqlite client must not be nil")
	}
	if _, err := client.ExecContext(ctx, logTableSchema); err != nil {
		return nil, fmt.Errorf("create logs table: %w", err)
	}
	return &LogRepositoryImpl{}, nil
}

func (r *LogRepositoryImpl) Create(ctx context.Context, exec sqlite.Executor, log models.Log) error {
	query := `
		INSERT INTO logs (expense_id, action, "before", "after", created_at)
		VALUES (?, ?, ?, ?, ?)
	`
	_, err := exec.ExecContext(ctx, query,
		log.ExpenseId,
		log.Action.String(),
		nullableJson(log.Before),
		nullableJson(log.After),
		log.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert log: %w", err)
	}
	return nil
}

// nullableJson stores a missing snapshot as NULL and a present one as text.
func nullableJson(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

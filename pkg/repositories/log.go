package repositories

//go:generate go tool mockgen -source=log.go -destination=log_mock.go -package=repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

type LogRepository interface {
	// Create inserts log through exec, so it can join the caller's transaction.
	Create(ctx context.Context, exec sqlite.Executor, log models.Log) error
	// List returns the user's logs, oldest first, including those of
	// expenses since deleted.
	List(ctx context.Context, exec sqlite.Executor, userId string) ([]models.Log, error)
}

type LogRepositoryImpl struct{}

// "before" and "after" are SQLite keywords, so they are always quoted.
const logTableSchema = `
	CREATE TABLE IF NOT EXISTS logs (
		id         INTEGER  PRIMARY KEY AUTOINCREMENT,
		expense_id TEXT     NOT NULL,
		user_id    TEXT,
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
	// Logs from before accounts get their owner with the expenses.
	if err := addUserIdColumn(ctx, client, "logs", false); err != nil {
		return nil, err
	}
	return &LogRepositoryImpl{}, nil
}

func (r *LogRepositoryImpl) Create(ctx context.Context, exec sqlite.Executor, log models.Log) error {
	query := `
		INSERT INTO logs (expense_id, user_id, action, "before", "after", created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	_, err := exec.ExecContext(ctx, query,
		log.ExpenseId,
		log.UserId,
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

func (r *LogRepositoryImpl) List(ctx context.Context, exec sqlite.Executor, userId string) ([]models.Log, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT id, expense_id, user_id, action, "before", "after", CAST(created_at AS TEXT)
		FROM logs WHERE user_id = ? ORDER BY id`, userId)
	if err != nil {
		return nil, fmt.Errorf("list logs: %w", err)
	}
	defer rows.Close()
	logs := []models.Log{}
	for rows.Next() {
		var (
			l             models.Log
			action        string
			before, after sql.NullString
			createdAt     string
		)
		if err := rows.Scan(&l.Id, &l.ExpenseId, &l.UserId, &action, &before, &after, &createdAt); err != nil {
			return nil, fmt.Errorf("scan log: %w", err)
		}
		l.Action = models.ParseLogAction(action)
		if before.Valid {
			l.Before = json.RawMessage(before.String)
		}
		if after.Valid {
			l.After = json.RawMessage(after.String)
		}
		if l.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("parse log created_at: %w", err)
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list logs: %w", err)
	}
	return logs, nil
}

// nullableJson stores a missing snapshot as NULL and a present one as text.
func nullableJson(raw json.RawMessage) any {
	if raw == nil {
		return nil
	}
	return string(raw)
}

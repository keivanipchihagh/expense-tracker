package repositories

//go:generate go tool mockgen -source=expense.go -destination=expense_mock.go -package=repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// likeEscaper escapes LIKE wildcards (and the escape character itself).
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// ErrExpenseNotFound is returned when no expense has the given id.
var ErrExpenseNotFound = errors.New("expense not found")

type ExpenseRepository interface {
	// Create inserts the expense and its create log in one transaction.
	Create(ctx context.Context, expense models.Expense) error
	// Get returns the expense with the given id, or ErrExpenseNotFound.
	Get(ctx context.Context, id string) (models.Expense, error)
	// List returns expenses matching the filter, oldest first. The dates
	// bound the local date (the date part of occurred_at).
	List(ctx context.Context, filter models.ExpenseFilter) ([]models.Expense, error)
	// Update overwrites every column except id and created_at and writes an
	// update log in one transaction. Returns ErrExpenseNotFound if missing.
	Update(ctx context.Context, expense models.Expense) error
	// Delete removes the expense and writes a delete log in one transaction.
	// Returns ErrExpenseNotFound if missing.
	Delete(ctx context.Context, id string) error
}

type ExpenseRepositoryImpl struct {
	client  *sqlite.Client
	logRepo LogRepository
}

const expenseTableSchema = `
	CREATE TABLE IF NOT EXISTS expenses (
		id             TEXT     PRIMARY KEY,
		amount         TEXT     NOT NULL,
		occurred_at    DATETIME NOT NULL,
		category       TEXT     NOT NULL,
		merchant       TEXT,
		note           TEXT,
		created_at     DATETIME NOT NULL,
		updated_at     DATETIME NOT NULL
	)
`

const expenseIndexSchema = `CREATE INDEX IF NOT EXISTS expenses_occurred_at ON expenses (occurred_at)`

// expenseSelect reads every column. Timestamps are cast to TEXT so the driver
// returns them exactly as stored instead of converting them to time.Time.
const expenseSelect = `
	SELECT id, amount, CAST(occurred_at AS TEXT), category, merchant, note,
	       CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
	FROM expenses
`

// NewExpenseRepository creates the expenses table and its index if they do
// not exist, and migrates older databases.
func NewExpenseRepository(ctx context.Context, client *sqlite.Client, logRepo LogRepository) (ExpenseRepository, error) {
	if client == nil || logRepo == nil {
		panic("expense repository: client and log repository must not be nil")
	}
	for _, stmt := range []string{expenseTableSchema, expenseIndexSchema} {
		if _, err := client.ExecContext(ctx, stmt); err != nil {
			return nil, fmt.Errorf("create expenses table: %w", err)
		}
	}
	if err := dropCurrencyColumn(ctx, client); err != nil {
		return nil, err
	}
	return &ExpenseRepositoryImpl{client: client, logRepo: logRepo}, nil
}

// dropCurrencyColumn removes the currency column from databases created
// before amounts lost their currency. Amounts are kept as they are.
func dropCurrencyColumn(ctx context.Context, client *sqlite.Client) error {
	var n int
	err := client.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('expenses') WHERE name = 'currency'",
	).Scan(&n)
	if err != nil {
		return fmt.Errorf("inspect expenses table: %w", err)
	}
	if n == 0 {
		return nil
	}
	if _, err := client.ExecContext(ctx, "ALTER TABLE expenses DROP COLUMN currency"); err != nil {
		return fmt.Errorf("drop currency column: %w", err)
	}
	return nil
}

func (r *ExpenseRepositoryImpl) Create(ctx context.Context, expense models.Expense) error {
	after, err := json.Marshal(expense)
	if err != nil {
		return fmt.Errorf("snapshot expense: %w", err)
	}

	query := `
		INSERT INTO expenses (id, amount, occurred_at, category, merchant, note,
		                      created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	return r.client.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query,
			expense.Id,
			expense.Amount,
			formatTime(expense.OccurredAt),
			string(expense.Category),
			expense.Merchant,
			expense.Note,
			formatTime(expense.CreatedAt.UTC()),
			formatTime(expense.UpdatedAt.UTC()),
		)
		if err != nil {
			return fmt.Errorf("insert expense: %w", err)
		}
		return r.logRepo.Create(ctx, tx, *models.NewLog(expense.Id, models.LogActionCreate, nil, after))
	})
}

func (r *ExpenseRepositoryImpl) Get(ctx context.Context, id string) (models.Expense, error) {
	return r.get(ctx, r.client, id)
}

func (r *ExpenseRepositoryImpl) List(ctx context.Context, filter models.ExpenseFilter) ([]models.Expense, error) {
	// occurred_at is stored as RFC 3339 text in its original offset, so it
	// starts with the local date and plain string comparison filters by local
	// date (and can use the index).
	query := expenseSelect + " WHERE 1 = 1"
	var args []any
	if !filter.DateFrom.IsZero() {
		query += " AND occurred_at >= ?"
		args = append(args, filter.DateFrom.Format(time.DateOnly))
	}
	if !filter.DateTo.IsZero() {
		query += " AND occurred_at < ?"
		args = append(args, filter.DateTo.AddDate(0, 0, 1).Format(time.DateOnly))
	}
	if filter.Category != "" {
		query += " AND category = ?"
		args = append(args, string(filter.Category))
	}
	if filter.Merchant != "" {
		// LIKE ignores ASCII case. % and _ in the search are matched literally.
		query += ` AND merchant LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscaper.Replace(filter.Merchant)+"%")
	}
	// julianday() converts every offset to the same instant, so the order is
	// chronological even when expenses were recorded in different offsets.
	query += " ORDER BY julianday(occurred_at), id"

	rows, err := r.client.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list expenses: %w", err)
	}
	defer rows.Close()

	expenses := []models.Expense{}
	for rows.Next() {
		e, err := scanExpense(rows)
		if err != nil {
			return nil, err
		}
		expenses = append(expenses, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list expenses: %w", err)
	}
	return expenses, nil
}

func (r *ExpenseRepositoryImpl) Update(ctx context.Context, expense models.Expense) error {
	after, err := json.Marshal(expense)
	if err != nil {
		return fmt.Errorf("snapshot expense: %w", err)
	}

	query := `
		UPDATE expenses
		SET amount = ?, occurred_at = ?, category = ?, merchant = ?,
		    note = ?, updated_at = ?
		WHERE id = ?
	`
	return r.client.WithTx(ctx, func(tx *sql.Tx) error {
		current, err := r.get(ctx, tx, expense.Id)
		if err != nil {
			return err
		}
		before, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("snapshot expense: %w", err)
		}

		_, err = tx.ExecContext(ctx, query,
			expense.Amount,
			formatTime(expense.OccurredAt),
			string(expense.Category),
			expense.Merchant,
			expense.Note,
			formatTime(expense.UpdatedAt.UTC()),
			expense.Id,
		)
		if err != nil {
			return fmt.Errorf("update expense: %w", err)
		}
		return r.logRepo.Create(ctx, tx, *models.NewLog(expense.Id, models.LogActionUpdate, before, after))
	})
}

func (r *ExpenseRepositoryImpl) Delete(ctx context.Context, id string) error {
	return r.client.WithTx(ctx, func(tx *sql.Tx) error {
		current, err := r.get(ctx, tx, id)
		if err != nil {
			return err
		}
		before, err := json.Marshal(current)
		if err != nil {
			return fmt.Errorf("snapshot expense: %w", err)
		}

		if _, err := tx.ExecContext(ctx, "DELETE FROM expenses WHERE id = ?", id); err != nil {
			return fmt.Errorf("delete expense: %w", err)
		}
		return r.logRepo.Create(ctx, tx, *models.NewLog(id, models.LogActionDelete, before, nil))
	})
}

func (r *ExpenseRepositoryImpl) get(ctx context.Context, exec sqlite.Executor, id string) (models.Expense, error) {
	e, err := scanExpense(exec.QueryRowContext(ctx, expenseSelect+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return models.Expense{}, ErrExpenseNotFound
	}
	return e, err
}

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanExpense(s scanner) (models.Expense, error) {
	var (
		e                                models.Expense
		category                         string
		occurredAt, createdAt, updatedAt string
		merchant, note                   sql.NullString
	)
	err := s.Scan(&e.Id, &e.Amount, &occurredAt, &category,
		&merchant, &note, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Expense{}, err
	}
	if err != nil {
		return models.Expense{}, fmt.Errorf("scan expense: %w", err)
	}

	e.Category = models.Category(category)
	e.Merchant = nullableString(merchant)
	e.Note = nullableString(note)

	for _, ts := range []struct {
		dst *time.Time
		src string
	}{{&e.OccurredAt, occurredAt}, {&e.CreatedAt, createdAt}, {&e.UpdatedAt, updatedAt}} {
		t, err := time.Parse(time.RFC3339Nano, ts.src)
		if err != nil {
			return models.Expense{}, fmt.Errorf("parse stored timestamp %q: %w", ts.src, err)
		}
		*ts.dst = t
	}
	return e, nil
}

// formatTime stores a timestamp as RFC 3339 text, keeping its offset.
func formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

func nullableString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

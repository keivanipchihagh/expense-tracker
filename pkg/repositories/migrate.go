package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
)

// hasColumn reports whether table has a column with the given name.
func hasColumn(ctx context.Context, client *sqlite.Client, table, column string) (bool, error) {
	var n int
	err := client.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", table, err)
	}
	return n > 0, nil
}

// addUserIdColumn adds user_id to a table created before accounts existed,
// and indexes it. Rows of tables whose content is worthless without an
// owner (sessions, codes, tokens) are deleted: their holders sign in again.
func addUserIdColumn(ctx context.Context, client *sqlite.Client, table string, dropUnowned bool) error {
	exists, err := hasColumn(ctx, client, table, "user_id")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := client.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN user_id TEXT"); err != nil {
			return fmt.Errorf("add user_id to %s: %w", table, err)
		}
	}
	if dropUnowned {
		if _, err := client.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id IS NULL"); err != nil {
			return fmt.Errorf("delete unowned rows from %s: %w", table, err)
		}
	}
	if _, err := client.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS "+table+"_user_id ON "+table+" (user_id)"); err != nil {
		return fmt.Errorf("index %s.user_id: %w", table, err)
	}
	return nil
}

// CountUnowned counts expenses created before accounts existed, which no
// user can see until they are assigned an owner.
func CountUnowned(ctx context.Context, client *sqlite.Client) (int, error) {
	var n int
	if err := client.QueryRowContext(ctx, "SELECT COUNT(*) FROM expenses WHERE user_id IS NULL").Scan(&n); err != nil {
		return 0, fmt.Errorf("count unowned expenses: %w", err)
	}
	return n, nil
}

// AssignUnowned gives every expense and log row without an owner to userId,
// in one transaction.
func AssignUnowned(ctx context.Context, client *sqlite.Client, userId string) error {
	return client.WithTx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"expenses", "logs"} {
			if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET user_id = ? WHERE user_id IS NULL", userId); err != nil {
				return fmt.Errorf("assign unowned %s: %w", table, err)
			}
		}
		return nil
	})
}

// LegacyCurrency returns the display currency saved in the settings table
// from before it moved onto users, and whether there was one.
func LegacyCurrency(ctx context.Context, client *sqlite.Client) (string, bool, error) {
	var n int
	if err := client.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'settings'").Scan(&n); err != nil {
		return "", false, fmt.Errorf("look for settings table: %w", err)
	}
	if n == 0 {
		return "", false, nil
	}
	var value string
	err := client.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'currency'").Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read legacy currency: %w", err)
	}
	return value, true, nil
}

// DropLegacySettings removes the settings table once its value has moved.
func DropLegacySettings(ctx context.Context, client *sqlite.Client) error {
	if _, err := client.ExecContext(ctx, "DROP TABLE IF EXISTS settings"); err != nil {
		return fmt.Errorf("drop settings table: %w", err)
	}
	return nil
}

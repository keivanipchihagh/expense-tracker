package repositories

//go:generate go tool mockgen -source=setting.go -destination=setting_mock.go -package=repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
)

// ErrSettingNotFound is returned when a setting has never been saved.
var ErrSettingNotFound = errors.New("setting not found")

// SettingRepository stores the owner's preferences as key/value pairs.
type SettingRepository interface {
	// Get returns the value saved for key, or ErrSettingNotFound.
	Get(ctx context.Context, key string) (string, error)
	// Set saves value for key, replacing any earlier value.
	Set(ctx context.Context, key, value string) error
}

type SettingRepositoryImpl struct {
	client *sqlite.Client
}

const settingTableSchema = `
	CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)
`

// NewSettingRepository creates the settings table if it does not exist.
func NewSettingRepository(ctx context.Context, client *sqlite.Client) (SettingRepository, error) {
	if client == nil {
		panic("setting repository: sqlite client must not be nil")
	}
	if _, err := client.ExecContext(ctx, settingTableSchema); err != nil {
		return nil, fmt.Errorf("create settings table: %w", err)
	}
	return &SettingRepositoryImpl{client: client}, nil
}

func (r *SettingRepositoryImpl) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.client.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSettingNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get setting: %w", err)
	}
	return value, nil
}

func (r *SettingRepositoryImpl) Set(ctx context.Context, key, value string) error {
	_, err := r.client.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	if err != nil {
		return fmt.Errorf("set setting: %w", err)
	}
	return nil
}

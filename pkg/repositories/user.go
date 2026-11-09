package repositories

//go:generate go tool mockgen -source=user.go -destination=user_mock.go -package=repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

var (
	// ErrUserNotFound is returned when no user matches.
	ErrUserNotFound = errors.New("user not found")
	// ErrEmailTaken is returned when another user already has the email.
	ErrEmailTaken = errors.New("email is taken")
)

type UserRepository interface {
	// Create inserts the user. Returns ErrEmailTaken.
	Create(ctx context.Context, user models.User) error
	// Get returns the user with the given id, or ErrUserNotFound.
	Get(ctx context.Context, id string) (models.User, error)
	// GetByEmail returns the user with the given (lowercase) email, or
	// ErrUserNotFound.
	GetByEmail(ctx context.Context, email string) (models.User, error)
	// Update overwrites every column except id and created_at. Returns
	// ErrUserNotFound or ErrEmailTaken.
	Update(ctx context.Context, user models.User) error
	// Delete removes the user and everything that is theirs, in one
	// transaction: expenses, change logs, sessions, OAuth codes and tokens,
	// and the OAuth clients only they were using. Returns ErrUserNotFound.
	Delete(ctx context.Context, id string) error
}

type UserRepositoryImpl struct {
	client *sqlite.Client
}

// Emails are stored lowercase, so UNIQUE is case-insensitive.
const userTableSchema = `
	CREATE TABLE IF NOT EXISTS users (
		id            TEXT     PRIMARY KEY,
		email         TEXT     NOT NULL UNIQUE,
		first_name    TEXT     NOT NULL DEFAULT '',
		last_name     TEXT     NOT NULL DEFAULT '',
		password_hash TEXT     NOT NULL,
		currency      TEXT     NOT NULL,
		created_at    DATETIME NOT NULL,
		updated_at    DATETIME NOT NULL
	)
`

const userSelect = `
	SELECT id, email, first_name, last_name, password_hash, currency, CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
	FROM users
`

// NewUserRepository creates the users table if it does not exist.
func NewUserRepository(ctx context.Context, client *sqlite.Client) (UserRepository, error) {
	if client == nil {
		panic("user repository: sqlite client must not be nil")
	}
	if _, err := client.ExecContext(ctx, userTableSchema); err != nil {
		return nil, fmt.Errorf("create users table: %w", err)
	}
	// Users created before names existed get empty ones.
	for _, column := range []string{"first_name", "last_name"} {
		has, err := hasColumn(ctx, client, "users", column)
		if err != nil {
			return nil, err
		}
		if !has {
			if _, err := client.ExecContext(ctx, "ALTER TABLE users ADD COLUMN "+column+" TEXT NOT NULL DEFAULT ''"); err != nil {
				return nil, fmt.Errorf("add %s to users: %w", column, err)
			}
		}
	}
	if err := dropUsernames(ctx, client); err != nil {
		return nil, err
	}
	return &UserRepositoryImpl{client: client}, nil
}

// dropUsernames removes the username column accounts used to have. SQLite
// can't drop a UNIQUE column, so the table is copied without it.
func dropUsernames(ctx context.Context, client *sqlite.Client) error {
	has, err := hasColumn(ctx, client, "users", "username")
	if err != nil || !has {
		return err
	}
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("drop usernames: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		strings.Replace(userTableSchema, "IF NOT EXISTS users", "users_new", 1),
		`INSERT INTO users_new (id, email, first_name, last_name, password_hash, currency, created_at, updated_at)
		 SELECT id, email, first_name, last_name, password_hash, currency, created_at, updated_at FROM users`,
		`DROP TABLE users`,
		`ALTER TABLE users_new RENAME TO users`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("drop usernames: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("drop usernames: %w", err)
	}
	return nil
}

func (r *UserRepositoryImpl) Create(ctx context.Context, u models.User) error {
	_, err := r.client.ExecContext(ctx,
		`INSERT INTO users (id, email, first_name, last_name, password_hash, currency, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Id, u.Email, u.FirstName, u.LastName, u.PasswordHash, string(u.Currency), formatTime(u.CreatedAt.UTC()), formatTime(u.UpdatedAt.UTC()),
	)
	if err != nil {
		return userWriteError("insert user", err)
	}
	return nil
}

func (r *UserRepositoryImpl) Get(ctx context.Context, id string) (models.User, error) {
	return scanUser(r.client.QueryRowContext(ctx, userSelect+" WHERE id = ?", id))
}

func (r *UserRepositoryImpl) GetByEmail(ctx context.Context, email string) (models.User, error) {
	return scanUser(r.client.QueryRowContext(ctx, userSelect+" WHERE email = ?", email))
}

func (r *UserRepositoryImpl) Update(ctx context.Context, u models.User) error {
	res, err := r.client.ExecContext(ctx,
		`UPDATE users SET email = ?, first_name = ?, last_name = ?, password_hash = ?, currency = ?, updated_at = ? WHERE id = ?`,
		u.Email, u.FirstName, u.LastName, u.PasswordHash, string(u.Currency), formatTime(u.UpdatedAt.UTC()), u.Id,
	)
	if err != nil {
		return userWriteError("update user", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *UserRepositoryImpl) Delete(ctx context.Context, id string) error {
	return r.client.WithTx(ctx, func(tx *sql.Tx) error {
		// Clients the user was using; removed below if no one else is.
		rows, err := tx.QueryContext(ctx,
			`SELECT client_id FROM oauth_codes WHERE user_id = ? UNION SELECT client_id FROM oauth_tokens WHERE user_id = ?`, id, id)
		if err != nil {
			return fmt.Errorf("find user's oauth clients: %w", err)
		}
		var clients []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				rows.Close()
				return fmt.Errorf("scan oauth client: %w", err)
			}
			clients = append(clients, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("find user's oauth clients: %w", err)
		}

		for _, table := range []string{"expenses", "logs", "sessions", "oauth_codes", "oauth_tokens"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id = ?", id); err != nil {
				return fmt.Errorf("delete user's %s: %w", table, err)
			}
		}
		for _, c := range clients {
			_, err := tx.ExecContext(ctx, `
				DELETE FROM oauth_clients
				WHERE id = ?
				  AND id NOT IN (SELECT client_id FROM oauth_tokens)
				  AND id NOT IN (SELECT client_id FROM oauth_codes)`, c)
			if err != nil {
				return fmt.Errorf("delete unused oauth client: %w", err)
			}
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
		if err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrUserNotFound
		}
		return nil
	})
}

// userWriteError turns a UNIQUE violation on the email into ErrEmailTaken;
// SQLite names the column in the message.
func userWriteError(op string, err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
		return ErrEmailTaken
	}
	return fmt.Errorf("%s: %w", op, err)
}

func scanUser(row *sql.Row) (models.User, error) {
	var (
		u                    models.User
		currency             string
		createdAt, updatedAt string
	)
	err := row.Scan(&u.Id, &u.Email, &u.FirstName, &u.LastName, &u.PasswordHash, &currency, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.User{}, ErrUserNotFound
	}
	if err != nil {
		return models.User{}, fmt.Errorf("scan user: %w", err)
	}
	u.Currency = models.Currency(currency)
	if u.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return models.User{}, fmt.Errorf("parse created_at: %w", err)
	}
	if u.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return models.User{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return u, nil
}

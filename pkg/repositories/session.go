package repositories

//go:generate go tool mockgen -source=session.go -destination=session_mock.go -package=repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// ErrSessionNotFound is returned when no session has the given hash.
var ErrSessionNotFound = errors.New("session not found")

type SessionRepository interface {
	Create(ctx context.Context, session models.Session) error
	// Get returns the session with the given token hash, or ErrSessionNotFound.
	Get(ctx context.Context, tokenHash string) (models.Session, error)
	// Delete removes the session; deleting a missing session is not an error.
	Delete(ctx context.Context, tokenHash string) error
	// DeleteExpired removes sessions that expired before now.
	DeleteExpired(ctx context.Context, now time.Time) error
	// DeleteOthers removes the user's sessions except the one with keepHash
	// (signs out other browsers, e.g. after a password change).
	DeleteOthers(ctx context.Context, userId, keepHash string) error
}

type SessionRepositoryImpl struct {
	client *sqlite.Client
}

// Timestamps are unix seconds so expiry checks are plain integer comparisons.
const sessionTableSchema = `
	CREATE TABLE IF NOT EXISTS sessions (
		token_hash TEXT    PRIMARY KEY,
		user_id    TEXT,
		expires_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL
	)
`

// NewSessionRepository creates the sessions table if it does not exist.
func NewSessionRepository(ctx context.Context, client *sqlite.Client) (SessionRepository, error) {
	if client == nil {
		panic("session repository: sqlite client must not be nil")
	}
	if _, err := client.ExecContext(ctx, sessionTableSchema); err != nil {
		return nil, fmt.Errorf("create sessions table: %w", err)
	}
	// Sessions from before accounts belong to nobody: sign in again.
	if err := addUserIdColumn(ctx, client, "sessions", true); err != nil {
		return nil, err
	}
	return &SessionRepositoryImpl{client: client}, nil
}

func (r *SessionRepositoryImpl) Create(ctx context.Context, s models.Session) error {
	_, err := r.client.ExecContext(ctx,
		"INSERT INTO sessions (token_hash, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)",
		s.TokenHash, s.UserId, s.ExpiresAt.Unix(), s.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (r *SessionRepositoryImpl) Get(ctx context.Context, tokenHash string) (models.Session, error) {
	var (
		s                    models.Session
		expiresAt, createdAt int64
	)
	err := r.client.QueryRowContext(ctx,
		"SELECT token_hash, user_id, expires_at, created_at FROM sessions WHERE token_hash = ?", tokenHash,
	).Scan(&s.TokenHash, &s.UserId, &expiresAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Session{}, ErrSessionNotFound
	}
	if err != nil {
		return models.Session{}, fmt.Errorf("get session: %w", err)
	}
	s.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	s.CreatedAt = time.Unix(createdAt, 0).UTC()
	return s, nil
}

func (r *SessionRepositoryImpl) Delete(ctx context.Context, tokenHash string) error {
	if _, err := r.client.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (r *SessionRepositoryImpl) DeleteExpired(ctx context.Context, now time.Time) error {
	if _, err := r.client.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", now.Unix()); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}

func (r *SessionRepositoryImpl) DeleteOthers(ctx context.Context, userId, keepHash string) error {
	if _, err := r.client.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ? AND token_hash != ?", userId, keepHash); err != nil {
		return fmt.Errorf("delete other sessions: %w", err)
	}
	return nil
}

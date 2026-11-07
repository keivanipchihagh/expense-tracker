package repositories

//go:generate go tool mockgen -source=oauth.go -destination=oauth_mock.go -package=repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// ErrOAuthNotFound is returned when a client, code or token does not exist.
var ErrOAuthNotFound = errors.New("oauth record not found")

type OAuthRepository interface {
	CreateClient(ctx context.Context, client models.OAuthClient) error
	// GetClient returns the client with the given id, or ErrOAuthNotFound.
	GetClient(ctx context.Context, id string) (models.OAuthClient, error)

	CreateCode(ctx context.Context, code models.OAuthCode) error
	// ConsumeCode deletes and returns the code in one statement, so a code
	// can be exchanged at most once. Returns ErrOAuthNotFound if missing.
	ConsumeCode(ctx context.Context, codeHash string) (models.OAuthCode, error)

	// CreateTokens stores tokens in one transaction.
	CreateTokens(ctx context.Context, tokens ...models.OAuthToken) error
	// GetToken returns the token of the given kind, or ErrOAuthNotFound.
	GetToken(ctx context.Context, tokenHash string, kind models.OAuthTokenKind) (models.OAuthToken, error)
	// ConsumeToken deletes and returns the token of the given kind in one
	// statement, so a refresh token can be used at most once.
	ConsumeToken(ctx context.Context, tokenHash string, kind models.OAuthTokenKind) (models.OAuthToken, error)

	// DeleteExpired removes codes and tokens that expired before now.
	DeleteExpired(ctx context.Context, now time.Time) error
}

type OAuthRepositoryImpl struct {
	client *sqlite.Client
}

// Timestamps are unix seconds so expiry checks are plain integer comparisons.
var oauthSchema = []string{
	`CREATE TABLE IF NOT EXISTS oauth_clients (
		id            TEXT    PRIMARY KEY,
		secret_hash   TEXT,
		name          TEXT    NOT NULL,
		redirect_uris TEXT    NOT NULL,
		auth_method   TEXT    NOT NULL,
		created_at    INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS oauth_codes (
		code_hash      TEXT    PRIMARY KEY,
		client_id      TEXT    NOT NULL,
		redirect_uri   TEXT    NOT NULL,
		code_challenge TEXT    NOT NULL,
		expires_at     INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS oauth_tokens (
		token_hash TEXT    PRIMARY KEY,
		kind       TEXT    NOT NULL CHECK (kind IN ('access', 'refresh')),
		client_id  TEXT    NOT NULL,
		expires_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL
	)`,
}

// NewOAuthRepository creates the OAuth tables if they do not exist.
func NewOAuthRepository(ctx context.Context, client *sqlite.Client) (OAuthRepository, error) {
	if client == nil {
		panic("oauth repository: sqlite client must not be nil")
	}
	for _, stmt := range oauthSchema {
		if _, err := client.ExecContext(ctx, stmt); err != nil {
			return nil, fmt.Errorf("create oauth tables: %w", err)
		}
	}
	return &OAuthRepositoryImpl{client: client}, nil
}

func (r *OAuthRepositoryImpl) CreateClient(ctx context.Context, c models.OAuthClient) error {
	uris, err := json.Marshal(c.RedirectUris)
	if err != nil {
		return fmt.Errorf("encode redirect uris: %w", err)
	}
	_, err = r.client.ExecContext(ctx,
		`INSERT INTO oauth_clients (id, secret_hash, name, redirect_uris, auth_method, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.Id, c.SecretHash, c.Name, string(uris), c.AuthMethod, c.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert oauth client: %w", err)
	}
	return nil
}

func (r *OAuthRepositoryImpl) GetClient(ctx context.Context, id string) (models.OAuthClient, error) {
	var (
		c         models.OAuthClient
		secret    sql.NullString
		uris      string
		createdAt int64
	)
	err := r.client.QueryRowContext(ctx,
		`SELECT id, secret_hash, name, redirect_uris, auth_method, created_at FROM oauth_clients WHERE id = ?`, id,
	).Scan(&c.Id, &secret, &c.Name, &uris, &c.AuthMethod, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.OAuthClient{}, ErrOAuthNotFound
	}
	if err != nil {
		return models.OAuthClient{}, fmt.Errorf("get oauth client: %w", err)
	}

	if secret.Valid {
		c.SecretHash = &secret.String
	}
	if err := json.Unmarshal([]byte(uris), &c.RedirectUris); err != nil {
		return models.OAuthClient{}, fmt.Errorf("decode redirect uris: %w", err)
	}
	c.CreatedAt = time.Unix(createdAt, 0).UTC()
	return c, nil
}

func (r *OAuthRepositoryImpl) CreateCode(ctx context.Context, c models.OAuthCode) error {
	_, err := r.client.ExecContext(ctx,
		`INSERT INTO oauth_codes (code_hash, client_id, redirect_uri, code_challenge, expires_at)
		 VALUES (?, ?, ?, ?, ?)`,
		c.CodeHash, c.ClientId, c.RedirectUri, c.CodeChallenge, c.ExpiresAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert oauth code: %w", err)
	}
	return nil
}

func (r *OAuthRepositoryImpl) ConsumeCode(ctx context.Context, codeHash string) (models.OAuthCode, error) {
	var (
		c         models.OAuthCode
		expiresAt int64
	)
	err := r.client.QueryRowContext(ctx,
		`DELETE FROM oauth_codes WHERE code_hash = ?
		 RETURNING code_hash, client_id, redirect_uri, code_challenge, expires_at`, codeHash,
	).Scan(&c.CodeHash, &c.ClientId, &c.RedirectUri, &c.CodeChallenge, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.OAuthCode{}, ErrOAuthNotFound
	}
	if err != nil {
		return models.OAuthCode{}, fmt.Errorf("consume oauth code: %w", err)
	}
	c.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	return c, nil
}

func (r *OAuthRepositoryImpl) CreateTokens(ctx context.Context, tokens ...models.OAuthToken) error {
	return r.client.WithTx(ctx, func(tx *sql.Tx) error {
		for _, t := range tokens {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO oauth_tokens (token_hash, kind, client_id, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
				t.TokenHash, string(t.Kind), t.ClientId, t.ExpiresAt.Unix(), t.CreatedAt.Unix(),
			)
			if err != nil {
				return fmt.Errorf("insert oauth token: %w", err)
			}
		}
		return nil
	})
}

func (r *OAuthRepositoryImpl) GetToken(ctx context.Context, tokenHash string, kind models.OAuthTokenKind) (models.OAuthToken, error) {
	return scanToken(r.client.QueryRowContext(ctx,
		`SELECT token_hash, kind, client_id, expires_at, created_at FROM oauth_tokens
		 WHERE token_hash = ? AND kind = ?`, tokenHash, string(kind)))
}

func (r *OAuthRepositoryImpl) ConsumeToken(ctx context.Context, tokenHash string, kind models.OAuthTokenKind) (models.OAuthToken, error) {
	return scanToken(r.client.QueryRowContext(ctx,
		`DELETE FROM oauth_tokens WHERE token_hash = ? AND kind = ?
		 RETURNING token_hash, kind, client_id, expires_at, created_at`, tokenHash, string(kind)))
}

func (r *OAuthRepositoryImpl) DeleteExpired(ctx context.Context, now time.Time) error {
	return r.client.WithTx(ctx, func(tx *sql.Tx) error {
		for _, table := range []string{"oauth_codes", "oauth_tokens"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE expires_at < ?", now.Unix()); err != nil {
				return fmt.Errorf("delete expired from %s: %w", table, err)
			}
		}
		return nil
	})
}

func scanToken(row *sql.Row) (models.OAuthToken, error) {
	var (
		t                    models.OAuthToken
		kind                 string
		expiresAt, createdAt int64
	)
	err := row.Scan(&t.TokenHash, &kind, &t.ClientId, &expiresAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.OAuthToken{}, ErrOAuthNotFound
	}
	if err != nil {
		return models.OAuthToken{}, fmt.Errorf("scan oauth token: %w", err)
	}
	t.Kind = models.OAuthTokenKind(kind)
	t.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	t.CreatedAt = time.Unix(createdAt, 0).UTC()
	return t, nil
}

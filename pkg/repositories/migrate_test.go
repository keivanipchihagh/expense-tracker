package repositories

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigration_FromBeforeAccounts(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	// Tables as they were before accounts: no user_id anywhere.
	for _, stmt := range []string{
		`CREATE TABLE sessions (token_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL)`,
		`INSERT INTO sessions VALUES ('old-session', 9999999999, 1)`,
		`CREATE TABLE oauth_tokens (token_hash TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('access', 'refresh')), client_id TEXT NOT NULL, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL)`,
		`INSERT INTO oauth_tokens VALUES ('old-token', 'access', 'claude', 9999999999, 1)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO settings VALUES ('currency', 'GBP')`,
	} {
		_, err := client.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}

	_, err := NewSessionRepository(ctx, client)
	require.NoError(t, err)
	oauth, err := NewOAuthRepository(ctx, client)
	require.NoError(t, err)

	for _, table := range []string{"sessions", "oauth_tokens", "oauth_codes"} {
		has, err := hasColumn(ctx, client, table, "user_id")
		require.NoError(t, err)
		assert.True(t, has, table)
	}
	var sessions int
	require.NoError(t, client.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessions))
	assert.Zero(t, sessions, "sessions without an owner are dropped: sign in again")
	_, err = oauth.GetToken(ctx, "old-token", "access")
	assert.ErrorIs(t, err, ErrOAuthNotFound, "so are tokens: apps sign in again")

	currency, ok, err := LegacyCurrency(ctx, client)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "GBP", currency)

	require.NoError(t, DropLegacySettings(ctx, client))
	_, ok, err = LegacyCurrency(ctx, client)
	require.NoError(t, err)
	assert.False(t, ok, "the settings table is gone")
	require.NoError(t, DropLegacySettings(ctx, client), "dropping twice is fine")

	// Running the migrations again changes nothing.
	_, err = NewSessionRepository(ctx, client)
	require.NoError(t, err)
	_, err = NewOAuthRepository(ctx, client)
	require.NoError(t, err)
}

package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUser(id, email string) models.User {
	at := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	return models.User{Id: id, Email: email, PasswordHash: "hash-" + id, Currency: models.CurrencyEUR, CreatedAt: at, UpdatedAt: at}
}

func TestUserRepository(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	repo, err := NewUserRepository(ctx, client)
	require.NoError(t, err)
	_, err = NewUserRepository(ctx, client)
	require.NoError(t, err, "creating the table twice is fine")

	alice := newUser("u1", "alice@example.com")
	require.NoError(t, repo.Create(ctx, alice))

	got, err := repo.Get(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, alice, got)
	got, err = repo.GetByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, alice, got)

	_, err = repo.Get(ctx, "missing")
	assert.ErrorIs(t, err, ErrUserNotFound)
	_, err = repo.GetByEmail(ctx, "nobody@example.com")
	assert.ErrorIs(t, err, ErrUserNotFound)

	assert.ErrorIs(t, repo.Create(ctx, newUser("u2", "alice@example.com")), ErrEmailTaken)

	bob := newUser("u2", "bob@example.com")
	require.NoError(t, repo.Create(ctx, bob))

	changed := alice
	changed.Email, changed.Currency, changed.PasswordHash = "a2@example.com", models.CurrencyGBP, "new-hash"
	changed.UpdatedAt = alice.UpdatedAt.Add(time.Hour)
	require.NoError(t, repo.Update(ctx, changed))
	got, err = repo.Get(ctx, "u1")
	require.NoError(t, err)
	assert.Equal(t, changed, got)

	taken := changed
	taken.Email = "bob@example.com"
	assert.ErrorIs(t, repo.Update(ctx, taken), ErrEmailTaken)
	assert.ErrorIs(t, repo.Update(ctx, newUser("ghost", "ghost@example.com")), ErrUserNotFound)
}

func TestUserRepository_DropsUsernames(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	// The table as it was when accounts had usernames.
	_, err := client.ExecContext(ctx, `
		CREATE TABLE users (
			id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, email TEXT NOT NULL UNIQUE,
			first_name TEXT NOT NULL DEFAULT '', last_name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL, currency TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
		);
		INSERT INTO users VALUES ('u1', 'alice', 'alice@example.com', 'Alice', 'Liddell', 'hash-u1', 'GBP', '2026-11-09T10:00:00Z', '2026-11-09T11:00:00Z');
		INSERT INTO users VALUES ('u2', 'bob', 'bob@example.com', '', '', 'hash-u2', 'EUR', '2026-11-09T10:00:00Z', '2026-11-09T10:00:00Z');`)
	require.NoError(t, err)

	repo, err := NewUserRepository(ctx, client)
	require.NoError(t, err)
	has, err := hasColumn(ctx, client, "users", "username")
	require.NoError(t, err)
	assert.False(t, has, "the username column is gone")

	got, err := repo.GetByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	assert.Equal(t, models.User{
		Id: "u1", Email: "alice@example.com", FirstName: "Alice", LastName: "Liddell", PasswordHash: "hash-u1", Currency: models.CurrencyGBP,
		CreatedAt: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 11, 9, 11, 0, 0, 0, time.UTC),
	}, got, "everything else is kept")
	_, err = repo.Get(ctx, "u2")
	require.NoError(t, err)

	// Emails are still unique, and new users need no username.
	assert.ErrorIs(t, repo.Create(ctx, newUser("u3", "bob@example.com")), ErrEmailTaken)
	require.NoError(t, repo.Create(ctx, newUser("u3", "carol@example.com")))

	_, err = NewUserRepository(ctx, client)
	require.NoError(t, err, "running it again changes nothing")
}

func TestSessionRepository_DeleteOthers(t *testing.T) {
	ctx := context.Background()
	repo, err := NewSessionRepository(ctx, newTestClient(t))
	require.NoError(t, err)
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for _, s := range []models.Session{
		{TokenHash: "keep", UserId: "u1", ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		{TokenHash: "other-browser", UserId: "u1", ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		{TokenHash: "someone-else", UserId: "u2", ExpiresAt: now.Add(time.Hour), CreatedAt: now},
	} {
		require.NoError(t, repo.Create(ctx, s))
	}

	require.NoError(t, repo.DeleteOthers(ctx, "u1", "keep"))

	got, err := repo.Get(ctx, "keep")
	require.NoError(t, err)
	assert.Equal(t, "u1", got.UserId)
	_, err = repo.Get(ctx, "other-browser")
	assert.ErrorIs(t, err, ErrSessionNotFound)
	_, err = repo.Get(ctx, "someone-else")
	assert.NoError(t, err, "other users are untouched")
}

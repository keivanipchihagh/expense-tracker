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

func TestUserRepository_Delete(t *testing.T) {
	ctx := context.Background()
	client := newTestClient(t)
	users, err := NewUserRepository(ctx, client)
	require.NoError(t, err)
	logs, err := NewLogRepository(ctx, client)
	require.NoError(t, err)
	expenses, err := NewExpenseRepository(ctx, client, logs)
	require.NoError(t, err)
	sessions, err := NewSessionRepository(ctx, client)
	require.NoError(t, err)
	oauth, err := NewOAuthRepository(ctx, client)
	require.NoError(t, err)

	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	for _, u := range []models.User{newUser("u1", "alice@example.com"), newUser("u2", "bob@example.com")} {
		require.NoError(t, users.Create(ctx, u))
		e := newExpense("e-"+u.Id, "2026-11-07T13:20:00+01:00")
		e.UserId = u.Id
		require.NoError(t, expenses.Create(ctx, e))
		require.NoError(t, sessions.Create(ctx, models.Session{TokenHash: "s-" + u.Id, UserId: u.Id, ExpiresAt: now.Add(time.Hour), CreatedAt: now}))
	}
	for _, c := range []string{"shared", "bobs-own", "nobodys-yet"} {
		require.NoError(t, oauth.CreateClient(ctx, models.OAuthClient{Id: c, Name: c, RedirectUris: []string{"http://localhost/cb"}, AuthMethod: "none", CreatedAt: now}))
	}
	token := func(hash, client, user string) models.OAuthToken {
		return models.OAuthToken{TokenHash: hash, Kind: models.OAuthTokenAccess, ClientId: client, UserId: user, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	}
	require.NoError(t, oauth.CreateTokens(ctx, token("t1", "shared", "u1"), token("t2", "shared", "u2")))
	require.NoError(t, oauth.CreateCode(ctx, models.OAuthCode{CodeHash: "c2", ClientId: "bobs-own", UserId: "u2", RedirectUri: "http://localhost/cb", ExpiresAt: now.Add(time.Minute)}))

	require.NoError(t, users.Delete(ctx, "u2"))

	rows := func(query string, args ...any) int {
		var n int
		require.NoError(t, client.QueryRow(query, args...).Scan(&n))
		return n
	}
	// Alice's rows stay (she has no OAuth code).
	for table, alices := range map[string]int{"expenses": 1, "logs": 1, "sessions": 1, "oauth_codes": 0, "oauth_tokens": 1} {
		assert.Zero(t, rows("SELECT COUNT(*) FROM "+table+" WHERE user_id = 'u2'"), "bob's %s are gone", table)
		assert.Equal(t, alices, rows("SELECT COUNT(*) FROM "+table+" WHERE user_id = 'u1'"), "alice's %s stay", table)
	}
	_, err = users.Get(ctx, "u2")
	assert.ErrorIs(t, err, ErrUserNotFound)
	_, err = users.Get(ctx, "u1")
	assert.NoError(t, err)
	assert.Equal(t, 1, rows("SELECT COUNT(*) FROM oauth_clients WHERE id = 'shared'"), "still used by alice")
	assert.Zero(t, rows("SELECT COUNT(*) FROM oauth_clients WHERE id = 'bobs-own'"), "only bob used it")
	assert.Equal(t, 1, rows("SELECT COUNT(*) FROM oauth_clients WHERE id = 'nobodys-yet'"), "clients bob never used are left alone")

	assert.ErrorIs(t, users.Delete(ctx, "u2"), ErrUserNotFound)
}

func TestExpenseRepository_History(t *testing.T) {
	ctx := context.Background()
	repo, _ := newTestExpenseRepo(t)
	e := newExpense("e1", "2026-11-07T13:20:00+01:00")
	require.NoError(t, repo.Create(ctx, e))
	e.Amount = "13.00"
	require.NoError(t, repo.Update(ctx, e))
	require.NoError(t, repo.Delete(ctx, testUser, "e1"))
	other := newExpense("e2", "2026-11-07T13:20:00+01:00")
	other.UserId = "someone-else"
	require.NoError(t, repo.Create(ctx, other))

	logs, err := repo.History(ctx, testUser)
	require.NoError(t, err)
	require.Len(t, logs, 3, "the deleted expense's history is kept; others' isn't included")
	assert.Equal(t, []models.LogAction{models.LogActionCreate, models.LogActionUpdate, models.LogActionDelete}, []models.LogAction{logs[0].Action, logs[1].Action, logs[2].Action})
	assert.Nil(t, logs[0].Before)
	assert.JSONEq(t, string(logs[1].After), string(logs[2].Before))
	assert.Contains(t, string(logs[1].After), `"13.00"`)
	assert.Nil(t, logs[2].After)
	assert.Equal(t, "e1", logs[0].ExpenseId)

	empty, err := repo.History(ctx, "nobody")
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NotNil(t, empty, "an empty list, not null, in exports")
}

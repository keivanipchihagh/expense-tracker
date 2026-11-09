package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestOAuthRepo(t *testing.T) OAuthRepository {
	t.Helper()
	repo, err := NewOAuthRepository(context.Background(), newTestClient(t))
	require.NoError(t, err)
	return repo
}

var oauthNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func TestNewOAuthRepository_Idempotent(t *testing.T) {
	client := newTestClient(t)
	_, err := NewOAuthRepository(context.Background(), client)
	require.NoError(t, err)
	_, err = NewOAuthRepository(context.Background(), client)
	require.NoError(t, err)
}

func TestOAuthRepository_Clients(t *testing.T) {
	repo := newTestOAuthRepo(t)
	ctx := context.Background()
	secret := "hash"

	clients := []models.OAuthClient{
		{Id: "public", Name: "Claude", RedirectUris: []string{"https://claude.ai/cb", "http://localhost:3000/cb"}, AuthMethod: models.AuthMethodNone, CreatedAt: oauthNow},
		{Id: "confidential", SecretHash: &secret, Name: "ChatGPT", RedirectUris: []string{"https://chatgpt.com/cb"}, AuthMethod: models.AuthMethodClientSecretPost, CreatedAt: oauthNow},
	}
	for _, c := range clients {
		require.NoError(t, repo.CreateClient(ctx, c))
	}
	for _, want := range clients {
		got, err := repo.GetClient(ctx, want.Id)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	_, err := repo.GetClient(ctx, "missing")
	assert.ErrorIs(t, err, ErrOAuthNotFound)
}

func TestOAuthRepository_ConsumeCodeOnce(t *testing.T) {
	repo := newTestOAuthRepo(t)
	ctx := context.Background()
	code := models.OAuthCode{CodeHash: "h", ClientId: "c", UserId: "user-1", RedirectUri: "https://a/cb", CodeChallenge: "ch", ExpiresAt: oauthNow}

	require.NoError(t, repo.CreateCode(ctx, code))

	got, err := repo.ConsumeCode(ctx, "h")
	require.NoError(t, err)
	assert.Equal(t, code, got)

	_, err = repo.ConsumeCode(ctx, "h")
	assert.ErrorIs(t, err, ErrOAuthNotFound, "a code can be exchanged only once")
}

func TestOAuthRepository_Tokens(t *testing.T) {
	repo := newTestOAuthRepo(t)
	ctx := context.Background()
	access := models.OAuthToken{TokenHash: "a", Kind: models.OAuthTokenAccess, ClientId: "c", UserId: "user-1", ExpiresAt: oauthNow.Add(time.Hour), CreatedAt: oauthNow}
	refresh := models.OAuthToken{TokenHash: "r", Kind: models.OAuthTokenRefresh, ClientId: "c", UserId: "user-1", ExpiresAt: oauthNow.Add(time.Hour), CreatedAt: oauthNow}

	require.NoError(t, repo.CreateTokens(ctx, access, refresh))

	got, err := repo.GetToken(ctx, "a", models.OAuthTokenAccess)
	require.NoError(t, err)
	assert.Equal(t, access, got)

	_, err = repo.GetToken(ctx, "r", models.OAuthTokenAccess)
	assert.ErrorIs(t, err, ErrOAuthNotFound, "a refresh token is not an access token")

	_, err = repo.ConsumeToken(ctx, "a", models.OAuthTokenRefresh)
	assert.ErrorIs(t, err, ErrOAuthNotFound, "an access token can't be used as a refresh token")

	got, err = repo.ConsumeToken(ctx, "r", models.OAuthTokenRefresh)
	require.NoError(t, err)
	assert.Equal(t, refresh, got)

	_, err = repo.ConsumeToken(ctx, "r", models.OAuthTokenRefresh)
	assert.ErrorIs(t, err, ErrOAuthNotFound, "a refresh token can be used only once")
}

func TestOAuthRepository_CreateTokensIsAtomic(t *testing.T) {
	repo := newTestOAuthRepo(t)
	ctx := context.Background()
	tok := models.OAuthToken{TokenHash: "dup", Kind: models.OAuthTokenAccess, ClientId: "c", ExpiresAt: oauthNow, CreatedAt: oauthNow}
	other := models.OAuthToken{TokenHash: "other", Kind: models.OAuthTokenRefresh, ClientId: "c", ExpiresAt: oauthNow, CreatedAt: oauthNow}

	require.NoError(t, repo.CreateTokens(ctx, tok))
	assert.Error(t, repo.CreateTokens(ctx, other, tok), "duplicate hash fails")

	_, err := repo.GetToken(ctx, "other", models.OAuthTokenRefresh)
	assert.ErrorIs(t, err, ErrOAuthNotFound, "the failed batch stored nothing")
}

func TestOAuthRepository_ConnectedApps(t *testing.T) {
	repo := newTestOAuthRepo(t)
	ctx := context.Background()

	for _, id := range []string{"claude", "chatgpt", "idle", "expired"} {
		require.NoError(t, repo.CreateClient(ctx, models.OAuthClient{Id: id, Name: id, RedirectUris: []string{"https://" + id + "/cb"}, AuthMethod: models.AuthMethodNone, CreatedAt: oauthNow}))
	}
	tok := func(hash, client, user string, created, expires time.Time) models.OAuthToken {
		return models.OAuthToken{TokenHash: hash, Kind: models.OAuthTokenRefresh, ClientId: client, UserId: user, CreatedAt: created, ExpiresAt: expires}
	}
	require.NoError(t, repo.CreateTokens(ctx,
		tok("c1", "claude", "me", oauthNow.Add(-2*time.Hour), oauthNow.Add(time.Hour)),
		tok("c2", "claude", "me", oauthNow.Add(-time.Hour), oauthNow.Add(time.Hour)),
		tok("g1", "chatgpt", "me", oauthNow.Add(-30*time.Minute), oauthNow.Add(time.Hour)),
		tok("e1", "expired", "me", oauthNow.Add(-48*time.Hour), oauthNow.Add(-time.Hour)),
		tok("o1", "claude", "other", oauthNow.Add(-time.Minute), oauthNow.Add(time.Hour)),
		tok("o2", "idle", "other", oauthNow.Add(-time.Minute), oauthNow.Add(time.Hour)),
	))

	apps, err := repo.ListConnectedApps(ctx, "me", oauthNow)
	require.NoError(t, err)
	require.Len(t, apps, 2, "only my clients with a valid token")
	assert.Equal(t, "chatgpt", apps[0].Client.Id, "most recently active first")
	assert.Equal(t, "claude", apps[1].Client.Id)
	assert.Equal(t, oauthNow.Add(-time.Hour), apps[1].LastActiveAt, "my last activity, not the other user's")
	assert.Equal(t, []string{"https://claude/cb"}, apps[1].Client.RedirectUris)

	// Revoking removes my codes and tokens only; the client stays while
	// someone else still uses it.
	require.NoError(t, repo.CreateCode(ctx, models.OAuthCode{CodeHash: "code", ClientId: "claude", UserId: "me", ExpiresAt: oauthNow.Add(time.Minute)}))
	require.NoError(t, repo.RevokeClient(ctx, "me", "claude"))

	_, err = repo.GetToken(ctx, "c2", models.OAuthTokenRefresh)
	assert.ErrorIs(t, err, ErrOAuthNotFound, "my tokens are gone")
	_, err = repo.ConsumeCode(ctx, "code")
	assert.ErrorIs(t, err, ErrOAuthNotFound, "my codes are gone")
	_, err = repo.GetToken(ctx, "o1", models.OAuthTokenRefresh)
	assert.NoError(t, err, "the other user's token survives")
	_, err = repo.GetClient(ctx, "claude")
	assert.NoError(t, err, "the client stays while it's in use")

	assert.ErrorIs(t, repo.RevokeClient(ctx, "me", "claude"), ErrOAuthNotFound, "nothing left to revoke")
	assert.ErrorIs(t, repo.RevokeClient(ctx, "me", "idle"), ErrOAuthNotFound, "can't revoke another user's app")

	require.NoError(t, repo.RevokeClient(ctx, "other", "claude"))
	_, err = repo.GetClient(ctx, "claude")
	assert.ErrorIs(t, err, ErrOAuthNotFound, "the last revoke removes the client")
}

func TestOAuthRepository_DeleteExpiredRemovesUnusedClients(t *testing.T) {
	repo := newTestOAuthRepo(t)
	ctx := context.Background()

	old := oauthNow.Add(-unusedClientTTL - time.Minute)
	for _, c := range []models.OAuthClient{
		{Id: "old-unused", CreatedAt: old},
		{Id: "old-with-token", CreatedAt: old},
		{Id: "new-unused", CreatedAt: oauthNow},
	} {
		c.Name, c.RedirectUris, c.AuthMethod = c.Id, []string{"https://a/cb"}, models.AuthMethodNone
		require.NoError(t, repo.CreateClient(ctx, c))
	}
	require.NoError(t, repo.CreateTokens(ctx, models.OAuthToken{TokenHash: "t", Kind: models.OAuthTokenRefresh, ClientId: "old-with-token", CreatedAt: old, ExpiresAt: oauthNow.Add(time.Hour)}))

	require.NoError(t, repo.DeleteExpired(ctx, oauthNow))

	_, err := repo.GetClient(ctx, "old-unused")
	assert.ErrorIs(t, err, ErrOAuthNotFound)
	_, err = repo.GetClient(ctx, "old-with-token")
	assert.NoError(t, err)
	_, err = repo.GetClient(ctx, "new-unused")
	assert.NoError(t, err, "a client gets a day to finish signing in")
}

func TestOAuthRepository_DeleteExpired(t *testing.T) {
	repo := newTestOAuthRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.CreateCode(ctx, models.OAuthCode{CodeHash: "old", ExpiresAt: oauthNow.Add(-time.Second)}))
	require.NoError(t, repo.CreateCode(ctx, models.OAuthCode{CodeHash: "new", ExpiresAt: oauthNow.Add(time.Minute)}))
	require.NoError(t, repo.CreateTokens(ctx,
		models.OAuthToken{TokenHash: "old", Kind: models.OAuthTokenAccess, ExpiresAt: oauthNow.Add(-time.Second), CreatedAt: oauthNow},
		models.OAuthToken{TokenHash: "new", Kind: models.OAuthTokenAccess, ExpiresAt: oauthNow.Add(time.Minute), CreatedAt: oauthNow},
	))

	require.NoError(t, repo.DeleteExpired(ctx, oauthNow))

	_, err := repo.ConsumeCode(ctx, "old")
	assert.ErrorIs(t, err, ErrOAuthNotFound)
	_, err = repo.ConsumeCode(ctx, "new")
	assert.NoError(t, err)
	_, err = repo.GetToken(ctx, "old", models.OAuthTokenAccess)
	assert.ErrorIs(t, err, ErrOAuthNotFound)
	_, err = repo.GetToken(ctx, "new", models.OAuthTokenAccess)
	assert.NoError(t, err)
}

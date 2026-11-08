package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionRepository(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	repo, err := NewSessionRepository(ctx, client)
	require.NoError(t, err)
	_, err = NewSessionRepository(ctx, client)
	require.NoError(t, err, "creating the table twice is fine")

	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s := models.Session{TokenHash: "h", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	require.NoError(t, repo.Create(ctx, s))

	got, err := repo.Get(ctx, "h")
	require.NoError(t, err)
	assert.Equal(t, s, got)

	require.NoError(t, repo.Delete(ctx, "h"))
	_, err = repo.Get(ctx, "h")
	assert.ErrorIs(t, err, ErrSessionNotFound)
	assert.NoError(t, repo.Delete(ctx, "h"), "deleting twice is fine")
}

func TestSessionRepository_DeleteExpired(t *testing.T) {
	ctx := context.Background()
	repo, err := NewSessionRepository(ctx, newTestClient(t))
	require.NoError(t, err)

	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	require.NoError(t, repo.Create(ctx, models.Session{TokenHash: "old", ExpiresAt: now.Add(-time.Second), CreatedAt: now}))
	require.NoError(t, repo.Create(ctx, models.Session{TokenHash: "new", ExpiresAt: now.Add(time.Hour), CreatedAt: now}))

	require.NoError(t, repo.DeleteExpired(ctx, now))

	_, err = repo.Get(ctx, "old")
	assert.ErrorIs(t, err, ErrSessionNotFound)
	_, err = repo.Get(ctx, "new")
	assert.NoError(t, err)
}

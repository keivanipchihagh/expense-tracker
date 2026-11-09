package repositories

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingRepository(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	repo, err := NewSettingRepository(ctx, client)
	require.NoError(t, err)
	_, err = NewSettingRepository(ctx, client)
	require.NoError(t, err, "creating the table twice is fine")

	_, err = repo.Get(ctx, "currency")
	assert.ErrorIs(t, err, ErrSettingNotFound, "never saved")

	require.NoError(t, repo.Set(ctx, "currency", "USD"))
	got, err := repo.Get(ctx, "currency")
	require.NoError(t, err)
	assert.Equal(t, "USD", got)

	require.NoError(t, repo.Set(ctx, "currency", "GBP"))
	got, err = repo.Get(ctx, "currency")
	require.NoError(t, err)
	assert.Equal(t, "GBP", got, "setting again replaces the value")

	var n int
	require.NoError(t, client.QueryRowContext(ctx, "SELECT COUNT(*) FROM settings").Scan(&n))
	assert.Equal(t, 1, n, "one row per key")

	_, err = repo.Get(ctx, "other")
	assert.ErrorIs(t, err, ErrSettingNotFound, "keys are independent")
}

func TestSettingRepository_ClosedDatabase(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	repo, err := NewSettingRepository(ctx, client)
	require.NoError(t, err)
	require.NoError(t, client.Close())

	_, err = repo.Get(ctx, "currency")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrSettingNotFound)
	assert.Error(t, repo.Set(ctx, "currency", "EUR"))
}

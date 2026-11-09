package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func names(list []models.CategoryUsage) []models.Category {
	out := make([]models.Category, len(list))
	for i, c := range list {
		out[i] = c.Name
	}
	return out
}

func TestCategoryRepository(t *testing.T) {
	ctx := context.Background()
	expenses, client := newTestExpenseRepo(t)
	users, err := NewUserRepository(ctx, client)
	require.NoError(t, err)
	repo, err := NewCategoryRepository(ctx, client)
	require.NoError(t, err)
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

	require.NoError(t, users.Create(ctx, newUser(testUser, "alice@example.com")))
	require.NoError(t, repo.Seed(ctx, testUser, []models.Category{"food", "rent"}, at))
	list, err := repo.List(ctx, testUser)
	require.NoError(t, err)
	assert.Equal(t, []models.CategoryUsage{{Name: "food"}, {Name: "rent"}}, list)

	// Seeding again does nothing.
	require.NoError(t, repo.Seed(ctx, testUser, models.DefaultCategories, at))
	list, _ = repo.List(ctx, testUser)
	assert.Len(t, list, 2)

	// New ones go last; duplicates are refused.
	require.NoError(t, repo.Add(ctx, testUser, "pets", at))
	assert.ErrorIs(t, repo.Add(ctx, testUser, "pets", at), ErrCategoryExists)
	require.NoError(t, repo.Add(ctx, "someone-else", "pets", at), "per user")

	// Counts follow the expenses.
	e := newExpense("e1", "2026-11-07T13:20:00+01:00")
	e.Category = "pets"
	require.NoError(t, expenses.Create(ctx, e))
	list, _ = repo.List(ctx, testUser)
	assert.Equal(t, []models.CategoryUsage{{Name: "food"}, {Name: "rent"}, {Name: "pets", Expenses: 1}}, list)

	assert.ErrorIs(t, repo.Remove(ctx, testUser, "pets"), ErrCategoryInUse)
	assert.ErrorIs(t, repo.Remove(ctx, testUser, "nope"), ErrCategoryNotFound)
	require.NoError(t, repo.Remove(ctx, testUser, "rent"))
	require.NoError(t, expenses.Delete(ctx, testUser, "e1"))
	require.NoError(t, repo.Remove(ctx, testUser, "pets"), "free once the expense is gone")
	list, _ = repo.List(ctx, testUser)
	assert.Equal(t, []models.Category{"food"}, names(list))
	other, _ := repo.List(ctx, "someone-else")
	assert.Equal(t, []models.Category{"pets"}, names(other), "other users keep theirs")
}

func TestCategoryRepository_SeedsExistingUsers(t *testing.T) {
	ctx := context.Background()
	expenses, client := newTestExpenseRepo(t)
	users, err := NewUserRepository(ctx, client)
	require.NoError(t, err)
	require.NoError(t, users.Create(ctx, newUser(testUser, "alice@example.com")))
	// An expense in a category that's no longer a default (from an older
	// version, say) keeps it.
	e := newExpense("e1", "2026-11-07T13:20:00+01:00")
	e.Category = "rent"
	require.NoError(t, expenses.Create(ctx, e))

	repo, err := NewCategoryRepository(ctx, client)
	require.NoError(t, err)
	list, err := repo.List(ctx, testUser)
	require.NoError(t, err)
	assert.Equal(t, append(append([]models.Category{}, models.DefaultCategories...), "rent"), names(list))

	_, err = NewCategoryRepository(ctx, client)
	require.NoError(t, err)
	list, _ = repo.List(ctx, testUser)
	assert.Len(t, list, len(models.DefaultCategories)+1, "starting again adds nothing")
}

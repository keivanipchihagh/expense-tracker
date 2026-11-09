package handler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// categorySuite is a handler whose category repository the test controls.
func categorySuite(t *testing.T, usage []models.CategoryUsage) (*Handler, *repositories.MockCategoryRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	categories := repositories.NewMockCategoryRepository(ctrl)
	categories.EXPECT().List(gomock.Any(), testUserId).Return(usage, nil).AnyTimes()
	h := NewHandler(repositories.NewMockExpenseRepository(ctrl), repositories.NewMockOAuthRepository(ctrl),
		repositories.NewMockSessionRepository(ctrl), repositories.NewMockUserRepository(ctrl), categories, testPublicUrl, true)
	h.now = func() time.Time { return testNow }
	return h, categories
}

func TestNormalizeCategory(t *testing.T) {
	for in, want := range map[string]models.Category{
		"Pets": "pets", "  Kids'   Stuff ": "kids' stuff", "Bars & Clubs": "bars & clubs", "Café": "café", "2nd home": "2nd home", "e.on": "e.on",
	} {
		got, problem := NormalizeCategory(in)
		assert.Empty(t, problem, in)
		assert.Equal(t, want, got, in)
	}
	for in, want := range map[string]string{
		"   ":                   "is required",
		strings.Repeat("a", 31): "must be at most 30 characters",
		"<script>":              "may only contain letters, digits, spaces and & ' - ., starting with a letter or digit",
		"-dash":                 "may only contain",
		"tab\there":             "", // tabs are just whitespace
		"new\nline":             "",
		"emoji 🍕":               "may only contain",
	} {
		_, problem := NormalizeCategory(in)
		if want == "" {
			assert.Empty(t, problem, in)
		} else {
			assert.Contains(t, problem, want, in)
		}
	}
}

func TestAddCategory(t *testing.T) {
	t.Run("added in its normal form", func(t *testing.T) {
		h, categories := categorySuite(t, defaultUsage())
		categories.EXPECT().Add(gomock.Any(), testUserId, models.Category("kids' stuff"), testNow).Return(nil)
		c, err := h.AddCategory(userCtx, "  Kids'  Stuff ")
		require.NoError(t, err)
		assert.Equal(t, models.Category("kids' stuff"), c)
	})
	t.Run("duplicate", func(t *testing.T) {
		h, categories := categorySuite(t, defaultUsage())
		categories.EXPECT().Add(gomock.Any(), testUserId, models.Category("food"), testNow).Return(repositories.ErrCategoryExists)
		_, err := h.AddCategory(userCtx, "Food")
		assert.Equal(t, map[string]string{"name": "already exists"}, validationFields(t, err))
	})
	t.Run("bad name never reaches the database", func(t *testing.T) {
		h, _ := categorySuite(t, defaultUsage())
		_, err := h.AddCategory(userCtx, "<b>")
		assert.Contains(t, validationFields(t, err)["name"], "may only contain")
	})
	t.Run("too many", func(t *testing.T) {
		many := make([]models.CategoryUsage, models.MaxCategories)
		h, _ := categorySuite(t, many)
		_, err := h.AddCategory(userCtx, "one more")
		assert.Contains(t, validationFields(t, err)["name"], "most categories allowed (50)")
	})
	t.Run("signed out", func(t *testing.T) {
		h, _ := categorySuite(t, nil)
		_, err := h.AddCategory(context.Background(), "pets")
		assert.Same(t, pkg.ErrNotSignedIn, err)
	})
}

func TestRemoveCategory(t *testing.T) {
	usage := []models.CategoryUsage{{Name: "food", Expenses: 3}, {Name: "pets"}, {Name: "rent", Expenses: 1}}
	t.Run("unused", func(t *testing.T) {
		h, categories := categorySuite(t, usage)
		categories.EXPECT().Remove(gomock.Any(), testUserId, models.Category("pets")).Return(nil)
		assert.NoError(t, h.RemoveCategory(userCtx, " Pets "))
	})
	t.Run("in use", func(t *testing.T) {
		h, _ := categorySuite(t, usage)
		err := h.RemoveCategory(userCtx, "food")
		var appErr *pkg.Error
		require.True(t, errors.As(err, &appErr))
		assert.Equal(t, 409, appErr.Status)
		assert.Equal(t, `"food" can't be removed: 3 expenses use it. Move them to another category first.`, appErr.Message)
		assert.Contains(t, h.RemoveCategory(userCtx, "rent").Error(), "1 expense uses it")
	})
	t.Run("taken in between", func(t *testing.T) {
		h, categories := categorySuite(t, usage)
		categories.EXPECT().Remove(gomock.Any(), testUserId, models.Category("pets")).Return(repositories.ErrCategoryInUse)
		assert.Contains(t, h.RemoveCategory(userCtx, "pets").Error(), "can't be removed")
	})
	t.Run("unknown", func(t *testing.T) {
		h, _ := categorySuite(t, usage)
		assert.Same(t, ErrCategoryNotFound, h.RemoveCategory(userCtx, "travel"))
	})
	t.Run("the last one stays", func(t *testing.T) {
		h, _ := categorySuite(t, []models.CategoryUsage{{Name: "pets"}})
		assert.Same(t, ErrLastCategory, h.RemoveCategory(userCtx, "pets"))
	})
}

func TestCreateExpense_UsesTheUsersCategories(t *testing.T) {
	h, _ := categorySuite(t, []models.CategoryUsage{{Name: "pets"}, {Name: "kids' stuff"}})
	input := map[string]any{"amount": "5", "occurred_at": "2026-11-07T13:20:00+01:00", "category": "food"}
	_, err := h.CreateExpense(userCtx, input)
	assert.Equal(t, map[string]string{"category": "must be one of your categories: pets, kids' stuff"}, validationFields(t, err), "a default the user removed is refused")
}

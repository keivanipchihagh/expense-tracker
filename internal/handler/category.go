package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

var (
	ErrCategoryNotFound = &pkg.Error{Status: http.StatusNotFound, Code: "not_found", Message: "category not found"}
	// ErrLastCategory keeps at least one category, so expenses can be added.
	ErrLastCategory = &pkg.Error{Status: http.StatusConflict, Code: "last_category", Message: "You need at least one category."}
)

// Categories returns the signed-in user's categories, in their order, with
// how many expenses use each.
func (h *Handler) Categories(ctx context.Context) ([]models.CategoryUsage, error) {
	uid, err := userId(ctx)
	if err != nil {
		return nil, err
	}
	list, err := h.categories.List(ctx, uid)
	if err != nil {
		h.logger.ErrorContext(ctx, "list categories failed", "error", err)
		return nil, pkg.InternalError
	}
	return list, nil
}

// CategoryNames returns the signed-in user's category names, in order.
func (h *Handler) CategoryNames(ctx context.Context) ([]models.Category, error) {
	list, err := h.Categories(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]models.Category, len(list))
	for i, c := range list {
		names[i] = c.Name
	}
	return names, nil
}

// AddCategory adds a category for the signed-in user and returns its name
// as stored (trimmed, single-spaced, lowercase).
func (h *Handler) AddCategory(ctx context.Context, name string) (models.Category, error) {
	uid, err := userId(ctx)
	if err != nil {
		return "", err
	}
	c, problem := NormalizeCategory(name)
	if problem != "" {
		return "", pkg.NewValidationError(map[string]string{"name": problem})
	}
	existing, err := h.categories.List(ctx, uid)
	if err != nil {
		h.logger.ErrorContext(ctx, "list categories failed", "error", err)
		return "", pkg.InternalError
	}
	if len(existing) >= models.MaxCategories {
		return "", pkg.NewValidationError(map[string]string{"name": fmt.Sprintf("can't be added: you have the most categories allowed (%d)", models.MaxCategories)})
	}
	err = h.categories.Add(ctx, uid, c, h.now())
	switch {
	case errors.Is(err, repositories.ErrCategoryExists):
		return "", pkg.NewValidationError(map[string]string{"name": "already exists"})
	case err != nil:
		h.logger.ErrorContext(ctx, "add category failed", "error", err)
		return "", pkg.InternalError
	}
	h.logger.InfoContext(ctx, "category added", "user_id", uid)
	return c, nil
}

// RemoveCategory removes one of the signed-in user's categories. It's
// refused while any of their expenses uses it, and for the last one.
func (h *Handler) RemoveCategory(ctx context.Context, name string) error {
	uid, err := userId(ctx)
	if err != nil {
		return err
	}
	c, _ := NormalizeCategory(name)
	list, err := h.categories.List(ctx, uid)
	if err != nil {
		h.logger.ErrorContext(ctx, "list categories failed", "error", err)
		return pkg.InternalError
	}
	var found *models.CategoryUsage
	for i := range list {
		if list[i].Name == c {
			found = &list[i]
		}
	}
	switch {
	case found == nil:
		return ErrCategoryNotFound
	case found.Expenses > 0:
		return inUseError(c, found.Expenses)
	case len(list) == 1:
		return ErrLastCategory
	}
	err = h.categories.Remove(ctx, uid, c)
	switch {
	case errors.Is(err, repositories.ErrCategoryNotFound):
		return ErrCategoryNotFound
	case errors.Is(err, repositories.ErrCategoryInUse):
		// An expense took it since the check above.
		return inUseError(c, 1)
	case err != nil:
		h.logger.ErrorContext(ctx, "remove category failed", "error", err)
		return pkg.InternalError
	}
	h.logger.InfoContext(ctx, "category removed", "user_id", uid)
	return nil
}

func inUseError(c models.Category, n int) error {
	noun := "expenses use"
	if n == 1 {
		noun = "expense uses"
	}
	return &pkg.Error{Status: http.StatusConflict, Code: "category_in_use",
		Message: fmt.Sprintf("%q can't be removed: %d %s it. Move them to another category first.", string(c), n, noun)}
}

// NormalizeCategory trims, single-spaces and lowercases a category name. It
// returns a problem to show when the name isn't acceptable: 1 to 30
// letters, digits, spaces and & ' - . characters, starting with a letter or
// digit.
func NormalizeCategory(s string) (models.Category, string) {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	n := utf8.RuneCountInString(s)
	switch {
	case n == 0:
		return "", "is required"
	case n > models.MaxCategoryLength:
		return "", fmt.Sprintf("must be at most %d characters", models.MaxCategoryLength)
	}
	for i, r := range s {
		ok := unicode.IsLetter(r) || unicode.IsDigit(r)
		if i > 0 {
			ok = ok || strings.ContainsRune(" &'-.", r)
		}
		if !ok {
			return "", "may only contain letters, digits, spaces and & ' - ., starting with a letter or digit"
		}
	}
	return models.Category(s), ""
}

// seedCategories gives a new account the default categories. A failure is
// logged; the account still works and gets them at the next start.
func (h *Handler) seedCategories(ctx context.Context, userId string) {
	if err := h.categories.Seed(ctx, userId, models.DefaultCategories, h.now()); err != nil {
		h.logger.ErrorContext(ctx, "seed categories failed", "user_id", userId, "error", err)
	}
}

// categoryError explains a category the user doesn't have.
func categoryError(list []models.Category) string {
	return "must be one of your categories: " + joinValues(list)
}

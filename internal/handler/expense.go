package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/keivanipchihagh/expense-tracker/pkg/utils"
)

// requiredFields must be present and non-null on create and cannot be cleared.
var requiredFields = []string{"amount", "occurred_at", "category"}

// writableFields are the keys a client may send.
var writableFields = map[string]bool{
	"amount": true, "occurred_at": true, "category": true,
	"merchant": true, "note": true,
}

// CreateExpense validates input and stores a new expense. Input keys are
// the JSON field names; numbers are json.Number.
func (h *Handler) CreateExpense(ctx context.Context, input map[string]any) (models.Expense, error) {
	uid, err := userId(ctx)
	if err != nil {
		return models.Expense{}, err
	}
	errs := map[string]string{}
	for _, key := range requiredFields {
		if _, ok := input[key]; !ok {
			errs[key] = "is required"
		}
	}

	categories, err := h.CategoryNames(ctx)
	if err != nil {
		return models.Expense{}, err
	}
	now := time.Now().UTC()
	expense := models.Expense{Id: uuid.Must(uuid.NewV7()).String(), UserId: uid, CreatedAt: now, UpdatedAt: now}
	applyInput(&expense, input, categories, errs)
	if len(errs) > 0 {
		return models.Expense{}, pkg.NewValidationError(errs)
	}

	if err := h.expenseRepo.Create(ctx, expense); err != nil {
		h.logger.ErrorContext(ctx, "create expense failed", "error", err)
		return models.Expense{}, pkg.InternalError
	}

	h.logger.InfoContext(ctx, "expense created", "id", expense.Id)
	return expense, nil
}

// ListExpenses returns expenses matching the filter, oldest first. The
// merchant search is trimmed; an empty one doesn't filter.
func (h *Handler) ListExpenses(ctx context.Context, filter models.ExpenseFilter) ([]models.Expense, error) {
	uid, err := userId(ctx)
	if err != nil {
		return nil, err
	}
	errs := map[string]string{}
	if !filter.DateFrom.IsZero() && !filter.DateTo.IsZero() && filter.DateTo.Before(filter.DateFrom) {
		errs["date_to"] = "must not be before date_from"
	}
	if filter.Category != "" {
		categories, err := h.CategoryNames(ctx)
		if err != nil {
			return nil, err
		}
		c, problem := NormalizeCategory(string(filter.Category))
		if problem != "" || !models.HasCategory(categories, c) {
			errs["category"] = categoryError(categories)
		}
		filter.Category = c
	}
	filter.Merchant = strings.TrimSpace(filter.Merchant)
	if utf8.RuneCountInString(filter.Merchant) > models.MaxMerchantLength {
		errs["merchant"] = fmt.Sprintf("must be at most %d characters", models.MaxMerchantLength)
	}
	if len(errs) > 0 {
		return nil, pkg.NewValidationError(errs)
	}

	expenses, err := h.expenseRepo.List(ctx, uid, filter)
	if err != nil {
		h.logger.ErrorContext(ctx, "list expenses failed", "error", err)
		return nil, pkg.InternalError
	}
	return expenses, nil
}

// GetExpense returns the expense with the given id.
func (h *Handler) GetExpense(ctx context.Context, id string) (models.Expense, error) {
	uid, err := userId(ctx)
	if err != nil {
		return models.Expense{}, err
	}
	expense, err := h.expenseRepo.Get(ctx, uid, id)
	if err != nil {
		return models.Expense{}, h.repoError(ctx, "get expense failed", err)
	}
	return expense, nil
}

// UpdateExpense applies a JSON Merge Patch: a missing key leaves the field
// unchanged and null clears an optional field. A patch that changes nothing
// returns the expense without writing.
func (h *Handler) UpdateExpense(ctx context.Context, id string, patch map[string]any) (models.Expense, error) {
	uid, err := userId(ctx)
	if err != nil {
		return models.Expense{}, err
	}
	current, err := h.expenseRepo.Get(ctx, uid, id)
	if err != nil {
		return models.Expense{}, h.repoError(ctx, "update expense failed", err)
	}

	categories, err := h.CategoryNames(ctx)
	if err != nil {
		return models.Expense{}, err
	}
	errs := map[string]string{}
	updated := current
	applyInput(&updated, patch, categories, errs)
	if len(errs) > 0 {
		return models.Expense{}, pkg.NewValidationError(errs)
	}
	if sameContent(current, updated) {
		return current, nil
	}

	updated.UpdatedAt = time.Now().UTC()
	if err := h.expenseRepo.Update(ctx, updated); err != nil {
		return models.Expense{}, h.repoError(ctx, "update expense failed", err)
	}

	h.logger.InfoContext(ctx, "expense updated", "id", id)
	return updated, nil
}

// DeleteExpense permanently removes the expense with the given id.
func (h *Handler) DeleteExpense(ctx context.Context, id string) error {
	uid, err := userId(ctx)
	if err != nil {
		return err
	}
	if err := h.expenseRepo.Delete(ctx, uid, id); err != nil {
		return h.repoError(ctx, "delete expense failed", err)
	}

	h.logger.InfoContext(ctx, "expense deleted", "id", id)
	return nil
}

// repoError maps a repository error to a domain error. Unexpected errors are
// logged and hidden behind a generic internal error.
func (h *Handler) repoError(ctx context.Context, msg string, err error) error {
	if errors.Is(err, repositories.ErrExpenseNotFound) {
		return pkg.ErrExpenseNotFound
	}
	h.logger.ErrorContext(ctx, msg, "error", err)
	return pkg.InternalError
}

// applyInput validates the keys in input and writes them onto e. Problems
// are recorded in errs, keyed by field name. A missing key leaves the field
// as it is; null clears an optional field and is rejected for a required one.
// categories are the user's; a category must be one of them, compared
// without regard to case.
func applyInput(e *models.Expense, input map[string]any, categories []models.Category, errs map[string]string) {
	for key, v := range input {
		switch {
		case !writableFields[key]:
			errs[key] = "unknown field"
		case v == nil && slices.Contains(requiredFields, key):
			errs[key] = "is required"
		}
	}

	if v := input["amount"]; v != nil {
		amount, err := utils.ParseAmount(v)
		if err != nil {
			errs["amount"] = err.Error()
		} else {
			e.Amount = amount
		}
	}

	if v := input["occurred_at"]; v != nil {
		s, ok := v.(string)
		t, err := utils.ParseOffsetTime(s)
		if !ok || err != nil {
			errs["occurred_at"] = "must be an RFC 3339 timestamp with a UTC offset, e.g. 2026-11-07T13:20:00+01:00"
		} else {
			e.OccurredAt = t
		}
	}

	if v := input["category"]; v != nil {
		s, _ := v.(string)
		if c, problem := NormalizeCategory(s); problem == "" && models.HasCategory(categories, c) {
			e.Category = c
		} else {
			errs["category"] = categoryError(categories)
		}
	}

	applyText(input, "merchant", models.MaxMerchantLength, &e.Merchant, errs)
	applyText(input, "note", models.MaxNoteLength, &e.Note, errs)
}

// applyText sets an optional text field: null or a blank string clears it.
func applyText(input map[string]any, key string, maxLength int, dst **string, errs map[string]string) {
	v, ok := input[key]
	if !ok {
		return
	}
	if v == nil {
		*dst = nil
		return
	}

	s, isString := v.(string)
	if !isString {
		errs[key] = "must be a string"
		return
	}
	s = strings.TrimSpace(s)
	if s == "" {
		*dst = nil
		return
	}
	if utf8.RuneCountInString(s) > maxLength {
		errs[key] = fmt.Sprintf("must be at most %d characters", maxLength)
		return
	}
	*dst = &s
}

func joinValues[T ~string](values []T) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}

// sameContent reports whether two expenses have the same field values,
// ignoring updated_at. Comparing the JSON form treats equal timestamps in the
// same offset as equal regardless of their internal representation.
func sameContent(a, b models.Expense) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

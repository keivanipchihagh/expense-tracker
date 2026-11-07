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
var requiredFields = []string{"amount", "currency", "occurred_at", "category"}

// writableFields are the keys a client may send.
var writableFields = map[string]bool{
	"amount": true, "currency": true, "occurred_at": true, "category": true,
	"merchant": true, "note": true,
}

// CreateExpense validates input and stores a new expense. Input keys are
// the JSON field names; numbers are json.Number.
func (h *Handler) CreateExpense(ctx context.Context, input map[string]any) (models.Expense, error) {
	errs := map[string]string{}
	for _, key := range requiredFields {
		if _, ok := input[key]; !ok {
			errs[key] = "is required"
		}
	}

	now := time.Now().UTC()
	expense := models.Expense{Id: uuid.Must(uuid.NewV7()).String(), CreatedAt: now, UpdatedAt: now}
	applyInput(&expense, input, errs)
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

// ListExpenses returns expenses whose local date is within [dateFrom,
// dateTo], oldest first. A zero bound is open.
func (h *Handler) ListExpenses(ctx context.Context, dateFrom, dateTo time.Time) ([]models.Expense, error) {
	if !dateFrom.IsZero() && !dateTo.IsZero() && dateTo.Before(dateFrom) {
		return nil, pkg.NewValidationError(map[string]string{"date_to": "must not be before date_from"})
	}

	expenses, err := h.expenseRepo.List(ctx, dateFrom, dateTo)
	if err != nil {
		h.logger.ErrorContext(ctx, "list expenses failed", "error", err)
		return nil, pkg.InternalError
	}
	return expenses, nil
}

// GetExpense returns the expense with the given id.
func (h *Handler) GetExpense(ctx context.Context, id string) (models.Expense, error) {
	expense, err := h.expenseRepo.Get(ctx, id)
	if err != nil {
		return models.Expense{}, h.repoError(ctx, "get expense failed", err)
	}
	return expense, nil
}

// UpdateExpense applies a JSON Merge Patch: a missing key leaves the field
// unchanged and null clears an optional field. A patch that changes nothing
// returns the expense without writing.
func (h *Handler) UpdateExpense(ctx context.Context, id string, patch map[string]any) (models.Expense, error) {
	current, err := h.expenseRepo.Get(ctx, id)
	if err != nil {
		return models.Expense{}, h.repoError(ctx, "update expense failed", err)
	}

	errs := map[string]string{}
	updated := current
	applyInput(&updated, patch, errs)
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
	if err := h.expenseRepo.Delete(ctx, id); err != nil {
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
func applyInput(e *models.Expense, input map[string]any, errs map[string]string) {
	for key, v := range input {
		switch {
		case !writableFields[key]:
			errs[key] = "unknown field"
		case v == nil && slices.Contains(requiredFields, key):
			errs[key] = "is required"
		}
	}

	previousCurrency := e.Currency
	if v := input["currency"]; v != nil {
		s, _ := v.(string)
		if c := models.Currency(s); c.IsValid() {
			e.Currency = c
		} else {
			errs["currency"] = "must be one of " + joinValues(models.Currencies)
		}
	}

	// The amount's allowed decimal places depend on the currency, so it is
	// checked after the currency and re-checked when only the currency changed.
	if _, bad := errs["currency"]; !bad {
		if v := input["amount"]; v != nil {
			setAmount(e, v, errs)
		} else if e.Amount != "" && e.Currency != previousCurrency {
			setAmount(e, e.Amount, errs)
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
		if c := models.Category(s); c.IsValid() {
			e.Category = c
		} else {
			errs["category"] = "must be one of " + joinValues(models.Categories)
		}
	}

	applyText(input, "merchant", models.MaxMerchantLength, &e.Merchant, errs)
	applyText(input, "note", models.MaxNoteLength, &e.Note, errs)
}

func setAmount(e *models.Expense, v any, errs map[string]string) {
	amount, err := utils.ParseAmount(v, string(e.Currency))
	if err != nil {
		errs["amount"] = err.Error()
		return
	}
	e.Amount = amount
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

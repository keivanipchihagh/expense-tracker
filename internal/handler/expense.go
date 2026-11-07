package handler

import (
	"context"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// CreateExpense logs the request and returns an empty expense; nothing is
// persisted yet.
func (h *Handler) CreateExpense(ctx context.Context, input map[string]any) (models.Expense, error) {
	h.logger.InfoContext(ctx, "expense created", "fields", len(input))
	return models.Expense{}, nil
}

// ListExpenses logs the request and returns no expenses. A zero dateFrom or
// dateTo means that side of the range is open.
func (h *Handler) ListExpenses(ctx context.Context, dateFrom, dateTo time.Time) ([]models.Expense, error) {
	h.logger.InfoContext(ctx, "expenses listed", "date_from", dateFrom, "date_to", dateTo)
	return []models.Expense{}, nil
}

// GetExpense logs the request and returns an empty expense.
func (h *Handler) GetExpense(ctx context.Context, id string) (models.Expense, error) {
	h.logger.InfoContext(ctx, "expense fetched", "id", id)
	return models.Expense{}, nil
}

// UpdateExpense logs the request and returns an empty expense. In patch, a
// key mapped to nil clears that field; a missing key leaves it unchanged.
func (h *Handler) UpdateExpense(ctx context.Context, id string, patch map[string]any) (models.Expense, error) {
	h.logger.InfoContext(ctx, "expense updated", "id", id, "fields", len(patch))
	return models.Expense{}, nil
}

// DeleteExpense logs the request.
func (h *Handler) DeleteExpense(ctx context.Context, id string) error {
	h.logger.InfoContext(ctx, "expense deleted", "id", id)
	return nil
}

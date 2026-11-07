package handler

import (
	"log/slog"

	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

type Handler struct {
	expenseRepo repositories.ExpenseRepository
	logger      *slog.Logger
}

func NewHandler(expenseRepo repositories.ExpenseRepository) *Handler {
	if expenseRepo == nil {
		panic("handler: expense repository must not be nil")
	}
	return &Handler{
		expenseRepo: expenseRepo,
		logger:      slog.Default().With("module", "handler"),
	}
}

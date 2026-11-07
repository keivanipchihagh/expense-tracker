package handler

import (
	"log/slog"

	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

type Handler struct {
	logRepo repositories.LogRepository
	logger  *slog.Logger
}

func NewHandler(logRepo repositories.LogRepository) *Handler {
	if logRepo == nil {
		panic("handler: log repository must not be nil")
	}
	return &Handler{
		logRepo: logRepo,
		logger:  slog.Default().With("module", "handler"),
	}
}

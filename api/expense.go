package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
)

// CreateExpense handles POST /api/v1/expenses.
func (s *Server) CreateExpense(w http.ResponseWriter, r *http.Request) {
	input, err := decodeJsonObject(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}

	expense, err := s.handler.CreateExpense(r.Context(), input)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusCreated, expense)
}

// ListExpenses handles GET /api/v1/expenses?date_from=YYYY-MM-DD&date_to=YYYY-MM-DD.
func (s *Server) ListExpenses(w http.ResponseWriter, r *http.Request) {
	fieldErrors := map[string]string{}
	dateFrom := parseDateParam(r, "date_from", fieldErrors)
	dateTo := parseDateParam(r, "date_to", fieldErrors)
	if len(fieldErrors) > 0 {
		writeError(w, http.StatusUnprocessableEntity, codeValidationFailed, "invalid query parameters", fieldErrors)
		return
	}

	expenses, err := s.handler.ListExpenses(r.Context(), dateFrom, dateTo)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusOK, expenses)
}

// GetExpense handles GET /api/v1/expenses/{id}.
func (s *Server) GetExpense(w http.ResponseWriter, r *http.Request) {
	expense, err := s.handler.GetExpense(r.Context(), r.PathValue("id"))
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusOK, expense)
}

// UpdateExpense handles PATCH /api/v1/expenses/{id} with a JSON Merge Patch body.
func (s *Server) UpdateExpense(w http.ResponseWriter, r *http.Request) {
	patch, err := decodeJsonObject(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}

	expense, err := s.handler.UpdateExpense(r.Context(), r.PathValue("id"), patch)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusOK, expense)
}

// DeleteExpense handles DELETE /api/v1/expenses/{id}.
func (s *Server) DeleteExpense(w http.ResponseWriter, r *http.Request) {
	if err := s.handler.DeleteExpense(r.Context(), r.PathValue("id")); err != nil {
		writeHandlerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseDateParam parses an optional YYYY-MM-DD query parameter. A missing
// parameter returns the zero time; an invalid one is recorded in fieldErrors.
func parseDateParam(r *http.Request, name string, fieldErrors map[string]string) time.Time {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		fieldErrors[name] = "must be a date in YYYY-MM-DD format"
		return time.Time{}
	}
	return t
}

// writeHandlerError writes a domain error with its status and code. Any other
// error is logged and sent as a generic 500.
func writeHandlerError(w http.ResponseWriter, r *http.Request, err error) {
	if appErr, ok := errors.AsType[*pkg.Error](err); ok {
		writeError(w, appErr.Status, appErr.Code, appErr.Message, appErr.Fields)
		return
	}
	slog.ErrorContext(r.Context(), "unexpected handler error", "error", err)
	writeError(w, http.StatusInternalServerError, codeInternal, "internal server error", nil)
}

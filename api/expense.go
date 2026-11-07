package api

import (
	"log/slog"
	"maps"
	"net/http"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// dateLayout is the format of the date_from and date_to query parameters.
const dateLayout = "2006-01-02"

// CreateExpense handles POST /api/v1/expenses.
func (s *Server) CreateExpense(w http.ResponseWriter, r *http.Request) {
	input, err := decodeJsonObject(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}

	expense, err := s.handler.CreateExpense(r.Context(), input)
	if err != nil {
		writeInternalError(w, r, "create expense failed", err)
		return
	}
	writeJson(w, http.StatusCreated, expenseModelToJson(expense))
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
		writeInternalError(w, r, "list expenses failed", err)
		return
	}
	writeJson(w, http.StatusOK, expenseModelsToJson(expenses))
}

// GetExpense handles GET /api/v1/expenses/{id}.
func (s *Server) GetExpense(w http.ResponseWriter, r *http.Request) {
	expense, err := s.handler.GetExpense(r.Context(), r.PathValue("id"))
	if err != nil {
		writeInternalError(w, r, "get expense failed", err)
		return
	}
	writeJson(w, http.StatusOK, expenseModelToJson(expense))
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
		writeInternalError(w, r, "update expense failed", err)
		return
	}
	writeJson(w, http.StatusOK, expenseModelToJson(expense))
}

// DeleteExpense handles DELETE /api/v1/expenses/{id}.
func (s *Server) DeleteExpense(w http.ResponseWriter, r *http.Request) {
	if err := s.handler.DeleteExpense(r.Context(), r.PathValue("id")); err != nil {
		writeInternalError(w, r, "delete expense failed", err)
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
	t, err := time.Parse(dateLayout, raw)
	if err != nil {
		fieldErrors[name] = "must be a date in YYYY-MM-DD format"
		return time.Time{}
	}
	return t
}

// expenseModelToJson flattens the core and user-defined fields of an expense
// into one JSON object. Core fields win if a name collides.
func expenseModelToJson(e models.Expense) map[string]any {
	out := make(map[string]any, len(e.Fields)+7)
	maps.Copy(out, e.Fields)

	out["id"] = e.Id
	out["amount"] = e.Amount
	out["currency"] = e.Currency
	out["occurred_at"] = e.OccurredAt
	out["raw_input"] = e.RawInput
	out["created_at"] = e.CreatedAt
	out["updated_at"] = e.UpdatedAt
	return out
}

// expenseModelsToJson converts a list of expenses; an empty list encodes as [].
func expenseModelsToJson(es []models.Expense) []map[string]any {
	out := make([]map[string]any, 0, len(es))
	for _, e := range es {
		out = append(out, expenseModelToJson(e))
	}
	return out
}

// writeInternalError logs err and sends a generic 500 without leaking details.
func writeInternalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	slog.ErrorContext(r.Context(), msg, "error", err)
	writeError(w, http.StatusInternalServerError, codeInternal, "internal server error", nil)
}

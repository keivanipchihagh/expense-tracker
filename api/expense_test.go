package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgHttp "github.com/keivanipchihagh/expense-tracker/pkg/http"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

const validBody = `{"amount":"12.50","currency":"EUR","occurred_at":"2026-11-07T13:20:00+01:00","category":"food"}`

func sampleExpense() models.Expense {
	ts := time.Date(2026, 11, 7, 12, 21, 0, 0, time.UTC)
	return models.Expense{
		Id:         "e1",
		Amount:     "12.50",
		Currency:   models.CurrencyEUR,
		OccurredAt: time.Date(2026, 11, 7, 13, 20, 0, 0, time.FixedZone("", 3600)),
		Category:   models.CategoryFood,
		CreatedAt:  ts,
		UpdatedAt:  ts,
	}
}

func do(routes http.Handler, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorDetail {
	t.Helper()
	var resp errorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	return resp.Error
}

func TestCreateExpense_Endpoint(t *testing.T) {
	t.Run("created", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

		rec := do(routes, http.MethodPost, "/api/v1/expenses", validBody)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var got map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.NotEmpty(t, got["id"])
		assert.Equal(t, "12.50", got["amount"])
		assert.Equal(t, "2026-11-07T13:20:00+01:00", got["occurred_at"])
		assert.Nil(t, got["merchant"])
	})

	t.Run("validation error", func(t *testing.T) {
		routes, _ := newTestRoutes(t)

		rec := do(routes, http.MethodPost, "/api/v1/expenses", `{"amount":"12.50"}`)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		e := decodeError(t, rec)
		assert.Equal(t, "validation_failed", e.Code)
		assert.Equal(t, "is required", e.Fields["currency"])
	})

	t.Run("repository error is a generic 500", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("disk full"))

		rec := do(routes, http.MethodPost, "/api/v1/expenses", validBody)
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		e := decodeError(t, rec)
		assert.Equal(t, "internal", e.Code)
		assert.NotContains(t, rec.Body.String(), "disk full")
	})

	decodeCases := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ``},
		{name: "malformed json", body: `{"amount":`},
		{name: "array", body: `[{"amount":"1"}]`},
		{name: "null", body: `null`},
		{name: "trailing data", body: `{} {}`},
	}
	for _, tt := range decodeCases {
		t.Run(tt.name, func(t *testing.T) {
			routes, _ := newTestRoutes(t)

			rec := do(routes, http.MethodPost, "/api/v1/expenses", tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Equal(t, codeInvalidJson, decodeError(t, rec).Code)
		})
	}

	t.Run("body too large", func(t *testing.T) {
		routes, _ := newTestRoutes(t)
		limited := pkgHttp.MaxBodyBytesMiddleware(16)(routes)

		rec := do(limited, http.MethodPost, "/api/v1/expenses", `{"note":"`+strings.Repeat("x", 64)+`"}`)
		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.Equal(t, codeBodyTooLarge, decodeError(t, rec).Code)
	})
}

func TestListExpenses_Endpoint(t *testing.T) {
	t.Run("returns a json array", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
		repo.EXPECT().List(gomock.Any(), from, to).Return([]models.Expense{sampleExpense()}, nil)

		rec := do(routes, http.MethodGet, "/api/v1/expenses?date_from=2026-11-01&date_to=2026-11-30", "")
		require.Equal(t, http.StatusOK, rec.Code)

		var got []map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Len(t, got, 1)
		assert.Equal(t, "e1", got[0]["id"])
	})

	t.Run("empty list is [] not null", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().List(gomock.Any(), time.Time{}, time.Time{}).Return([]models.Expense{}, nil)

		rec := do(routes, http.MethodGet, "/api/v1/expenses", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "[]", strings.TrimSpace(rec.Body.String()))
	})

	t.Run("invalid date format", func(t *testing.T) {
		routes, _ := newTestRoutes(t)

		rec := do(routes, http.MethodGet, "/api/v1/expenses?date_from=yesterday&date_to=2026-13-01", "")
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		assert.Equal(t, map[string]string{
			"date_from": "must be a date in YYYY-MM-DD format",
			"date_to":   "must be a date in YYYY-MM-DD format",
		}, decodeError(t, rec).Fields)
	})

	t.Run("date_to before date_from", func(t *testing.T) {
		routes, _ := newTestRoutes(t)

		rec := do(routes, http.MethodGet, "/api/v1/expenses?date_from=2026-11-30&date_to=2026-11-01", "")
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		assert.Equal(t, "must not be before date_from", decodeError(t, rec).Fields["date_to"])
	})
}

func TestGetExpense_Endpoint(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().Get(gomock.Any(), "e1").Return(sampleExpense(), nil)

		rec := do(routes, http.MethodGet, "/api/v1/expenses/e1", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"id":"e1"`)
	})

	t.Run("not found", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().Get(gomock.Any(), "nope").Return(models.Expense{}, repositories.ErrExpenseNotFound)

		rec := do(routes, http.MethodGet, "/api/v1/expenses/nope", "")
		require.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "not_found", decodeError(t, rec).Code)
	})
}

func TestUpdateExpense_Endpoint(t *testing.T) {
	t.Run("updated", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().Get(gomock.Any(), "e1").Return(sampleExpense(), nil)
		repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

		rec := do(routes, http.MethodPatch, "/api/v1/expenses/e1", `{"note":"corrected"}`)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"note":"corrected"`)
	})

	t.Run("not found", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().Get(gomock.Any(), "nope").Return(models.Expense{}, repositories.ErrExpenseNotFound)

		rec := do(routes, http.MethodPatch, "/api/v1/expenses/nope", `{"note":"x"}`)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("malformed json", func(t *testing.T) {
		routes, _ := newTestRoutes(t)

		rec := do(routes, http.MethodPatch, "/api/v1/expenses/e1", `nope`)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestDeleteExpense_Endpoint(t *testing.T) {
	t.Run("deleted", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().Delete(gomock.Any(), "e1").Return(nil)

		rec := do(routes, http.MethodDelete, "/api/v1/expenses/e1", "")
		require.Equal(t, http.StatusNoContent, rec.Code)
		assert.Empty(t, rec.Body.String())
	})

	t.Run("not found", func(t *testing.T) {
		routes, repo := newTestRoutes(t)
		repo.EXPECT().Delete(gomock.Any(), "nope").Return(repositories.ErrExpenseNotFound)

		rec := do(routes, http.MethodDelete, "/api/v1/expenses/nope", "")
		require.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestExpenseRoutes_MethodNotAllowed(t *testing.T) {
	routes, _ := newTestRoutes(t)

	assert.Equal(t, http.StatusMethodNotAllowed, do(routes, http.MethodPut, "/api/v1/expenses/e1", `{}`).Code)
	assert.Equal(t, http.StatusMethodNotAllowed, do(routes, http.MethodDelete, "/api/v1/expenses", "").Code)
}

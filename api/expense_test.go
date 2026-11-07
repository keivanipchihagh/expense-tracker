package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pkgHttp "github.com/keivanipchihagh/expense-tracker/pkg/http"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpenseEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string            // error envelope code, if any
		wantFields map[string]string // error envelope fields, if any
		wantArray  bool              // response is a JSON array
		wantEmpty  bool              // response has no body
	}{
		// create
		{name: "create with object", method: http.MethodPost, path: "/api/v1/expenses", body: `{"amount":"12.50","currency":"EUR"}`, wantStatus: http.StatusCreated},
		{name: "create with empty object", method: http.MethodPost, path: "/api/v1/expenses", body: `{}`, wantStatus: http.StatusCreated},
		{name: "create with empty body", method: http.MethodPost, path: "/api/v1/expenses", body: ``, wantStatus: http.StatusBadRequest, wantCode: codeInvalidJson},
		{name: "create with malformed json", method: http.MethodPost, path: "/api/v1/expenses", body: `{"amount":`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidJson},
		{name: "create with array", method: http.MethodPost, path: "/api/v1/expenses", body: `[{"amount":"1"}]`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidJson},
		{name: "create with null", method: http.MethodPost, path: "/api/v1/expenses", body: `null`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidJson},
		{name: "create with trailing data", method: http.MethodPost, path: "/api/v1/expenses", body: `{} {}`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidJson},

		// list
		{name: "list without dates", method: http.MethodGet, path: "/api/v1/expenses", wantStatus: http.StatusOK, wantArray: true},
		{name: "list with dates", method: http.MethodGet, path: "/api/v1/expenses?date_from=2026-11-01&date_to=2026-11-30", wantStatus: http.StatusOK, wantArray: true},
		{
			name: "list with invalid dates", method: http.MethodGet, path: "/api/v1/expenses?date_from=yesterday&date_to=2026-13-01",
			wantStatus: http.StatusUnprocessableEntity, wantCode: codeValidationFailed,
			wantFields: map[string]string{
				"date_from": "must be a date in YYYY-MM-DD format",
				"date_to":   "must be a date in YYYY-MM-DD format",
			},
		},

		// get
		{name: "get by id", method: http.MethodGet, path: "/api/v1/expenses/e1", wantStatus: http.StatusOK},

		// update
		{name: "update with patch", method: http.MethodPatch, path: "/api/v1/expenses/e1", body: `{"merchant":null}`, wantStatus: http.StatusOK},
		{name: "update with malformed json", method: http.MethodPatch, path: "/api/v1/expenses/e1", body: `nope`, wantStatus: http.StatusBadRequest, wantCode: codeInvalidJson},

		// delete
		{name: "delete by id", method: http.MethodDelete, path: "/api/v1/expenses/e1", wantStatus: http.StatusNoContent, wantEmpty: true},

		// routing
		{name: "put is not allowed", method: http.MethodPut, path: "/api/v1/expenses/e1", body: `{}`, wantStatus: http.StatusMethodNotAllowed},
		{name: "delete on collection is not allowed", method: http.MethodDelete, path: "/api/v1/expenses", wantStatus: http.StatusMethodNotAllowed},
	}

	routes := newTestRoutes(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))

			require.Equal(t, tt.wantStatus, rec.Code, rec.Body.String())

			switch {
			case tt.wantEmpty:
				assert.Empty(t, rec.Body.String())
			case tt.wantCode != "":
				var resp errorResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.Equal(t, tt.wantCode, resp.Error.Code)
				assert.Equal(t, tt.wantFields, resp.Error.Fields)
			case tt.wantArray:
				var resp []map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.NotNil(t, resp)
			case tt.wantStatus < 300:
				var resp map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				assert.Contains(t, resp, "id")
			}
		})
	}
}

func TestCreateExpense_BodyTooLarge(t *testing.T) {
	routes := pkgHttp.MaxBodyBytesMiddleware(16)(newTestRoutes(t))

	rec := httptest.NewRecorder()
	body := `{"note":"` + strings.Repeat("x", 64) + `"}`
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/expenses", strings.NewReader(body)))

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	var resp errorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, codeBodyTooLarge, resp.Error.Code)
}

func TestExpenseModelToJson_FlattensFields(t *testing.T) {
	out := expenseModelToJson(models.Expense{
		Id:       "e1",
		Amount:   "12.50",
		Currency: "EUR",
		Fields:   map[string]any{"category": "food", "id": "ignored"},
	})

	assert.Equal(t, "e1", out["id"], "core fields win over user-defined fields")
	assert.Equal(t, "12.50", out["amount"])
	assert.Equal(t, "EUR", out["currency"])
	assert.Equal(t, "food", out["category"])
}

func TestExpenseModelsToJson_EmptyList(t *testing.T) {
	raw, err := json.Marshal(expenseModelsToJson(nil))
	require.NoError(t, err)
	assert.Equal(t, "[]", string(raw), "empty list must encode as [] not null")
}

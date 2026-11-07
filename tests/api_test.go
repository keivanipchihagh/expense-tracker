// Package tests runs the full HTTP stack (router, middlewares, handler,
// repositories) against a real SQLite database.
package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/api"
	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	pkgHttp "github.com/keivanipchihagh/expense-tracker/pkg/http"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testServer struct {
	url    string
	client *sqlite.Client
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	ctx := context.Background()

	db, err := sqlite.NewClient(ctx, filepath.Join(t.TempDir(), "expense.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	logRepo, err := repositories.NewLogRepository(ctx, db)
	require.NoError(t, err)
	expenseRepo, err := repositories.NewExpenseRepository(ctx, db, logRepo)
	require.NoError(t, err)

	routes := api.NewServer(handler.NewHandler(expenseRepo), "test").Routes()
	srv := httptest.NewServer(pkgHttp.NewHttpServer(routes, 5*time.Second, 1<<20).Handler)
	t.Cleanup(srv.Close)

	return &testServer{url: srv.URL, client: db}
}

// call sends a request and decodes a JSON response body into out (if non-nil).
func (s *testServer) call(t *testing.T, method, path, body string, out any) int {
	t.Helper()
	req, err := http.NewRequest(method, s.url+path, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if out != nil && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, out), string(raw))
	}
	return resp.StatusCode
}

func (s *testServer) logActions(t *testing.T) []string {
	t.Helper()
	rows, err := s.client.Query("SELECT action FROM logs ORDER BY id")
	require.NoError(t, err)
	defer rows.Close()

	actions := []string{}
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		actions = append(actions, a)
	}
	return actions
}

func TestExpenseLifecycle(t *testing.T) {
	s := newTestServer(t)

	// Create
	var created map[string]any
	status := s.call(t, http.MethodPost, "/api/v1/expenses",
		`{"amount":12.5,"currency":"EUR","occurred_at":"2026-11-07T13:20:00+01:00","category":"food","merchant":"Rewe"}`,
		&created)
	require.Equal(t, http.StatusCreated, status)
	id := created["id"].(string)
	assert.Equal(t, "12.50", created["amount"], "a JSON number is stored exactly")

	// Get
	var fetched map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses/"+id, "", &fetched))
	assert.Equal(t, created, fetched, "get returns what create returned")

	// List
	var listed []map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses?date_from=2026-11-07&date_to=2026-11-07", "", &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, id, listed[0]["id"])

	// Update: change the amount, clear the merchant, add a note
	var updated map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodPatch, "/api/v1/expenses/"+id,
		`{"amount":"20","merchant":null,"note":"split with Alex"}`, &updated))
	assert.Equal(t, "20.00", updated["amount"])
	assert.Nil(t, updated["merchant"])
	assert.Equal(t, "split with Alex", updated["note"])
	assert.Equal(t, created["created_at"], updated["created_at"])
	assert.NotEqual(t, created["updated_at"], updated["updated_at"])

	// A patch that changes nothing writes no log
	require.Equal(t, http.StatusOK, s.call(t, http.MethodPatch, "/api/v1/expenses/"+id, `{"amount":"20.00"}`, nil))

	// Delete
	require.Equal(t, http.StatusNoContent, s.call(t, http.MethodDelete, "/api/v1/expenses/"+id, "", nil))
	require.Equal(t, http.StatusNotFound, s.call(t, http.MethodGet, "/api/v1/expenses/"+id, "", nil))
	require.Equal(t, http.StatusNotFound, s.call(t, http.MethodDelete, "/api/v1/expenses/"+id, "", nil))

	assert.Equal(t, []string{"create", "update", "delete"}, s.logActions(t))
}

func TestValidationErrorsAreReported(t *testing.T) {
	s := newTestServer(t)

	var resp struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	status := s.call(t, http.MethodPost, "/api/v1/expenses",
		`{"amount":"1.234","currency":"EUR","occurred_at":"2026-11-07T13:20:00","category":"rent","tip":1}`, &resp)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, "validation_failed", resp.Error.Code)
	assert.Equal(t, map[string]string{
		"amount":      "must have at most 2 decimal places for EUR",
		"occurred_at": "must be an RFC 3339 timestamp with a UTC offset, e.g. 2026-11-07T13:20:00+01:00",
		"category":    resp.Error.Fields["category"], // full list checked in handler tests
		"tip":         "unknown field",
	}, resp.Error.Fields)
	assert.Empty(t, s.logActions(t), "nothing is written")
}

func TestSecurityHeadersOnApiResponses(t *testing.T) {
	s := newTestServer(t)

	resp, err := http.Get(s.url + "/api/v1/expenses")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

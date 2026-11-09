package http

import (
	"context"
	"errors"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serve(t *testing.T, mw Middleware, h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	mw(h).ServeHTTP(rec, req)
	return rec
}

func TestMiddlewareChain_Order(t *testing.T) {
	var order []string
	tag := func(name string) Middleware {
		return func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+">")
				h.ServeHTTP(w, r)
				order = append(order, "<"+name)
			})
		}
	}

	chain := MiddlewareChain(tag("a"), tag("b"))
	serve(t, chain, func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, []string{"a>", "b>", "handler", "<b", "<a"}, order)
}

func TestRecoverMiddleware(t *testing.T) {
	rec := serve(t, RecoverMiddleware(), func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, internalErrorBody, rec.Body.String())
}

func TestLoggingMiddleware_PassesThroughStatus(t *testing.T) {
	rec := serve(t, LoggingMiddleware(), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusTeapot, rec.Code)
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	rec := serve(t, SecurityHeadersMiddleware(), func(w http.ResponseWriter, r *http.Request) {},
		httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestTimeoutMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		timeout      time.Duration
		wantDeadline bool
	}{
		{name: "sets a deadline", timeout: time.Second, wantDeadline: true},
		{name: "zero disables", timeout: 0, wantDeadline: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hasDeadline bool
			serve(t, TimeoutMiddleware(tt.timeout), func(w http.ResponseWriter, r *http.Request) {
				_, hasDeadline = r.Context().Deadline()
			}, httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Equal(t, tt.wantDeadline, hasDeadline)
		})
	}
}

func TestMaxBodyBytesMiddleware(t *testing.T) {
	tests := []struct {
		name    string
		limit   int64
		body    string
		wantErr bool
	}{
		{name: "under the limit", limit: 10, body: "12345"},
		{name: "exactly the limit", limit: 5, body: "12345"},
		{name: "over the limit", limit: 4, body: "12345", wantErr: true},
		{name: "zero disables", limit: 0, body: strings.Repeat("x", 1<<16)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var readErr error
			serve(t, MaxBodyBytesMiddleware(tt.limit), func(w http.ResponseWriter, r *http.Request) {
				_, readErr = io.ReadAll(r.Body)
			}, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tt.body)))

			if tt.wantErr {
				assert.Error(t, readErr)
				return
			}
			require.NoError(t, readErr)
		})
	}
}

func TestBearerAuthMiddleware(t *testing.T) {
	const metadata = "https://expenses.example.com/.well-known/oauth-protected-resource"
	validate := func(_ context.Context, token string) (string, bool, error) {
		switch token {
		case "good":
			return "user-1", true, nil
		case "broken":
			return "", false, errors.New("db down")
		}
		return "", false, nil
	}
	isPublic := func(path string) bool { return path == "/health" }
	mw := BearerAuthMiddleware(validate, isPublic, func(*http.Request) string { return metadata })

	tests := []struct {
		name          string
		path          string
		authorization string
		wantStatus    int
		wantChallenge string
	}{
		{name: "public path without token", path: "/health", wantStatus: http.StatusOK},
		{name: "valid token", path: "/api", authorization: "Bearer good", wantStatus: http.StatusOK},
		{name: "scheme is case-insensitive", path: "/api", authorization: "bearer good", wantStatus: http.StatusOK},
		{name: "missing header", path: "/api", wantStatus: http.StatusUnauthorized, wantChallenge: `Bearer resource_metadata="` + metadata + `"`},
		{name: "wrong scheme", path: "/api", authorization: "Basic Z29vZA==", wantStatus: http.StatusUnauthorized, wantChallenge: `Bearer resource_metadata="` + metadata + `"`},
		{name: "empty token", path: "/api", authorization: "Bearer ", wantStatus: http.StatusUnauthorized, wantChallenge: `Bearer resource_metadata="` + metadata + `"`},
		{name: "invalid token", path: "/api", authorization: "Bearer bad", wantStatus: http.StatusUnauthorized, wantChallenge: `Bearer resource_metadata="` + metadata + `", error="invalid_token"`},
		{name: "validator error", path: "/api", authorization: "Bearer broken", wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()
			var seenUser string
			mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seenUser = pkg.UserIdFrom(r.Context()) })).ServeHTTP(rec, req)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.authorization != "" && tt.wantStatus == http.StatusOK {
				assert.Equal(t, "user-1", seenUser, "the token's user is passed downstream")
			}
			assert.Equal(t, tt.wantChallenge, rec.Header().Get("WWW-Authenticate"))
			if tt.wantStatus == http.StatusUnauthorized {
				assert.JSONEq(t, unauthorizedBody, rec.Body.String())
			}
		})
	}
}

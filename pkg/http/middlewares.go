package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// internalErrorBody is the response body sent when a handler panics.
const internalErrorBody = `{"error":{"code":"internal","message":"internal server error"}}`

// unauthorizedBody is the response body sent for a missing or invalid token.
const unauthorizedBody = `{"error":{"code":"unauthorized","message":"missing or invalid access token"}}`

// TokenValidator reports whether token is a valid access token. An error
// means the check itself failed, not that the token is invalid.
type TokenValidator func(ctx context.Context, token string) (bool, error)

// BearerAuthMiddleware requires "Authorization: Bearer <token>" with a valid
// token on every request whose path isPublic doesn't exempt. A rejection is a
// 401 whose WWW-Authenticate header points at the resource metadata URL for
// the request (RFC 9728); that is how MCP clients find out where to log in.
func BearerAuthMiddleware(validate TokenValidator, isPublic func(path string) bool, resourceMetadataUrl func(r *http.Request) string) Middleware {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(r.URL.Path) {
				h.ServeHTTP(w, r)
				return
			}
			challenge := `Bearer resource_metadata="` + resourceMetadataUrl(r) + `"`

			token, ok := bearerToken(r)
			if !ok {
				writeUnauthorized(w, challenge)
				return
			}
			valid, err := validate(r.Context(), token)
			if err != nil {
				slog.ErrorContext(r.Context(), "access token check failed", "error", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(internalErrorBody))
				return
			}
			if !valid {
				writeUnauthorized(w, challenge+`, error="invalid_token"`)
				return
			}
			h.ServeHTTP(w, r)
		})
	}
}

// bearerToken extracts the token from "Authorization: Bearer <token>". The
// scheme is case-insensitive (RFC 7235).
func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func writeUnauthorized(w http.ResponseWriter, challenge string) {
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(unauthorizedBody))
}

// RecoverMiddleware turns a panic in a downstream handler into a logged
// error and a 500 response instead of a dropped connection.
func RecoverMiddleware() Middleware {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					slog.Error("panic in http handler", "panic", rec, "method", r.Method, "path", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(internalErrorBody))
				}
			}()
			h.ServeHTTP(w, r)
		})
	}
}

// LoggingMiddleware logs one line per request with method, path, status
// and duration. Query strings and bodies are never logged.
func LoggingMiddleware() Middleware {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			h.ServeHTTP(rec, r)

			slog.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// SecurityHeadersMiddleware sets baseline response headers for a JSON API.
// HSTS is left to the reverse proxy that terminates TLS.
func SecurityHeadersMiddleware() Middleware {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Cache-Control", "no-store")
			h.ServeHTTP(w, r)
		})
	}
}

// TimeoutMiddleware cancels the request context after d. Handlers and the
// database calls they make abort once it fires. Zero disables it.
func TimeoutMiddleware(d time.Duration) Middleware {
	return func(h http.Handler) http.Handler {
		if d <= 0 {
			return h
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MaxBodyBytesMiddleware caps the request body at n bytes; reading past
// the limit returns an error. Zero disables it.
func MaxBodyBytesMiddleware(n int64) Middleware {
	return func(h http.Handler) http.Handler {
		if n <= 0 {
			return h
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			h.ServeHTTP(w, r)
		})
	}
}

package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// internalErrorBody is the response body sent when a handler panics.
const internalErrorBody = `{"error":{"code":"internal","message":"internal server error"}}`

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

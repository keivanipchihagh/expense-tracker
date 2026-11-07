package http

import (
	"net/http"
	"slices"
)

// Middleware wraps an http.Handler with extra behaviour.
type Middleware func(http.Handler) http.Handler

// MiddlewareChain composes middlewares into one. The first middleware is
// the outermost: it sees the request first and the response last.
func MiddlewareChain(middlewares ...Middleware) Middleware {
	return func(h http.Handler) http.Handler {
		for _, middleware := range slices.Backward(middlewares) {
			h = middleware(h)
		}
		return h
	}
}

// statusRecorder captures the status code written by downstream handlers.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

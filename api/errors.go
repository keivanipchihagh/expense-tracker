package api

import (
	"net/http"
	"strings"
)

// errorPages replaces the router's plain-text 404 and 405 responses: JSON
// in the API's error format for machine paths, a small HTML page elsewhere.
// Responses written by handlers pass through untouched.
func errorPages(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&errorPageWriter{ResponseWriter: w, r: r}, r)
	})
}

type errorPageWriter struct {
	http.ResponseWriter
	r           *http.Request
	intercepted bool
}

func (e *errorPageWriter) WriteHeader(status int) {
	// The router's own errors come from http.Error, which writes text/plain.
	if (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) &&
		strings.HasPrefix(e.Header().Get("Content-Type"), "text/plain") {
		e.intercepted = true
		writeErrorPage(e.ResponseWriter, e.r, status)
		return
	}
	e.ResponseWriter.WriteHeader(status)
}

func (e *errorPageWriter) Write(b []byte) (int, error) {
	if e.intercepted {
		return len(b), nil // drop the plain-text body
	}
	return e.ResponseWriter.Write(b)
}

func (e *errorPageWriter) Flush() {
	if f, ok := e.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (e *errorPageWriter) Unwrap() http.ResponseWriter {
	return e.ResponseWriter
}

func writeErrorPage(w http.ResponseWriter, r *http.Request, status int) {
	code, message := "not_found", "not found"
	if status == http.StatusMethodNotAllowed {
		code, message = "method_not_allowed", "method not allowed"
	}

	if isMachinePath(r.URL.Path) {
		writeError(w, status, code, message, nil)
		return
	}

	title, text := "Page not found", "The page you're looking for doesn't exist or has moved."
	if status == http.StatusMethodNotAllowed {
		title, text = "Method not allowed", "This address doesn't accept that kind of request."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", uiCsp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = pages["error.html"].ExecuteTemplate(w, "error.html", pageData{Title: title, Error: text, Status: status})
}

// isMachinePath reports whether path is used by programs rather than people.
func isMachinePath(path string) bool {
	return path == "/health" || path == "/mcp" ||
		strings.HasPrefix(path, "/api/") ||
		strings.HasPrefix(path, "/oauth/") ||
		strings.HasPrefix(path, "/.well-known/")
}

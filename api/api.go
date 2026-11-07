package api

import (
	"net/http"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
)

// Server holds the dependencies of the HTTP API handlers.
type Server struct {
	handler *handler.Handler
	version string
}

// NewServer creates the API server. version is reported by /health.
func NewServer(handler *handler.Handler, version string) *Server {
	if handler == nil {
		panic("api: handler must not be nil")
	}
	if version == "" {
		panic("api: version must not be empty")
	}
	return &Server{handler: handler, version: version}
}

// Routes returns the handler with every API route registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.Health)

	mux.HandleFunc("POST /api/v1/expenses", s.CreateExpense)
	mux.HandleFunc("GET /api/v1/expenses", s.ListExpenses)
	mux.HandleFunc("GET /api/v1/expenses/{id}", s.GetExpense)
	mux.HandleFunc("PATCH /api/v1/expenses/{id}", s.UpdateExpense)
	mux.HandleFunc("DELETE /api/v1/expenses/{id}", s.DeleteExpense)

	return mux
}

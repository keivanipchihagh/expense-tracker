package api

import "net/http"

// Server holds the dependencies of the HTTP API handlers.
type Server struct {
	version string
}

// NewServer creates the API server. version is reported by /health.
func NewServer(version string) *Server {
	if version == "" {
		panic("api: version must not be empty")
	}
	return &Server{version: version}
}

// Routes returns the handler with every API route registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.Health)
	return mux
}

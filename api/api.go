package api

import (
	"net/http"
	"strings"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	pkgHttp "github.com/keivanipchihagh/expense-tracker/pkg/http"
)

// Server holds the dependencies of the HTTP API handlers.
type Server struct {
	handler   *handler.Handler
	version   string
	publicUrl string
}

// NewServer creates the API server. version is reported by /health;
// publicUrl is the base URL used in OAuth metadata.
func NewServer(handler *handler.Handler, version, publicUrl string) *Server {
	if handler == nil {
		panic("api: handler must not be nil")
	}
	if version == "" || publicUrl == "" {
		panic("api: version and public url must not be empty")
	}
	return &Server{handler: handler, version: version, publicUrl: strings.TrimSuffix(publicUrl, "/")}
}

// Handler returns every route behind bearer-token auth. Paths that
// IsPublicPath accepts are reachable without a token.
func (s *Server) Handler() http.Handler {
	auth := pkgHttp.BearerAuthMiddleware(
		s.handler.ValidateAccessToken,
		IsPublicPath,
		s.publicUrl+"/.well-known/oauth-protected-resource",
	)
	return auth(s.Routes())
}

// Routes returns the handler with every route registered and no auth.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.Health)

	// OAuth discovery. Clients may append the resource path to the
	// well-known URL, so any suffix serves the same document.
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.ProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/{path...}", s.ProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.AuthorizationServerMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/{path...}", s.AuthorizationServerMetadata)
	mux.HandleFunc("GET /.well-known/openid-configuration", s.AuthorizationServerMetadata)

	// OAuth endpoints
	mux.HandleFunc("POST /oauth/register", s.RegisterClient)
	mux.HandleFunc("GET /oauth/authorize", s.AuthorizePage)
	mux.HandleFunc("POST /oauth/authorize", s.AuthorizeSubmit)
	mux.HandleFunc("POST /oauth/token", s.Token)

	// Expenses
	mux.HandleFunc("POST /api/v1/expenses", s.CreateExpense)
	mux.HandleFunc("GET /api/v1/expenses", s.ListExpenses)
	mux.HandleFunc("GET /api/v1/expenses/{id}", s.GetExpense)
	mux.HandleFunc("PATCH /api/v1/expenses/{id}", s.UpdateExpense)
	mux.HandleFunc("DELETE /api/v1/expenses/{id}", s.DeleteExpense)

	return mux
}

// IsPublicPath reports whether path is reachable without an access token:
// the health check, OAuth discovery and the OAuth endpoints themselves.
func IsPublicPath(path string) bool {
	return path == "/health" ||
		strings.HasPrefix(path, "/.well-known/") ||
		strings.HasPrefix(path, "/oauth/")
}

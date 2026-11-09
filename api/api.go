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
	// secureCookies marks the session cookie Secure when served over HTTPS.
	secureCookies bool
}

// NewServer creates the API server. version is reported to MCP clients;
// publicUrl is the base URL used in OAuth metadata.
func NewServer(handler *handler.Handler, version, publicUrl string) *Server {
	if handler == nil {
		panic("api: handler must not be nil")
	}
	if version == "" || publicUrl == "" {
		panic("api: version and public url must not be empty")
	}
	return &Server{
		handler:       handler,
		version:       version,
		publicUrl:     strings.TrimSuffix(publicUrl, "/"),
		secureCookies: strings.HasPrefix(publicUrl, "https://"),
	}
}

// Handler returns every route with authentication applied: the JSON API and
// MCP require a bearer token, the UI requires a session cookie.
func (s *Server) Handler() http.Handler {
	auth := pkgHttp.BearerAuthMiddleware(s.handler.ValidateAccessToken, IsPublicPath, s.resourceMetadataUrl)
	return auth(s.Routes())
}

// resourceMetadataUrl is where a client that got a 401 learns how to log in.
// MCP clients get the metadata for the /mcp resource itself.
func (s *Server) resourceMetadataUrl(r *http.Request) string {
	if r.URL.Path == "/mcp" {
		return s.publicUrl + "/.well-known/oauth-protected-resource/mcp"
	}
	return s.publicUrl + "/.well-known/oauth-protected-resource"
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
	mux.HandleFunc("POST /api/v1/users", s.SignUp)
	mux.HandleFunc("GET /api/v1/me", s.GetMe)
	mux.HandleFunc("PATCH /api/v1/me", s.UpdateMe)
	mux.HandleFunc("PUT /api/v1/me/password", s.ChangeMyPassword)

	// MCP (Streamable HTTP)
	mux.Handle("/mcp", s.NewMcpHandler())

	// UI: pages, HTMX fragments and static assets
	mux.Handle("GET /static/", staticFiles())
	mux.HandleFunc("GET /login", s.LoginPage)
	mux.HandleFunc("POST /login", s.LoginSubmit)
	mux.HandleFunc("POST /logout", s.Logout)
	mux.HandleFunc("GET /{$}", s.requireSession(s.IndexPage))
	mux.HandleFunc("GET /ui/expenses", s.requireSession(s.ResultsPartial))
	mux.HandleFunc("GET /ui/expenses/new", s.requireSession(s.NewExpenseForm))
	mux.HandleFunc("POST /ui/expenses", s.requireSession(s.CreateExpenseSubmit))
	mux.HandleFunc("GET /ui/expenses/{id}/edit", s.requireSession(s.EditExpenseForm))
	mux.HandleFunc("POST /ui/expenses/{id}", s.requireSession(s.UpdateExpenseSubmit))
	mux.HandleFunc("POST /ui/expenses/{id}/delete", s.requireSession(s.DeleteExpenseSubmit))
	mux.HandleFunc("GET /signup", s.SignupPage)
	mux.HandleFunc("POST /signup", s.SignupSubmit)
	mux.HandleFunc("GET /settings", s.requireSession(s.SettingsPage))
	mux.HandleFunc("POST /settings", s.requireSession(s.SettingsSubmit))
	mux.HandleFunc("POST /settings/password", s.requireSession(s.PasswordSubmit))
	mux.HandleFunc("GET /account", s.AccountRedirect)
	mux.HandleFunc("GET /ui/export", s.requireSession(s.ExportExpenses))
	mux.HandleFunc("GET /analysis", s.requireSession(s.AnalysisPage))
	mux.HandleFunc("GET /apps", s.requireSession(s.AppsPage))
	mux.HandleFunc("POST /ui/apps/{id}/revoke", s.requireSession(s.RevokeAppSubmit))

	return errorPages(mux)
}

// IsPublicPath reports whether path is exempt from bearer-token auth. Only
// the JSON API and MCP need a token; the UI checks its session cookie
// itself, and health, OAuth discovery, the OAuth endpoints and sign-up
// (POST /api/v1/users, the only route on that path) are public.
func IsPublicPath(path string) bool {
	if path == "/api/v1/users" {
		return true
	}
	return !strings.HasPrefix(path, "/api/") && path != "/mcp"
}

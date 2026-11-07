package api

import (
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// ProtectedResourceMetadata handles /.well-known/oauth-protected-resource
// (RFC 9728): it tells clients which authorization server issues tokens.
func (s *Server) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJson(w, http.StatusOK, map[string]any{
		"resource":                 s.publicUrl,
		"authorization_servers":    []string{s.publicUrl},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "expense-tracker",
	})
}

// AuthorizationServerMetadata handles /.well-known/oauth-authorization-server
// (RFC 8414): it lists the OAuth endpoints and what they support.
func (s *Server) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	writeJson(w, http.StatusOK, map[string]any{
		"issuer":                                         s.publicUrl,
		"authorization_endpoint":                         s.publicUrl + "/oauth/authorize",
		"token_endpoint":                                 s.publicUrl + "/oauth/token",
		"registration_endpoint":                          s.publicUrl + "/oauth/register",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{models.AuthMethodNone, models.AuthMethodClientSecretPost, models.AuthMethodClientSecretBasic},
		"authorization_response_iss_parameter_supported": true,
	})
}

type registerRequest struct {
	RedirectUris            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// RegisterClient handles POST /oauth/register (RFC 7591). Unknown metadata
// fields are ignored, as the RFC allows.
func (s *Server) RegisterClient(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOAuthError(w, pkg.NewOAuthError(http.StatusBadRequest, "invalid_client_metadata", "request body must be a JSON object"))
		return
	}

	client, secret, err := s.handler.RegisterClient(r.Context(), handler.RegisterClientRequest{
		RedirectUris:            req.RedirectUris,
		ClientName:              req.ClientName,
		TokenEndpointAuthMethod: req.TokenEndpointAuthMethod,
	})
	if err != nil {
		writeOAuthError(w, err)
		return
	}

	resp := map[string]any{
		"client_id":                  client.Id,
		"client_id_issued_at":        client.CreatedAt.Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectUris,
		"token_endpoint_auth_method": client.AuthMethod,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0 // never
	}
	writeJson(w, http.StatusCreated, resp)
}

// AuthorizePage handles GET /oauth/authorize: it validates the request and
// shows the login page.
func (s *Server) AuthorizePage(w http.ResponseWriter, r *http.Request) {
	req := authorizeRequestFrom(r.URL.Query())

	client, err := s.handler.CheckAuthorizeRequest(r.Context(), req)
	if err != nil {
		s.writeAuthorizeError(w, r, req, err)
		return
	}
	renderLogin(w, http.StatusOK, loginPage{Client: client, Request: req})
}

// AuthorizeSubmit handles POST /oauth/authorize: the login form.
func (s *Server) AuthorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderError(w, http.StatusBadRequest, "The login form could not be read.")
		return
	}
	req := authorizeRequestFrom(r.PostForm)

	redirect, err := s.handler.Authorize(r.Context(), req, r.PostForm.Get("password"))
	switch {
	case errors.Is(err, handler.ErrWrongPassword):
		client, _ := s.handler.CheckAuthorizeRequest(r.Context(), req)
		renderLogin(w, http.StatusUnauthorized, loginPage{Client: client, Request: req, Error: "Wrong password."})
	case errors.Is(err, handler.ErrTooManyLogins):
		renderError(w, http.StatusTooManyRequests, "Too many failed logins. Try again in 15 minutes.")
	case err != nil:
		s.writeAuthorizeError(w, r, req, err)
	default:
		http.Redirect(w, r, redirect, http.StatusFound)
	}
}

// Token handles POST /oauth/token. Client credentials may come from HTTP
// Basic auth or the form.
func (s *Server) Token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, pkg.NewOAuthError(http.StatusBadRequest, "invalid_request", "request body must be form-encoded"))
		return
	}

	clientId, clientSecret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	if id, secret, ok := r.BasicAuth(); ok {
		clientId, clientSecret = unescapeBasic(id), unescapeBasic(secret)
	}

	resp, err := s.handler.Token(r.Context(), handler.TokenRequest{
		GrantType:    r.PostForm.Get("grant_type"),
		Code:         r.PostForm.Get("code"),
		RedirectUri:  r.PostForm.Get("redirect_uri"),
		CodeVerifier: r.PostForm.Get("code_verifier"),
		RefreshToken: r.PostForm.Get("refresh_token"),
		ClientId:     clientId,
		ClientSecret: clientSecret,
	})
	if err != nil {
		writeOAuthError(w, err)
		return
	}
	writeJson(w, http.StatusOK, resp)
}

func authorizeRequestFrom(v url.Values) handler.AuthorizeRequest {
	return handler.AuthorizeRequest{
		ResponseType:        v.Get("response_type"),
		ClientId:            v.Get("client_id"),
		RedirectUri:         v.Get("redirect_uri"),
		CodeChallenge:       v.Get("code_challenge"),
		CodeChallengeMethod: v.Get("code_challenge_method"),
		State:               v.Get("state"),
		Resource:            v.Get("resource"),
	}
}

// writeAuthorizeError redirects errors the client should receive and shows
// the rest, whose redirect URI can't be trusted, as a page.
func (s *Server) writeAuthorizeError(w http.ResponseWriter, r *http.Request, req handler.AuthorizeRequest, err error) {
	if authErr, ok := errors.AsType[*handler.AuthorizeError](err); ok {
		if authErr.Redirect {
			http.Redirect(w, r, s.handler.AuthorizeErrorRedirect(req, authErr), http.StatusFound)
			return
		}
		renderError(w, http.StatusBadRequest, "This login link is invalid: "+authErr.Description+".")
		return
	}
	slog.ErrorContext(r.Context(), "authorize failed", "error", err)
	renderError(w, http.StatusInternalServerError, "Something went wrong. Try again.")
}

// writeOAuthError writes an OAuth error as {"error", "error_description"}.
func writeOAuthError(w http.ResponseWriter, err error) {
	var oauthErr *pkg.OAuthError
	if !errors.As(err, &oauthErr) {
		oauthErr = pkg.NewOAuthError(http.StatusInternalServerError, "server_error", "internal server error")
	}
	if oauthErr.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="oauth"`)
	}
	writeJson(w, oauthErr.Status, map[string]string{
		"error":             oauthErr.Code,
		"error_description": oauthErr.Description,
	})
}

// unescapeBasic undoes the form-encoding RFC 6749 section 2.3.1 applies to
// client credentials in HTTP Basic auth.
func unescapeBasic(s string) string {
	if u, err := url.QueryUnescape(s); err == nil {
		return u
	}
	return s
}

type loginPage struct {
	Client  models.OAuthClient
	Request handler.AuthorizeRequest
	Error   string
}

// redirectHost is shown on the login page so a login link that would send
// your access to an unexpected site is easy to spot.
func (p loginPage) RedirectHost() string {
	u, err := url.Parse(p.Request.RedirectUri)
	if err != nil || u.Host == "" {
		return p.Request.RedirectUri
	}
	return u.Host
}

// The page loads nothing external, can't be framed and only runs inline CSS.
const pageCsp = "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'"

func renderLogin(w http.ResponseWriter, status int, page loginPage) {
	renderPage(w, status, loginTemplate, page)
}

func renderError(w http.ResponseWriter, status int, message string) {
	renderPage(w, status, errorTemplate, message)
}

func renderPage(w http.ResponseWriter, status int, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", pageCsp)
	w.WriteHeader(status)
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("render page failed", "error", err)
	}
}

const pageStyle = `<style>
body{font-family:system-ui,sans-serif;background:#f5f5f4;color:#1c1917;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0;padding:16px}
main{background:#fff;border:1px solid #e7e5e4;border-radius:12px;padding:28px;max-width:380px;width:100%}
h1{font-size:1.2rem;margin:0 0 12px}p{line-height:1.5;margin:0 0 16px}
input{width:100%;box-sizing:border-box;padding:10px;border:1px solid #d6d3d1;border-radius:8px;font-size:1rem;margin-bottom:12px}
button{width:100%;padding:10px;border:0;border-radius:8px;background:#1c1917;color:#fff;font-size:1rem;cursor:pointer}
.error{color:#b91c1c}.muted{color:#78716c;font-size:.9rem}
@media (prefers-color-scheme:dark){body{background:#1c1917;color:#f5f5f4}main{background:#292524;border-color:#44403c}input{background:#1c1917;color:#f5f5f4;border-color:#57534e}button{background:#f5f5f4;color:#1c1917}}
</style>`

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sign in · expense-tracker</title>` + pageStyle + `</head>
<body><main>
<h1>Sign in to expense-tracker</h1>
<p><strong>{{.Client.Name}}</strong> wants access to your expenses.</p>
<p class="muted">After you sign in you'll be sent to <strong>{{.RedirectHost}}</strong>. If you don't recognise it, close this page.</p>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<form method="post" action="/oauth/authorize">
<input type="hidden" name="response_type" value="{{.Request.ResponseType}}">
<input type="hidden" name="client_id" value="{{.Request.ClientId}}">
<input type="hidden" name="redirect_uri" value="{{.Request.RedirectUri}}">
<input type="hidden" name="code_challenge" value="{{.Request.CodeChallenge}}">
<input type="hidden" name="code_challenge_method" value="{{.Request.CodeChallengeMethod}}">
<input type="hidden" name="state" value="{{.Request.State}}">
<input type="hidden" name="resource" value="{{.Request.Resource}}">
<input type="password" name="password" placeholder="Password" autocomplete="current-password" required autofocus>
<button type="submit">Sign in</button>
</form>
</main></body></html>`))

var errorTemplate = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Sign-in problem · expense-tracker</title>` + pageStyle + `</head>
<body><main><h1>Sign-in problem</h1><p>{{.}}</p></main></body></html>`))

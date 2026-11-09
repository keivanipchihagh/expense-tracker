package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// ProtectedResourceMetadata handles /.well-known/oauth-protected-resource
// (RFC 9728): it tells clients which authorization server issues tokens.
// A path after the well-known prefix names the resource, so the document
// for /mcp says its resource is <public url>/mcp.
func (s *Server) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	resource := s.publicUrl + strings.TrimPrefix(r.URL.Path, "/.well-known/oauth-protected-resource")
	writeJson(w, http.StatusOK, map[string]any{
		"resource":                 resource,
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
	s.renderLogin(w, http.StatusOK, loginPage{Client: client, Request: req})
}

// AuthorizeSubmit handles POST /oauth/authorize: the login form.
func (s *Server) AuthorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderAuthorizeError(w, http.StatusBadRequest, "The login form could not be read.")
		return
	}
	req := authorizeRequestFrom(r.PostForm)

	email := r.PostForm.Get("email")
	redirect, err := s.handler.Authorize(r.Context(), req, email, r.PostForm.Get("password"))
	switch {
	case errors.Is(err, handler.ErrWrongPassword):
		client, _ := s.handler.CheckAuthorizeRequest(r.Context(), req)
		s.renderLogin(w, http.StatusUnauthorized, loginPage{Client: client, Request: req, Email: email, Error: "Wrong email or password."})
	case errors.Is(err, handler.ErrTooManyLogins):
		s.renderAuthorizeError(w, http.StatusTooManyRequests, "Too many failed sign-ins for this email. Try again in 15 minutes.")
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
		s.renderAuthorizeError(w, http.StatusBadRequest, "This login link is invalid: "+authErr.Description+".")
		return
	}
	slog.ErrorContext(r.Context(), "authorize failed", "error", err)
	s.renderAuthorizeError(w, http.StatusInternalServerError, "Something went wrong. Try again.")
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
	// Email is shown again after a failed attempt.
	Email string
	Error string
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

// authorizeCsp is the UI's policy without form-action: browsers apply
// form-action to the redirect after the login form is submitted, and that
// redirect goes to the client's own callback (another origin by design).
const authorizeCsp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"frame-ancestors 'none'; base-uri 'none'"

// renderLogin shows the OAuth login page, styled like the rest of the UI.
func (s *Server) renderLogin(w http.ResponseWriter, status int, page loginPage) {
	s.renderAuthorize(w, status, pageData{Title: "Sign in", Authorize: &page})
}

// renderAuthorizeError shows a sign-in problem the client can't be told
// about (for example, an unknown redirect URI).
func (s *Server) renderAuthorizeError(w http.ResponseWriter, status int, message string) {
	s.renderAuthorize(w, status, pageData{Title: "Sign-in problem", Error: message})
}

func (s *Server) renderAuthorize(w http.ResponseWriter, status int, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", authorizeCsp)
	w.WriteHeader(status)
	if err := pages["authorize.html"].ExecuteTemplate(w, "authorize.html", data); err != nil {
		slog.Error("render authorize page failed", "error", err)
	}
}

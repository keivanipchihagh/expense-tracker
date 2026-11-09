// Package tests runs the full HTTP stack (router, middlewares, OAuth,
// handler, repositories) against a real SQLite database, the way a client
// such as Claude or ChatGPT talks to it.
package tests

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/api"
	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	pkgHttp "github.com/keivanipchihagh/expense-tracker/pkg/http"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

const (
	// Every test server starts with this account (see newTestServer).
	email       = "alice@example.com"
	password    = "correct-horse-battery-staple"
	redirectUri = "http://localhost:9999/callback"
	verifier    = "test-verifier-0123456789-abcdefghijklmnopqrstuvwxyz"
)

type testServer struct {
	url   string
	db    *sqlite.Client
	h     *handler.Handler
	token string // access token used by call
	// email and password sign in through login and the UI helpers.
	email, password string
}

// noRedirect returns redirect responses instead of following them, so the
// OAuth redirects can be inspected.
var noRedirect = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	return newTestServerWith(t, true)
}

// newTestServerWith starts a server with sign-up on or off.
func newTestServerWith(t *testing.T, signupEnabled bool) *testServer {
	t.Helper()
	ctx := context.Background()

	db, err := sqlite.NewClient(ctx, filepath.Join(t.TempDir(), "expense.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	logRepo, err := repositories.NewLogRepository(ctx, db)
	require.NoError(t, err)
	expenseRepo, err := repositories.NewExpenseRepository(ctx, db, logRepo)
	require.NoError(t, err)
	oauthRepo, err := repositories.NewOAuthRepository(ctx, db)
	require.NoError(t, err)
	sessionRepo, err := repositories.NewSessionRepository(ctx, db)
	require.NoError(t, err)
	userRepo, err := repositories.NewUserRepository(ctx, db)
	require.NoError(t, err)

	// The public URL is the test server's own address, known only once it runs.
	var routes http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { routes.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)

	h := handler.NewHandler(expenseRepo, oauthRepo, sessionRepo, userRepo, srv.URL, signupEnabled)
	h.SetBcryptCost(bcrypt.MinCost)
	routes = pkgHttp.NewHttpServer(api.NewServer(h, "test", srv.URL).Handler(), 5*time.Second, 1<<20).Handler

	s := &testServer{url: srv.URL, db: db, h: h, email: email, password: password}
	s.addUser(t, email, password)
	return s
}

// addUser creates an account directly, like sign-up would.
func (s *testServer) addUser(t *testing.T, email, password string) {
	t.Helper()
	_, err := s.h.EnsureOwner(context.Background(), handler.SignUpInput{Email: email, Password: password})
	require.NoError(t, err)
}

// as returns a view of the server signed in as another account: its own
// OAuth token for call, and its email for the UI helpers.
func (s *testServer) as(t *testing.T, email, password string) *testServer {
	t.Helper()
	s.addUser(t, email, password)
	other := &testServer{url: s.url, db: s.db, h: s.h, email: email, password: password}
	other.login(t)
	return other
}

func challenge(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func decode(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if out != nil && len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, out), string(raw))
	}
}

// register registers a public client and returns its id.
func (s *testServer) register(t *testing.T) string {
	t.Helper()
	resp, err := http.Post(s.url+"/oauth/register", "application/json",
		strings.NewReader(`{"client_name":"Test Client","redirect_uris":["`+redirectUri+`"],"token_endpoint_auth_method":"none"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var client struct {
		ClientId string `json:"client_id"`
	}
	decode(t, resp, &client)
	return client.ClientId
}

func authorizeParams(clientId string) url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientId},
		"redirect_uri":          {redirectUri},
		"code_challenge":        {challenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {"state-123"},
	}
}

// authorize submits the login form and returns the authorization code.
func (s *testServer) authorize(t *testing.T, clientId string) string {
	t.Helper()
	form := authorizeParams(clientId)
	form.Set("email", s.email)
	form.Set("password", s.password)

	resp, err := noRedirect.PostForm(s.url+"/oauth/authorize", form)
	require.NoError(t, err)
	decode(t, resp, nil)
	require.Equal(t, http.StatusFound, resp.StatusCode)

	location, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "localhost:9999", location.Host)
	require.Equal(t, "state-123", location.Query().Get("state"))
	return location.Query().Get("code")
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func (s *testServer) tokenRequest(t *testing.T, form url.Values) (int, tokens, map[string]string) {
	t.Helper()
	resp, err := http.PostForm(s.url+"/oauth/token", form)
	require.NoError(t, err)

	var raw json.RawMessage
	decode(t, resp, &raw)
	var tok tokens
	var oauthErr map[string]string
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.Unmarshal(raw, &tok))
	} else {
		require.NoError(t, json.Unmarshal(raw, &oauthErr))
	}
	return resp.StatusCode, tok, oauthErr
}

func codeGrant(clientId, code string) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientId},
		"code":          {code},
		"redirect_uri":  {redirectUri},
		"code_verifier": {verifier},
	}
}

// login runs the whole OAuth flow and stores the access token for call.
func (s *testServer) login(t *testing.T) {
	t.Helper()
	clientId := s.register(t)
	status, tok, _ := s.tokenRequest(t, codeGrant(clientId, s.authorize(t, clientId)))
	require.Equal(t, http.StatusOK, status)
	s.token = tok.AccessToken
}

// call sends an authenticated request and decodes the JSON response into out.
func (s *testServer) call(t *testing.T, method, path, body string, out any) int {
	t.Helper()
	req, err := http.NewRequest(method, s.url+path, strings.NewReader(body))
	require.NoError(t, err)
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	decode(t, resp, out)
	return resp.StatusCode
}

func (s *testServer) logActions(t *testing.T) []string {
	t.Helper()
	rows, err := s.db.Query("SELECT action FROM logs ORDER BY id")
	require.NoError(t, err)
	defer rows.Close()

	actions := []string{}
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		actions = append(actions, a)
	}
	return actions
}

func TestApiRequiresToken(t *testing.T) {
	s := newTestServer(t)

	resp, err := http.Get(s.url + "/api/v1/expenses")
	require.NoError(t, err)
	decode(t, resp, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, `Bearer resource_metadata="`+s.url+`/.well-known/oauth-protected-resource"`, resp.Header.Get("WWW-Authenticate"),
		"the 401 tells clients where to discover how to log in")

	s.token = "made-up-token"
	assert.Equal(t, http.StatusUnauthorized, s.call(t, http.MethodGet, "/api/v1/expenses", "", nil))

	s.token = ""
	assert.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/health", "", nil), "health stays public")
}

func TestDiscovery(t *testing.T) {
	s := newTestServer(t)

	var resource map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/.well-known/oauth-protected-resource", "", &resource))
	assert.Equal(t, s.url, resource["resource"])
	assert.Equal(t, []any{s.url}, resource["authorization_servers"])

	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", nil),
		"clients may append the resource path")

	var server map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/.well-known/oauth-authorization-server", "", &server))
	assert.Equal(t, s.url, server["issuer"])
	assert.Equal(t, s.url+"/oauth/authorize", server["authorization_endpoint"])
	assert.Equal(t, s.url+"/oauth/token", server["token_endpoint"])
	assert.Equal(t, s.url+"/oauth/register", server["registration_endpoint"])
	assert.Equal(t, []any{"S256"}, server["code_challenge_methods_supported"])
}

func TestOAuthFlow(t *testing.T) {
	s := newTestServer(t)
	clientId := s.register(t)

	// The login page names the client and where it will redirect to.
	resp, err := http.Get(s.url + "/oauth/authorize?" + authorizeParams(clientId).Encode())
	require.NoError(t, err)
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(page), "Test Client")
	assert.Contains(t, string(page), "localhost:9999")
	assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'")

	// A wrong password shows the form again.
	form := authorizeParams(clientId)
	form.Set("email", email)
	form.Set("password", "wrong")
	resp, err = noRedirect.PostForm(s.url+"/oauth/authorize", form)
	require.NoError(t, err)
	page, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, string(page), "Wrong email or password")
	assert.Contains(t, string(page), `value="alice@example.com"`, "the email is kept")

	// The right password yields a code, which is exchanged once for tokens.
	code := s.authorize(t, clientId)
	status, tok, _ := s.tokenRequest(t, codeGrant(clientId, code))
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "Bearer", tok.TokenType)
	assert.Equal(t, 3600, tok.ExpiresIn)

	status, _, oauthErr := s.tokenRequest(t, codeGrant(clientId, code))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "invalid_grant", oauthErr["error"], "a code works only once")

	// The access token opens the API.
	s.token = tok.AccessToken
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses", "", nil))

	// Refreshing returns new tokens and retires the old refresh token.
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {clientId}, "refresh_token": {tok.RefreshToken}}
	status, refreshed, _ := s.tokenRequest(t, refresh)
	require.Equal(t, http.StatusOK, status)
	assert.NotEqual(t, tok.RefreshToken, refreshed.RefreshToken)

	s.token = refreshed.AccessToken
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses", "", nil))

	status, _, oauthErr = s.tokenRequest(t, refresh)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "invalid_grant", oauthErr["error"], "a refresh token works only once")
}

func TestOAuthLoginPage_MatchesUi(t *testing.T) {
	s := newTestServer(t)
	clientId := s.register(t)
	get := func(params url.Values) (int, http.Header, string) {
		resp, err := http.Get(s.url + "/oauth/authorize?" + params.Encode())
		require.NoError(t, err)
		defer resp.Body.Close()
		page, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(page)
	}

	params := authorizeParams(clientId)
	params.Set("state", `"><script>alert(1)</script>`)
	status, header, page := get(params)
	require.Equal(t, http.StatusOK, status)

	// The shared stylesheet, layout and theme switch; no inline styles.
	assert.Contains(t, page, `<link rel="stylesheet" href="/static/style.css">`)
	assert.Contains(t, page, `<script src="/static/theme.js"></script>`)
	assert.Contains(t, page, "data-theme-toggle", "the theme switch is in the top bar")
	assert.Contains(t, page, `<section class="login">`)
	assert.Contains(t, page, `<button type="submit" class="primary block">Sign in</button>`)
	assert.NotContains(t, page, "<style")
	assert.NotContains(t, page, "style=")
	assert.NotContains(t, page, "Sign out", "the visitor isn't signed in to the UI")

	csp := header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "style-src 'self'")
	assert.Contains(t, csp, "script-src 'self'")
	assert.Contains(t, csp, "frame-ancestors 'none'")
	assert.NotContains(t, csp, "unsafe-inline")
	assert.NotContains(t, csp, "form-action", "browsers would block the redirect to the client's callback")

	// Every OAuth parameter rides along, escaped.
	for _, field := range []string{"response_type", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "state", "resource"} {
		assert.Contains(t, page, `name="`+field+`"`)
	}
	assert.NotContains(t, page, "<script>alert(1)</script>")
	assert.Contains(t, page, `value="&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;"`)

	// Problems that can't go back to the client get the same look.
	bad := authorizeParams(clientId)
	bad.Set("redirect_uri", "https://evil.example/callback")
	status, header, page = get(bad)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, page, "<h1>Sign-in problem</h1>")
	assert.Contains(t, page, `<p class="alert" role="alert">This login link is invalid:`)
	assert.Contains(t, page, `href="/static/style.css"`)
	assert.NotContains(t, header.Get("Content-Security-Policy"), "unsafe-inline")
}

func TestHealthHidesVersion(t *testing.T) {
	s := newTestServer(t)
	resp, err := http.Get(s.url + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

func TestOAuthFlow_RefreshWithWrongClientDoesNotBurnToken(t *testing.T) {
	s := newTestServer(t)
	clientId := s.register(t)
	otherId := s.register(t)

	status, tok, _ := s.tokenRequest(t, codeGrant(clientId, s.authorize(t, clientId)))
	require.Equal(t, http.StatusOK, status)

	status, _, _ = s.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {otherId}, "refresh_token": {tok.RefreshToken}})
	assert.Equal(t, http.StatusBadRequest, status)

	status, _, _ = s.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientId}, "refresh_token": {tok.RefreshToken}})
	assert.Equal(t, http.StatusOK, status, "the real owner can still refresh")
}

func TestOAuthFlow_WrongVerifier(t *testing.T) {
	s := newTestServer(t)
	clientId := s.register(t)

	grant := codeGrant(clientId, s.authorize(t, clientId))
	grant.Set("code_verifier", "another-verifier-0123456789-abcdefghijklmnopqrstu")

	status, _, oauthErr := s.tokenRequest(t, grant)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "invalid_grant", oauthErr["error"], "PKCE stops a stolen code from being used")
}

func TestOAuthFlow_ConfidentialClientWithBasicAuth(t *testing.T) {
	s := newTestServer(t)

	resp, err := http.Post(s.url+"/oauth/register", "application/json",
		strings.NewReader(`{"client_name":"Server App","redirect_uris":["`+redirectUri+`"]}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var client struct {
		ClientId     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		AuthMethod   string `json:"token_endpoint_auth_method"`
	}
	decode(t, resp, &client)
	require.NotEmpty(t, client.ClientSecret)
	assert.Equal(t, "client_secret_basic", client.AuthMethod)

	grant := codeGrant(client.ClientId, s.authorize(t, client.ClientId))
	grant.Del("client_id")

	tokenWith := func(secret string) int {
		req, _ := http.NewRequest(http.MethodPost, s.url+"/oauth/token", strings.NewReader(grant.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(client.ClientId, secret)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		decode(t, resp, nil)
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusUnauthorized, tokenWith("wrong-secret"))
	assert.Equal(t, http.StatusOK, tokenWith(client.ClientSecret))
}

func TestExpenseLifecycle(t *testing.T) {
	s := newTestServer(t)
	s.login(t)

	// Create
	var created map[string]any
	status := s.call(t, http.MethodPost, "/api/v1/expenses",
		`{"amount":12.5,"occurred_at":"2026-11-07T13:20:00+01:00","category":"food","merchant":"Rewe"}`,
		&created)
	require.Equal(t, http.StatusCreated, status)
	id := created["id"].(string)
	assert.Equal(t, "12.50", created["amount"], "a JSON number is stored exactly")
	assert.NotContains(t, created, "currency", "expenses have no currency")

	// Get
	var fetched map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses/"+id, "", &fetched))
	assert.Equal(t, created, fetched, "get returns what create returned")

	// List
	var listed []map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses?date_from=2026-11-07&date_to=2026-11-07", "", &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, id, listed[0]["id"])

	// Update: change the amount, clear the merchant, add a note
	var updated map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodPatch, "/api/v1/expenses/"+id,
		`{"amount":"20","merchant":null,"note":"split with Alex"}`, &updated))
	assert.Equal(t, "20.00", updated["amount"])
	assert.Nil(t, updated["merchant"])
	assert.Equal(t, "split with Alex", updated["note"])
	assert.Equal(t, created["created_at"], updated["created_at"])
	assert.NotEqual(t, created["updated_at"], updated["updated_at"])

	// A patch that changes nothing writes no log
	require.Equal(t, http.StatusOK, s.call(t, http.MethodPatch, "/api/v1/expenses/"+id, `{"amount":"20.00"}`, nil))

	// Delete
	require.Equal(t, http.StatusNoContent, s.call(t, http.MethodDelete, "/api/v1/expenses/"+id, "", nil))
	require.Equal(t, http.StatusNotFound, s.call(t, http.MethodGet, "/api/v1/expenses/"+id, "", nil))
	require.Equal(t, http.StatusNotFound, s.call(t, http.MethodDelete, "/api/v1/expenses/"+id, "", nil))

	assert.Equal(t, []string{"create", "update", "delete"}, s.logActions(t))
}

func TestValidationErrorsAreReported(t *testing.T) {
	s := newTestServer(t)
	s.login(t)

	var resp struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	status := s.call(t, http.MethodPost, "/api/v1/expenses",
		`{"amount":"1.234","currency":"EUR","occurred_at":"2026-11-07T13:20:00","category":"rent","tip":1}`, &resp)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, "validation_failed", resp.Error.Code)
	assert.Equal(t, "must have at most 2 decimal places", resp.Error.Fields["amount"])
	assert.Equal(t, "unknown field", resp.Error.Fields["currency"], "currency is no longer accepted")
	assert.Contains(t, resp.Error.Fields["occurred_at"], "UTC offset")
	assert.Contains(t, resp.Error.Fields["category"], "must be one of")
	assert.Equal(t, "unknown field", resp.Error.Fields["tip"])
	assert.Empty(t, s.logActions(t), "nothing is written")
}

func TestSecurityHeadersOnApiResponses(t *testing.T) {
	s := newTestServer(t)

	resp, err := http.Get(s.url + "/api/v1/expenses")
	require.NoError(t, err)
	decode(t, resp, nil)

	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
}

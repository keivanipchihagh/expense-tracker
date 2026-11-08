package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

const (
	authCodeTTL     = 5 * time.Minute
	accessTokenTTL  = time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour

	maxRedirectUris  = 10
	maxClientNameLen = 100

	// After maxFailedLogins wrong passwords within failedLoginWindow, the
	// login page refuses all attempts until the window has passed.
	maxFailedLogins   = 10
	failedLoginWindow = 15 * time.Minute

	// Registration is open, so it is capped: at most maxRegistrations new
	// clients per registrationWindow.
	maxRegistrations   = 20
	registrationWindow = time.Hour
)

// codeChallengePattern matches a PKCE S256 challenge: base64url, 43 chars.
var codeChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// codeVerifierPattern matches a PKCE code verifier (RFC 7636 section 4.1).
var codeVerifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

var (
	// ErrWrongPassword is returned by Authorize when the password is wrong.
	ErrWrongPassword = errors.New("wrong password")
	// ErrTooManyLogins is returned by Authorize while failed logins are locked out.
	ErrTooManyLogins = errors.New("too many failed logins, try again later")
)

// RegisterClientRequest is the subset of RFC 7591 client metadata we use.
type RegisterClientRequest struct {
	RedirectUris            []string
	ClientName              string
	TokenEndpointAuthMethod string
}

// AuthorizeRequest holds the query parameters of an authorization request.
type AuthorizeRequest struct {
	ResponseType        string
	ClientId            string
	RedirectUri         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
	Resource            string
}

// AuthorizeError is an error in an authorization request. When Redirect is
// true the error is sent back to the client's redirect URI; otherwise the
// redirect URI can't be trusted and the error is shown to the user.
type AuthorizeError struct {
	Redirect    bool
	Code        string
	Description string
}

func (e *AuthorizeError) Error() string { return e.Code + ": " + e.Description }

// TokenRequest holds the form fields of a token request. ClientSecret comes
// from either the form or HTTP Basic auth.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectUri  string
	CodeVerifier string
	RefreshToken string
	ClientId     string
	ClientSecret string
}

// TokenResponse is the successful token endpoint response (RFC 6749 section 5.1).
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// RegisterClient registers an OAuth client (RFC 7591). Registration is open,
// as MCP clients expect; access still requires logging in with the password.
// The returned secret is empty for public clients.
func (h *Handler) RegisterClient(ctx context.Context, req RegisterClientRequest) (models.OAuthClient, string, error) {
	if h.registrations.full(h.now()) {
		h.logger.WarnContext(ctx, "oauth registration refused: rate limit reached")
		return models.OAuthClient{}, "", pkg.NewOAuthError(http.StatusTooManyRequests, "temporarily_unavailable", "too many registrations, try again later")
	}
	if len(req.RedirectUris) == 0 || len(req.RedirectUris) > maxRedirectUris {
		return models.OAuthClient{}, "", pkg.NewOAuthError(http.StatusBadRequest, "invalid_redirect_uri", "between 1 and 10 redirect_uris are required")
	}
	for _, u := range req.RedirectUris {
		if err := validateRedirectUri(u); err != nil {
			return models.OAuthClient{}, "", pkg.NewOAuthError(http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		}
	}

	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = models.AuthMethodClientSecretBasic // the RFC 7591 default
	}
	if !slices.Contains([]string{models.AuthMethodNone, models.AuthMethodClientSecretPost, models.AuthMethodClientSecretBasic}, method) {
		return models.OAuthClient{}, "", pkg.NewOAuthError(http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
	}

	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = "Unnamed client"
	}
	if len([]rune(name)) > maxClientNameLen {
		return models.OAuthClient{}, "", pkg.NewOAuthError(http.StatusBadRequest, "invalid_client_metadata", "client_name is too long")
	}

	client := models.OAuthClient{
		Id:           uuid.Must(uuid.NewV7()).String(),
		Name:         name,
		RedirectUris: req.RedirectUris,
		AuthMethod:   method,
		CreatedAt:    h.now().UTC(),
	}
	var secret string
	if method != models.AuthMethodNone {
		secret = randomToken()
		hash := hashSecret(secret)
		client.SecretHash = &hash
	}

	if err := h.oauthRepo.CreateClient(ctx, client); err != nil {
		h.logger.ErrorContext(ctx, "register oauth client failed", "error", err)
		return models.OAuthClient{}, "", pkg.NewOAuthError(http.StatusInternalServerError, "server_error", "internal server error")
	}
	h.registrations.record(h.now())

	h.logger.InfoContext(ctx, "oauth client registered", "client_id", client.Id, "client_name", client.Name)
	return client, secret, nil
}

// CheckAuthorizeRequest validates an authorization request and returns the
// client it is for. Errors are *AuthorizeError or pkg.InternalError.
func (h *Handler) CheckAuthorizeRequest(ctx context.Context, req AuthorizeRequest) (models.OAuthClient, error) {
	// The client and redirect URI are checked first: until both are known to
	// be good, errors must not be redirected anywhere.
	client, err := h.oauthRepo.GetClient(ctx, req.ClientId)
	if errors.Is(err, repositories.ErrOAuthNotFound) {
		return models.OAuthClient{}, &AuthorizeError{Code: "invalid_client", Description: "unknown client_id"}
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "get oauth client failed", "error", err)
		return models.OAuthClient{}, pkg.InternalError
	}
	if !redirectUriAllowed(client.RedirectUris, req.RedirectUri) {
		return models.OAuthClient{}, &AuthorizeError{Code: "invalid_request", Description: "redirect_uri is not registered for this client"}
	}

	switch {
	case req.ResponseType != "code":
		return client, &AuthorizeError{Redirect: true, Code: "unsupported_response_type", Description: "response_type must be code"}
	case req.CodeChallengeMethod != "S256" || !codeChallengePattern.MatchString(req.CodeChallenge):
		return client, &AuthorizeError{Redirect: true, Code: "invalid_request", Description: "PKCE with code_challenge_method S256 is required"}
	case req.Resource != "" && !strings.HasPrefix(req.Resource, h.publicUrl):
		return client, &AuthorizeError{Redirect: true, Code: "invalid_target", Description: "resource is not served by this server"}
	}
	return client, nil
}

// Authorize checks the password and, if it is right, issues an authorization
// code and returns the URL to redirect the browser to. Besides the errors of
// CheckAuthorizeRequest it returns ErrWrongPassword and ErrTooManyLogins.
func (h *Handler) Authorize(ctx context.Context, req AuthorizeRequest, password string) (string, error) {
	client, err := h.CheckAuthorizeRequest(ctx, req)
	if err != nil {
		return "", err
	}

	now := h.now()
	if h.logins.full(now) {
		return "", ErrTooManyLogins
	}
	if !secretsEqual(password, h.password) {
		h.logins.record(now)
		h.logger.WarnContext(ctx, "oauth login failed: wrong password", "client_id", client.Id)
		return "", ErrWrongPassword
	}

	code := randomToken()
	err = h.oauthRepo.CreateCode(ctx, models.OAuthCode{
		CodeHash:      hashSecret(code),
		ClientId:      client.Id,
		RedirectUri:   req.RedirectUri,
		CodeChallenge: req.CodeChallenge,
		ExpiresAt:     now.Add(authCodeTTL).UTC(),
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "create oauth code failed", "error", err)
		return "", pkg.InternalError
	}

	h.logger.InfoContext(ctx, "oauth login succeeded", "client_id", client.Id, "client_name", client.Name)
	return redirectWithParams(req.RedirectUri, url.Values{"code": {code}, "state": {req.State}, "iss": {h.publicUrl}}), nil
}

// AuthorizeErrorRedirect returns the client's redirect URI carrying err.
func (h *Handler) AuthorizeErrorRedirect(req AuthorizeRequest, err *AuthorizeError) string {
	return redirectWithParams(req.RedirectUri, url.Values{
		"error":             {err.Code},
		"error_description": {err.Description},
		"state":             {req.State},
		"iss":               {h.publicUrl},
	})
}

// Token handles the authorization_code and refresh_token grants. Errors are
// *pkg.OAuthError.
func (h *Handler) Token(ctx context.Context, req TokenRequest) (TokenResponse, error) {
	client, err := h.authenticateClient(ctx, req.ClientId, req.ClientSecret)
	if err != nil {
		return TokenResponse{}, err
	}

	switch req.GrantType {
	case "authorization_code":
		return h.exchangeCode(ctx, client, req)
	case "refresh_token":
		return h.refresh(ctx, client, req)
	default:
		return TokenResponse{}, pkg.NewOAuthError(http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

// ListConnectedApps returns the apps that currently hold a valid token.
func (h *Handler) ListConnectedApps(ctx context.Context) ([]models.ConnectedApp, error) {
	apps, err := h.oauthRepo.ListConnectedApps(ctx, h.now())
	if err != nil {
		h.logger.ErrorContext(ctx, "list connected apps failed", "error", err)
		return nil, pkg.InternalError
	}
	return apps, nil
}

// RevokeApp removes an app with all its tokens. Its next request fails with
// 401 and it has to sign in again.
func (h *Handler) RevokeApp(ctx context.Context, clientId string) error {
	err := h.oauthRepo.DeleteClient(ctx, clientId)
	if errors.Is(err, repositories.ErrOAuthNotFound) {
		return pkg.ErrAppNotFound
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "revoke app failed", "error", err)
		return pkg.InternalError
	}
	h.logger.InfoContext(ctx, "app access revoked", "client_id", clientId)
	return nil
}

// ValidateAccessToken reports whether token is a current access token. An
// error means the check itself failed.
func (h *Handler) ValidateAccessToken(ctx context.Context, token string) (bool, error) {
	t, err := h.oauthRepo.GetToken(ctx, hashSecret(token), models.OAuthTokenAccess)
	if errors.Is(err, repositories.ErrOAuthNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return h.now().Before(t.ExpiresAt), nil
}

func (h *Handler) authenticateClient(ctx context.Context, clientId, secret string) (models.OAuthClient, error) {
	invalid := pkg.NewOAuthError(http.StatusUnauthorized, "invalid_client", "client authentication failed")

	client, err := h.oauthRepo.GetClient(ctx, clientId)
	if errors.Is(err, repositories.ErrOAuthNotFound) {
		return models.OAuthClient{}, invalid
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "get oauth client failed", "error", err)
		return models.OAuthClient{}, pkg.NewOAuthError(http.StatusInternalServerError, "server_error", "internal server error")
	}
	if client.SecretHash != nil && !secretsEqual(hashSecret(secret), *client.SecretHash) {
		return models.OAuthClient{}, invalid
	}
	return client, nil
}

func (h *Handler) exchangeCode(ctx context.Context, client models.OAuthClient, req TokenRequest) (TokenResponse, error) {
	invalid := pkg.NewOAuthError(http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired or already used")

	code, err := h.oauthRepo.ConsumeCode(ctx, hashSecret(req.Code))
	if errors.Is(err, repositories.ErrOAuthNotFound) {
		return TokenResponse{}, invalid
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "consume oauth code failed", "error", err)
		return TokenResponse{}, pkg.NewOAuthError(http.StatusInternalServerError, "server_error", "internal server error")
	}

	switch {
	case !h.now().Before(code.ExpiresAt),
		code.ClientId != client.Id,
		code.RedirectUri != req.RedirectUri,
		!codeVerifierPattern.MatchString(req.CodeVerifier),
		!secretsEqual(pkceChallenge(req.CodeVerifier), code.CodeChallenge):
		return TokenResponse{}, invalid
	}
	return h.issueTokens(ctx, client)
}

func (h *Handler) refresh(ctx context.Context, client models.OAuthClient, req TokenRequest) (TokenResponse, error) {
	invalid := pkg.NewOAuthError(http.StatusBadRequest, "invalid_grant", "refresh token is invalid, expired or already used")

	// Check ownership and expiry before consuming, so a refresh token sent
	// with the wrong client id is rejected without being destroyed.
	hash := hashSecret(req.RefreshToken)
	t, err := h.oauthRepo.GetToken(ctx, hash, models.OAuthTokenRefresh)
	if errors.Is(err, repositories.ErrOAuthNotFound) {
		return TokenResponse{}, invalid
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "get refresh token failed", "error", err)
		return TokenResponse{}, pkg.NewOAuthError(http.StatusInternalServerError, "server_error", "internal server error")
	}
	if t.ClientId != client.Id || !h.now().Before(t.ExpiresAt) {
		return TokenResponse{}, invalid
	}

	// Consuming the token rotates it: each refresh token works once. If two
	// requests race, only one consumes it.
	if _, err := h.oauthRepo.ConsumeToken(ctx, hash, models.OAuthTokenRefresh); err != nil {
		if errors.Is(err, repositories.ErrOAuthNotFound) {
			return TokenResponse{}, invalid
		}
		h.logger.ErrorContext(ctx, "consume refresh token failed", "error", err)
		return TokenResponse{}, pkg.NewOAuthError(http.StatusInternalServerError, "server_error", "internal server error")
	}
	return h.issueTokens(ctx, client)
}

func (h *Handler) issueTokens(ctx context.Context, client models.OAuthClient) (TokenResponse, error) {
	now := h.now().UTC()
	access, refresh := randomToken(), randomToken()

	err := h.oauthRepo.CreateTokens(ctx,
		models.OAuthToken{TokenHash: hashSecret(access), Kind: models.OAuthTokenAccess, ClientId: client.Id, ExpiresAt: now.Add(accessTokenTTL), CreatedAt: now},
		models.OAuthToken{TokenHash: hashSecret(refresh), Kind: models.OAuthTokenRefresh, ClientId: client.Id, ExpiresAt: now.Add(refreshTokenTTL), CreatedAt: now},
	)
	if err != nil {
		h.logger.ErrorContext(ctx, "create oauth tokens failed", "error", err)
		return TokenResponse{}, pkg.NewOAuthError(http.StatusInternalServerError, "server_error", "internal server error")
	}

	// Expired rows are pruned whenever tokens are issued; failure is harmless.
	if err := h.oauthRepo.DeleteExpired(ctx, now); err != nil {
		h.logger.WarnContext(ctx, "delete expired oauth records failed", "error", err)
	}

	h.logger.InfoContext(ctx, "oauth tokens issued", "client_id", client.Id)
	return TokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int(accessTokenTTL.Seconds()),
		RefreshToken: refresh,
	}, nil
}

// validateRedirectUri accepts https URIs, http URIs on a loopback host, and
// private-use schemes of native apps (RFC 8252). Script-like schemes and
// fragments are rejected.
func validateRedirectUri(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return errors.New("redirect_uri must be an absolute URI")
	}
	if u.Fragment != "" {
		return errors.New("redirect_uri must not contain a fragment")
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if u.Host == "" {
			return errors.New("redirect_uri must have a host")
		}
	case "http":
		if !isLoopback(u.Hostname()) {
			return errors.New("http redirect_uri is only allowed for localhost")
		}
	case "javascript", "data", "file", "vbscript":
		return errors.New("redirect_uri scheme is not allowed")
	}
	return nil
}

// redirectUriAllowed reports whether uri is one of the registered URIs.
// Loopback URIs match on any port, as native apps pick a free port at
// runtime (RFC 8252 section 7.3).
func redirectUriAllowed(registered []string, uri string) bool {
	if uri == "" {
		return false
	}
	if slices.Contains(registered, uri) {
		return true
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "http" || !isLoopback(u.Hostname()) {
		return false
	}
	for _, r := range registered {
		ru, err := url.Parse(r)
		if err == nil && ru.Scheme == "http" && ru.Hostname() == u.Hostname() &&
			ru.Path == u.Path && ru.RawQuery == u.RawQuery {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// redirectWithParams appends params to uri's query, dropping empty values.
func redirectWithParams(uri string, params url.Values) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	q := u.Query()
	for k, vs := range params {
		for _, v := range vs {
			if v != "" {
				q.Add(k, v)
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// randomToken returns 32 random bytes, base64url-encoded (43 characters).
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashSecret returns the hex SHA-256 of a secret. Codes, tokens and client
// secrets are random and long, so a fast hash is enough to make a leaked
// database useless.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// pkceChallenge returns the S256 challenge for a code verifier (RFC 7636).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// secretsEqual compares two strings in constant time. Hashing first gives
// equal lengths, so the length of the secret doesn't leak either.
func secretsEqual(a, b string) bool {
	ha, hb := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

// rateLimiter counts events in a sliding window. Limits are global rather
// than per client IP: there is one user, and IPs behind a proxy or tunnel
// can't be trusted.
type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	events []time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window}
}

// full reports whether the window already holds max events.
func (l *rateLimiter) full(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	return len(l.events) >= l.max
}

func (l *rateLimiter) record(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now)
	l.events = append(l.events, now)
}

func (l *rateLimiter) prune(now time.Time) {
	cutoff := now.Add(-l.window)
	i := 0
	for i < len(l.events) && l.events[i].Before(cutoff) {
		i++
	}
	l.events = l.events[i:]
}

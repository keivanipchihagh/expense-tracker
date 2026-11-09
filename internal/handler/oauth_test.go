package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// A PKCE pair. The challenge was computed independently with Python:
// base64url(sha256(verifier)) without padding.
const (
	testVerifier  = "dBjftJeZ4CVP-mJ92Kn9hMvXWHYn_c5sZBD9gRrcb_A"
	testChallenge = "ZVmEkGg8WhzRIXNVOL5FLW5hfwlPumDi5KJjp1Vflqw"
)

var testNow = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func publicClient() models.OAuthClient {
	return models.OAuthClient{
		Id:           "client-1",
		Name:         "Claude",
		RedirectUris: []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:3000/callback"},
		AuthMethod:   models.AuthMethodNone,
	}
}

func validAuthorizeRequest() AuthorizeRequest {
	return AuthorizeRequest{
		ResponseType:        "code",
		ClientId:            "client-1",
		RedirectUri:         "https://claude.ai/api/mcp/auth_callback",
		CodeChallenge:       testChallenge,
		CodeChallengeMethod: "S256",
		State:               "xyz",
	}
}

// setupOAuthSuite returns a suite whose handler clock is fixed at testNow.
func setupOAuthSuite(t *testing.T) *HandlerTestSuite {
	ts := setupHandlerTestSuite(t)
	ts.handler.now = func() time.Time { return testNow }
	return ts
}

func oauthErrCode(t *testing.T, err error) (int, string) {
	t.Helper()
	var oauthErr *pkg.OAuthError
	require.ErrorAs(t, err, &oauthErr)
	return oauthErr.Status, oauthErr.Code
}

func TestRegisterClient(t *testing.T) {
	tests := []struct {
		name       string
		req        RegisterClientRequest
		wantMethod string
		wantSecret bool
		wantErr    string
	}{
		{name: "public client", req: RegisterClientRequest{RedirectUris: []string{"https://chatgpt.com/connector/callback"}, ClientName: "ChatGPT", TokenEndpointAuthMethod: "none"}, wantMethod: "none"},
		{name: "default is client_secret_basic", req: RegisterClientRequest{RedirectUris: []string{"https://a.example/cb"}}, wantMethod: "client_secret_basic", wantSecret: true},
		{name: "client_secret_post", req: RegisterClientRequest{RedirectUris: []string{"https://a.example/cb"}, TokenEndpointAuthMethod: "client_secret_post"}, wantMethod: "client_secret_post", wantSecret: true},
		{name: "loopback http", req: RegisterClientRequest{RedirectUris: []string{"http://127.0.0.1:33418/callback"}, TokenEndpointAuthMethod: "none"}, wantMethod: "none"},
		{name: "native app scheme", req: RegisterClientRequest{RedirectUris: []string{"cursor://anysphere.cursor-mcp/oauth/callback"}, TokenEndpointAuthMethod: "none"}, wantMethod: "none"},
		{name: "no redirect uris", req: RegisterClientRequest{}, wantErr: "invalid_redirect_uri"},
		{name: "plain http", req: RegisterClientRequest{RedirectUris: []string{"http://evil.example/cb"}}, wantErr: "invalid_redirect_uri"},
		{name: "javascript scheme", req: RegisterClientRequest{RedirectUris: []string{"javascript:alert(1)"}}, wantErr: "invalid_redirect_uri"},
		{name: "fragment", req: RegisterClientRequest{RedirectUris: []string{"https://a.example/cb#x"}}, wantErr: "invalid_redirect_uri"},
		{name: "relative uri", req: RegisterClientRequest{RedirectUris: []string{"/callback"}}, wantErr: "invalid_redirect_uri"},
		{name: "unsupported auth method", req: RegisterClientRequest{RedirectUris: []string{"https://a.example/cb"}, TokenEndpointAuthMethod: "private_key_jwt"}, wantErr: "invalid_client_metadata"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)

			var stored models.OAuthClient
			if tt.wantErr == "" {
				ts.oauthRepo.EXPECT().CreateClient(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, c models.OAuthClient) error { stored = c; return nil })
			}

			client, secret, err := ts.handler.RegisterClient(context.Background(), tt.req)
			if tt.wantErr != "" {
				_, code := oauthErrCode(t, err)
				assert.Equal(t, tt.wantErr, code)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, stored, client)
			assert.Equal(t, tt.wantMethod, client.AuthMethod)
			assert.NotEmpty(t, client.Id)

			if tt.wantSecret {
				require.NotEmpty(t, secret)
				require.NotNil(t, client.SecretHash)
				assert.Equal(t, hashSecret(secret), *client.SecretHash, "only the hash is stored")
			} else {
				assert.Empty(t, secret)
				assert.Nil(t, client.SecretHash)
			}
		})
	}
}

func TestRegisterClient_DefaultName(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.oauthRepo.EXPECT().CreateClient(gomock.Any(), gomock.Any()).Return(nil)

	client, _, err := ts.handler.RegisterClient(context.Background(), RegisterClientRequest{RedirectUris: []string{"https://a.example/cb"}})
	require.NoError(t, err)
	assert.Equal(t, "Unnamed client", client.Name)
}

func TestRegisterClient_RateLimited(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.oauthRepo.EXPECT().CreateClient(gomock.Any(), gomock.Any()).Return(nil).Times(maxRegistrations)
	req := RegisterClientRequest{RedirectUris: []string{"https://a.example/cb"}, TokenEndpointAuthMethod: "none"}

	for range maxRegistrations {
		_, _, err := ts.handler.RegisterClient(context.Background(), req)
		require.NoError(t, err)
	}
	_, _, err := ts.handler.RegisterClient(context.Background(), req)
	status, code := oauthErrCode(t, err)
	assert.Equal(t, http.StatusTooManyRequests, status)
	assert.Equal(t, "temporarily_unavailable", code)

	ts.handler.now = func() time.Time { return testNow.Add(registrationWindow + time.Second) }
	ts.oauthRepo.EXPECT().CreateClient(gomock.Any(), gomock.Any()).Return(nil)
	_, _, err = ts.handler.RegisterClient(context.Background(), req)
	assert.NoError(t, err, "the window slides")
}

func TestListConnectedApps(t *testing.T) {
	ts := setupOAuthSuite(t)
	want := []models.ConnectedApp{{Client: publicClient(), LastActiveAt: testNow}}
	ts.oauthRepo.EXPECT().ListConnectedApps(gomock.Any(), testUserId, testNow).Return(want, nil)

	got, err := ts.handler.ListConnectedApps(userCtx)
	require.NoError(t, err)
	assert.Equal(t, want, got, "the signed-in user's apps")

	ts.oauthRepo.EXPECT().ListConnectedApps(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))
	_, err = ts.handler.ListConnectedApps(userCtx)
	assert.Same(t, pkg.InternalError, err)

	_, err = ts.handler.ListConnectedApps(context.Background())
	assert.Same(t, pkg.ErrNotSignedIn, err, "no user, no apps")
}

func TestRevokeApp(t *testing.T) {
	tests := []struct {
		name    string
		repoErr error
		wantErr error
	}{
		{name: "revoked"},
		{name: "unknown app", repoErr: repositories.ErrOAuthNotFound, wantErr: pkg.ErrAppNotFound},
		{name: "database error", repoErr: errors.New("db down"), wantErr: pkg.InternalError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			ts.oauthRepo.EXPECT().RevokeClient(gomock.Any(), testUserId, "client-1").Return(tt.repoErr)

			err := ts.handler.RevokeApp(userCtx, "client-1")
			if tt.wantErr != nil {
				assert.Same(t, tt.wantErr, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestCheckAuthorizeRequest(t *testing.T) {
	tests := []struct {
		name         string
		modify       func(r *AuthorizeRequest)
		clientErr    error
		wantCode     string
		wantRedirect bool
	}{
		{name: "valid", modify: func(r *AuthorizeRequest) {}},
		{name: "loopback redirect on another port", modify: func(r *AuthorizeRequest) { r.RedirectUri = "http://localhost:51234/callback" }},
		{name: "resource on this server", modify: func(r *AuthorizeRequest) { r.Resource = testPublicUrl + "/mcp" }},
		{name: "unknown client", modify: func(r *AuthorizeRequest) {}, clientErr: repositories.ErrOAuthNotFound, wantCode: "invalid_client"},
		{name: "unregistered redirect", modify: func(r *AuthorizeRequest) { r.RedirectUri = "https://evil.example/cb" }, wantCode: "invalid_request"},
		{name: "missing redirect", modify: func(r *AuthorizeRequest) { r.RedirectUri = "" }, wantCode: "invalid_request"},
		{name: "loopback redirect with another path", modify: func(r *AuthorizeRequest) { r.RedirectUri = "http://localhost:3000/other" }, wantCode: "invalid_request"},
		{name: "wrong response type", modify: func(r *AuthorizeRequest) { r.ResponseType = "token" }, wantCode: "unsupported_response_type", wantRedirect: true},
		{name: "no pkce", modify: func(r *AuthorizeRequest) { r.CodeChallenge = "" }, wantCode: "invalid_request", wantRedirect: true},
		{name: "plain pkce", modify: func(r *AuthorizeRequest) { r.CodeChallengeMethod = "plain" }, wantCode: "invalid_request", wantRedirect: true},
		{name: "malformed challenge", modify: func(r *AuthorizeRequest) { r.CodeChallenge = "short" }, wantCode: "invalid_request", wantRedirect: true},
		{name: "foreign resource", modify: func(r *AuthorizeRequest) { r.Resource = "https://other.example/mcp" }, wantCode: "invalid_target", wantRedirect: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			ts.oauthRepo.EXPECT().GetClient(gomock.Any(), "client-1").Return(publicClient(), tt.clientErr)

			req := validAuthorizeRequest()
			tt.modify(&req)

			_, err := ts.handler.CheckAuthorizeRequest(context.Background(), req)
			if tt.wantCode == "" {
				require.NoError(t, err)
				return
			}
			var authErr *AuthorizeError
			require.ErrorAs(t, err, &authErr)
			assert.Equal(t, tt.wantCode, authErr.Code)
			assert.Equal(t, tt.wantRedirect, authErr.Redirect, "only errors with a trusted redirect URI are redirected")
		})
	}
}

func TestCheckAuthorizeRequest_DatabaseError(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.oauthRepo.EXPECT().GetClient(gomock.Any(), gomock.Any()).Return(models.OAuthClient{}, errors.New("db down"))

	_, err := ts.handler.CheckAuthorizeRequest(context.Background(), validAuthorizeRequest())
	assert.Same(t, pkg.InternalError, err)
}

func TestRevokeApp_NeedsUser(t *testing.T) {
	ts := setupOAuthSuite(t)
	assert.Same(t, pkg.ErrNotSignedIn, ts.handler.RevokeApp(context.Background(), "client-1"))
}

func TestAuthorize_Success(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.knowsUser()
	ts.oauthRepo.EXPECT().GetClient(gomock.Any(), "client-1").Return(publicClient(), nil)

	var stored models.OAuthCode
	ts.oauthRepo.EXPECT().CreateCode(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, c models.OAuthCode) error { stored = c; return nil })

	redirect, err := ts.handler.Authorize(context.Background(), validAuthorizeRequest(), " Alice@Example.com ", testPassword)
	require.NoError(t, err, "the email is trimmed and lowercased")

	u, err := url.Parse(redirect)
	require.NoError(t, err)
	assert.Equal(t, "claude.ai", u.Host)
	assert.Equal(t, "xyz", u.Query().Get("state"))
	assert.Equal(t, testPublicUrl, u.Query().Get("iss"))

	code := u.Query().Get("code")
	require.NotEmpty(t, code)
	assert.Equal(t, hashSecret(code), stored.CodeHash, "only the code's hash is stored")
	assert.Equal(t, testChallenge, stored.CodeChallenge)
	assert.Equal(t, "https://claude.ai/api/mcp/auth_callback", stored.RedirectUri)
	assert.Equal(t, testNow.Add(authCodeTTL), stored.ExpiresAt)
	assert.Equal(t, testUserId, stored.UserId, "the code is bound to who signed in")
}

func TestAuthorize_WrongCredentials(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.knowsUser()
	ts.oauthRepo.EXPECT().GetClient(gomock.Any(), gomock.Any()).Return(publicClient(), nil).Times(2)

	_, err := ts.handler.Authorize(context.Background(), validAuthorizeRequest(), testEmail, "nope")
	assert.ErrorIs(t, err, ErrWrongPassword)
	_, err = ts.handler.Authorize(context.Background(), validAuthorizeRequest(), "nobody@example.com", testPassword)
	assert.ErrorIs(t, err, ErrWrongPassword, "an unknown email gets the same answer")
}

func TestAuthorize_LockoutAfterRepeatedFailures(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.knowsUser()
	ts.oauthRepo.EXPECT().GetClient(gomock.Any(), gomock.Any()).Return(publicClient(), nil).AnyTimes()

	for range maxFailedLogins {
		_, err := ts.handler.Authorize(context.Background(), validAuthorizeRequest(), testEmail, "nope")
		require.ErrorIs(t, err, ErrWrongPassword)
	}

	_, err := ts.handler.Authorize(context.Background(), validAuthorizeRequest(), "ALICE@example.com", testPassword)
	assert.ErrorIs(t, err, ErrTooManyLogins, "even the right password is refused while locked, in any letter case")

	// Once the window has passed, the right password works again.
	ts.handler.now = func() time.Time { return testNow.Add(failedLoginWindow + time.Second) }
	ts.oauthRepo.EXPECT().CreateCode(gomock.Any(), gomock.Any()).Return(nil)
	_, err = ts.handler.Authorize(context.Background(), validAuthorizeRequest(), testEmail, testPassword)
	assert.NoError(t, err)
}

func TestAuthorize_InvalidRequestSkipsPasswordCheck(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.oauthRepo.EXPECT().GetClient(gomock.Any(), gomock.Any()).Return(models.OAuthClient{}, repositories.ErrOAuthNotFound)

	_, err := ts.handler.Authorize(context.Background(), validAuthorizeRequest(), testEmail, testPassword)
	var authErr *AuthorizeError
	assert.ErrorAs(t, err, &authErr)
}

func TestAuthorizeErrorRedirect(t *testing.T) {
	ts := setupOAuthSuite(t)
	redirect := ts.handler.AuthorizeErrorRedirect(validAuthorizeRequest(), &AuthorizeError{Redirect: true, Code: "invalid_request", Description: "PKCE required"})

	u, err := url.Parse(redirect)
	require.NoError(t, err)
	assert.Equal(t, "invalid_request", u.Query().Get("error"))
	assert.Equal(t, "PKCE required", u.Query().Get("error_description"))
	assert.Equal(t, "xyz", u.Query().Get("state"))
}

func validCode() models.OAuthCode {
	return models.OAuthCode{
		CodeHash:      hashSecret("the-code"),
		ClientId:      "client-1",
		UserId:        testUserId,
		RedirectUri:   "https://claude.ai/api/mcp/auth_callback",
		CodeChallenge: testChallenge,
		ExpiresAt:     testNow.Add(time.Minute),
	}
}

func codeTokenRequest() TokenRequest {
	return TokenRequest{
		GrantType:    "authorization_code",
		Code:         "the-code",
		RedirectUri:  "https://claude.ai/api/mcp/auth_callback",
		CodeVerifier: testVerifier,
		ClientId:     "client-1",
	}
}

func TestToken_AuthorizationCode(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.oauthRepo.EXPECT().GetClient(gomock.Any(), "client-1").Return(publicClient(), nil)
	ts.oauthRepo.EXPECT().ConsumeCode(gomock.Any(), hashSecret("the-code")).Return(validCode(), nil)

	var issued []models.OAuthToken
	ts.oauthRepo.EXPECT().CreateTokens(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tokens ...models.OAuthToken) error { issued = tokens; return nil })
	ts.oauthRepo.EXPECT().DeleteExpired(gomock.Any(), testNow).Return(nil)

	resp, err := ts.handler.Token(context.Background(), codeTokenRequest())
	require.NoError(t, err)
	assert.Equal(t, "Bearer", resp.TokenType)
	assert.Equal(t, 3600, resp.ExpiresIn)

	require.Len(t, issued, 2)
	assert.Equal(t, models.OAuthToken{TokenHash: hashSecret(resp.AccessToken), Kind: models.OAuthTokenAccess, ClientId: "client-1", UserId: testUserId, ExpiresAt: testNow.Add(accessTokenTTL), CreatedAt: testNow}, issued[0])
	assert.Equal(t, models.OAuthToken{TokenHash: hashSecret(resp.RefreshToken), Kind: models.OAuthTokenRefresh, ClientId: "client-1", UserId: testUserId, ExpiresAt: testNow.Add(refreshTokenTTL), CreatedAt: testNow}, issued[1], "tokens belong to the code's user")
}

func TestToken_AuthorizationCode_Rejected(t *testing.T) {
	tests := []struct {
		name   string
		code   func(c *models.OAuthCode)
		req    func(r *TokenRequest)
		gone   bool // the code doesn't exist (never issued or already used)
		reason string
	}{
		{name: "unknown or reused code", gone: true},
		{name: "expired code", code: func(c *models.OAuthCode) { c.ExpiresAt = testNow }},
		{name: "issued to another client", code: func(c *models.OAuthCode) { c.ClientId = "client-2" }},
		{name: "different redirect uri", req: func(r *TokenRequest) { r.RedirectUri = "http://localhost:3000/callback" }},
		{name: "wrong code verifier", req: func(r *TokenRequest) { r.CodeVerifier = "wrong-verifier-wrong-verifier-wrong-verifier-x" }},
		{name: "missing code verifier", req: func(r *TokenRequest) { r.CodeVerifier = "" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			ts.oauthRepo.EXPECT().GetClient(gomock.Any(), "client-1").Return(publicClient(), nil)

			code := validCode()
			if tt.code != nil {
				tt.code(&code)
			}
			if tt.gone {
				ts.oauthRepo.EXPECT().ConsumeCode(gomock.Any(), gomock.Any()).Return(models.OAuthCode{}, repositories.ErrOAuthNotFound)
			} else {
				ts.oauthRepo.EXPECT().ConsumeCode(gomock.Any(), gomock.Any()).Return(code, nil)
			}

			req := codeTokenRequest()
			if tt.req != nil {
				tt.req(&req)
			}

			_, err := ts.handler.Token(context.Background(), req)
			status, errCode := oauthErrCode(t, err)
			assert.Equal(t, http.StatusBadRequest, status)
			assert.Equal(t, "invalid_grant", errCode)
		})
	}
}

func TestToken_ClientAuthentication(t *testing.T) {
	secretHash := hashSecret("s3cret")
	confidential := publicClient()
	confidential.AuthMethod = models.AuthMethodClientSecretBasic
	confidential.SecretHash = &secretHash

	tests := []struct {
		name      string
		client    models.OAuthClient
		clientErr error
		secret    string
		wantOK    bool
	}{
		{name: "confidential with right secret", client: confidential, secret: "s3cret", wantOK: true},
		{name: "confidential with wrong secret", client: confidential, secret: "nope"},
		{name: "confidential without secret", client: confidential},
		{name: "unknown client", clientErr: repositories.ErrOAuthNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			ts.oauthRepo.EXPECT().GetClient(gomock.Any(), "client-1").Return(tt.client, tt.clientErr)

			req := TokenRequest{GrantType: "unsupported", ClientId: "client-1", ClientSecret: tt.secret}
			_, err := ts.handler.Token(context.Background(), req)

			status, code := oauthErrCode(t, err)
			if tt.wantOK {
				assert.Equal(t, "unsupported_grant_type", code, "authentication passed and reached the grant check")
				return
			}
			assert.Equal(t, http.StatusUnauthorized, status)
			assert.Equal(t, "invalid_client", code)
		})
	}
}

func TestToken_RefreshToken(t *testing.T) {
	refresh := models.OAuthToken{TokenHash: hashSecret("old-refresh"), Kind: models.OAuthTokenRefresh, ClientId: "client-1", UserId: "user-7", ExpiresAt: testNow.Add(time.Hour)}
	req := TokenRequest{GrantType: "refresh_token", RefreshToken: "old-refresh", ClientId: "client-1"}

	t.Run("rotates", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.oauthRepo.EXPECT().GetClient(gomock.Any(), "client-1").Return(publicClient(), nil)
		ts.oauthRepo.EXPECT().GetToken(gomock.Any(), hashSecret("old-refresh"), models.OAuthTokenRefresh).Return(refresh, nil)
		ts.oauthRepo.EXPECT().ConsumeToken(gomock.Any(), hashSecret("old-refresh"), models.OAuthTokenRefresh).Return(refresh, nil)
		var issued []models.OAuthToken
		ts.oauthRepo.EXPECT().CreateTokens(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, tokens ...models.OAuthToken) error { issued = tokens; return nil })
		ts.oauthRepo.EXPECT().DeleteExpired(gomock.Any(), gomock.Any()).Return(nil)

		resp, err := ts.handler.Token(context.Background(), req)
		require.NoError(t, err)
		assert.NotEqual(t, "old-refresh", resp.RefreshToken)
		require.Len(t, issued, 2)
		assert.Equal(t, "user-7", issued[0].UserId, "refreshing keeps the same user")
		assert.Equal(t, "user-7", issued[1].UserId)
	})

	// None of these consume the token (no ConsumeToken expectation): a token
	// sent with the wrong client id must survive for its real owner.
	rejected := []struct {
		name  string
		token models.OAuthToken
		err   error
	}{
		{name: "unknown or already used", err: repositories.ErrOAuthNotFound},
		{name: "expired", token: func() models.OAuthToken { t := refresh; t.ExpiresAt = testNow; return t }()},
		{name: "another client's token", token: func() models.OAuthToken { t := refresh; t.ClientId = "client-2"; return t }()},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			ts.oauthRepo.EXPECT().GetClient(gomock.Any(), "client-1").Return(publicClient(), nil)
			ts.oauthRepo.EXPECT().GetToken(gomock.Any(), gomock.Any(), models.OAuthTokenRefresh).Return(tt.token, tt.err)

			_, err := ts.handler.Token(context.Background(), req)
			_, code := oauthErrCode(t, err)
			assert.Equal(t, "invalid_grant", code)
		})
	}

	t.Run("lost race with a concurrent refresh", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.oauthRepo.EXPECT().GetClient(gomock.Any(), "client-1").Return(publicClient(), nil)
		ts.oauthRepo.EXPECT().GetToken(gomock.Any(), gomock.Any(), models.OAuthTokenRefresh).Return(refresh, nil)
		ts.oauthRepo.EXPECT().ConsumeToken(gomock.Any(), gomock.Any(), models.OAuthTokenRefresh).Return(models.OAuthToken{}, repositories.ErrOAuthNotFound)

		_, err := ts.handler.Token(context.Background(), req)
		_, code := oauthErrCode(t, err)
		assert.Equal(t, "invalid_grant", code)
	})
}

func TestToken_IssueFailure(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.oauthRepo.EXPECT().GetClient(gomock.Any(), gomock.Any()).Return(publicClient(), nil)
	ts.oauthRepo.EXPECT().ConsumeCode(gomock.Any(), gomock.Any()).Return(validCode(), nil)
	ts.oauthRepo.EXPECT().CreateTokens(gomock.Any(), gomock.Any()).Return(errors.New("disk full"))

	_, err := ts.handler.Token(context.Background(), codeTokenRequest())
	status, code := oauthErrCode(t, err)
	assert.Equal(t, http.StatusInternalServerError, status)
	assert.Equal(t, "server_error", code)
}

func TestValidateAccessToken(t *testing.T) {
	tests := []struct {
		name      string
		token     models.OAuthToken
		err       error
		wantValid bool
		wantErr   bool
	}{
		{name: "valid", token: models.OAuthToken{UserId: testUserId, ExpiresAt: testNow.Add(time.Minute)}, wantValid: true},
		{name: "expired", token: models.OAuthToken{UserId: testUserId, ExpiresAt: testNow}},
		{name: "from before accounts (no user)", token: models.OAuthToken{ExpiresAt: testNow.Add(time.Minute)}},
		{name: "unknown", err: repositories.ErrOAuthNotFound},
		{name: "database error", err: errors.New("db down"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			ts.oauthRepo.EXPECT().GetToken(gomock.Any(), hashSecret("tok"), models.OAuthTokenAccess).Return(tt.token, tt.err)

			uid, valid, err := ts.handler.ValidateAccessToken(context.Background(), "tok")
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantValid, valid)
			if tt.wantValid {
				assert.Equal(t, testUserId, uid)
			} else {
				assert.Empty(t, uid)
			}
		})
	}
}

func TestPkceChallenge_MatchesIndependentComputation(t *testing.T) {
	assert.Equal(t, testChallenge, pkceChallenge(testVerifier))
}

func TestRedirectUriAllowed(t *testing.T) {
	registered := []string{"https://claude.ai/cb", "http://localhost:3000/callback", "http://127.0.0.1/cb?x=1"}

	tests := []struct {
		uri  string
		want bool
	}{
		{"https://claude.ai/cb", true},
		{"http://localhost:3000/callback", true},
		{"http://localhost:49152/callback", true},
		{"http://127.0.0.1:8080/cb?x=1", true},
		{"https://claude.ai/cb/", false},
		{"https://claude.ai:8443/cb", false},
		{"http://localhost:3000/other", false},
		{"http://127.0.0.1/cb?x=2", false},
		{"http://localhost.evil.example:3000/callback", false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, redirectUriAllowed(registered, tt.uri), tt.uri)
	}
}

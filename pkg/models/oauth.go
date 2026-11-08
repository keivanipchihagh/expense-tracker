package models

import "time"

// OAuth client authentication methods at the token endpoint (RFC 7591).
const (
	AuthMethodNone              = "none" // public client, proves itself with PKCE only
	AuthMethodClientSecretPost  = "client_secret_post"
	AuthMethodClientSecretBasic = "client_secret_basic"
)

// OAuthClient is an application registered through dynamic client
// registration, e.g. ChatGPT, Claude or an MCP client on your machine.
type OAuthClient struct {
	Id string
	// SecretHash is the SHA-256 of the client secret, nil for public clients.
	SecretHash   *string
	Name         string
	RedirectUris []string
	AuthMethod   string
	CreatedAt    time.Time
}

// ConnectedApp is a registered client that currently holds a valid token.
type ConnectedApp struct {
	Client OAuthClient
	// LastActiveAt is when it last received a token (sign-in or refresh).
	LastActiveAt time.Time
}

// OAuthCode is a one-time authorization code waiting to be exchanged for
// tokens. Only its hash is stored.
type OAuthCode struct {
	CodeHash    string
	ClientId    string
	RedirectUri string
	// CodeChallenge is the PKCE S256 challenge sent with the authorization request.
	CodeChallenge string
	ExpiresAt     time.Time
}

// OAuthTokenKind distinguishes access tokens from refresh tokens.
type OAuthTokenKind string

const (
	OAuthTokenAccess  OAuthTokenKind = "access"
	OAuthTokenRefresh OAuthTokenKind = "refresh"
)

// OAuthToken is an issued access or refresh token. Only its hash is stored.
type OAuthToken struct {
	TokenHash string
	Kind      OAuthTokenKind
	ClientId  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

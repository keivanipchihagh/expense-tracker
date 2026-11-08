package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestLogin_Success(t *testing.T) {
	ts := setupOAuthSuite(t)

	var stored models.Session
	ts.sessionRepo.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, s models.Session) error { stored = s; return nil })
	ts.sessionRepo.EXPECT().DeleteExpired(gomock.Any(), testNow).Return(nil)

	token, expiresAt, err := ts.handler.Login(context.Background(), testPassword)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, hashSecret(token), stored.TokenHash, "only the hash is stored")
	assert.Equal(t, testNow.Add(sessionTTL), expiresAt)
	assert.Equal(t, expiresAt, stored.ExpiresAt)
}

func TestLogin_WrongPasswordAndLockout(t *testing.T) {
	ts := setupOAuthSuite(t)

	for range maxFailedLogins {
		_, _, err := ts.handler.Login(context.Background(), "nope")
		require.ErrorIs(t, err, ErrWrongPassword)
	}
	_, _, err := ts.handler.Login(context.Background(), testPassword)
	assert.ErrorIs(t, err, ErrTooManyLogins)
}

func TestLogin_SharesLockoutWithOAuth(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.oauthRepo.EXPECT().GetClient(gomock.Any(), gomock.Any()).Return(publicClient(), nil).AnyTimes()

	for range maxFailedLogins {
		_, err := ts.handler.Authorize(context.Background(), validAuthorizeRequest(), "nope")
		require.ErrorIs(t, err, ErrWrongPassword)
	}
	_, _, err := ts.handler.Login(context.Background(), testPassword)
	assert.ErrorIs(t, err, ErrTooManyLogins, "guessing on one login page locks the other too")
}

func TestLogin_RepositoryError(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.sessionRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("disk full"))

	_, _, err := ts.handler.Login(context.Background(), testPassword)
	assert.Same(t, pkg.InternalError, err)
}

func TestValidateSession(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		session   models.Session
		err       error
		wantValid bool
		wantErr   bool
		noLookup  bool
	}{
		{name: "valid", token: "tok", session: models.Session{ExpiresAt: testNow.Add(time.Hour)}, wantValid: true},
		{name: "expired", token: "tok", session: models.Session{ExpiresAt: testNow}},
		{name: "unknown", token: "tok", err: repositories.ErrSessionNotFound},
		{name: "empty token", token: "", noLookup: true},
		{name: "database error", token: "tok", err: errors.New("db down"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			if !tt.noLookup {
				ts.sessionRepo.EXPECT().Get(gomock.Any(), hashSecret(tt.token)).Return(tt.session, tt.err)
			}

			valid, err := ts.handler.ValidateSession(context.Background(), tt.token)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantValid, valid)
		})
	}
}

func TestLogout(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.sessionRepo.EXPECT().Delete(gomock.Any(), hashSecret("tok")).Return(nil)
	require.NoError(t, ts.handler.Logout(context.Background(), "tok"))

	ts.sessionRepo.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(errors.New("db down"))
	assert.Same(t, pkg.InternalError, ts.handler.Logout(context.Background(), "tok"))
}

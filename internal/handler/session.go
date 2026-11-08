package handler

import (
	"context"
	"errors"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

// sessionTTL is how long a UI sign-in lasts.
const sessionTTL = 30 * 24 * time.Hour

// Login checks the password and starts a UI session. It returns the session
// token for the cookie and when it expires. Failed attempts count towards
// the same lockout as the OAuth login page.
func (h *Handler) Login(ctx context.Context, password string) (string, time.Time, error) {
	now := h.now()
	if h.logins.locked(now) {
		return "", time.Time{}, ErrTooManyLogins
	}
	if !secretsEqual(password, h.password) {
		h.logins.fail(now)
		h.logger.WarnContext(ctx, "ui login failed: wrong password")
		return "", time.Time{}, ErrWrongPassword
	}

	token := randomToken()
	expiresAt := now.Add(sessionTTL).UTC()
	err := h.sessionRepo.Create(ctx, models.Session{TokenHash: hashSecret(token), ExpiresAt: expiresAt, CreatedAt: now.UTC()})
	if err != nil {
		h.logger.ErrorContext(ctx, "create session failed", "error", err)
		return "", time.Time{}, pkg.InternalError
	}

	// Expired sessions are pruned on every sign-in; failure is harmless.
	if err := h.sessionRepo.DeleteExpired(ctx, now); err != nil {
		h.logger.WarnContext(ctx, "delete expired sessions failed", "error", err)
	}

	h.logger.InfoContext(ctx, "ui login succeeded")
	return token, expiresAt, nil
}

// ValidateSession reports whether token belongs to a current session. An
// error means the check itself failed.
func (h *Handler) ValidateSession(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	s, err := h.sessionRepo.Get(ctx, hashSecret(token))
	if errors.Is(err, repositories.ErrSessionNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return h.now().Before(s.ExpiresAt), nil
}

// Logout ends the session with the given token.
func (h *Handler) Logout(ctx context.Context, token string) error {
	if err := h.sessionRepo.Delete(ctx, hashSecret(token)); err != nil {
		h.logger.ErrorContext(ctx, "delete session failed", "error", err)
		return pkg.InternalError
	}
	h.logger.InfoContext(ctx, "ui logout")
	return nil
}

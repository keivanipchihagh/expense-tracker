package handler

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	expenseRepo repositories.ExpenseRepository
	oauthRepo   repositories.OAuthRepository
	sessionRepo repositories.SessionRepository
	userRepo    repositories.UserRepository
	// publicUrl is the server's base URL, used as the OAuth issuer.
	publicUrl string
	// signupEnabled allows new accounts to be created.
	signupEnabled bool
	// logins limits wrong passwords per email; registrations limits new
	// OAuth clients and signups new accounts.
	logins        *keyedLimiter
	registrations *rateLimiter
	signups       *rateLimiter
	// bcryptCost is the cost of new password hashes; tests lower it.
	bcryptCost int
	now        func() time.Time
	logger     *slog.Logger
}

func NewHandler(
	expenseRepo repositories.ExpenseRepository,
	oauthRepo repositories.OAuthRepository,
	sessionRepo repositories.SessionRepository,
	userRepo repositories.UserRepository,
	publicUrl string,
	signupEnabled bool,
) *Handler {
	if expenseRepo == nil || oauthRepo == nil || sessionRepo == nil || userRepo == nil {
		panic("handler: repositories must not be nil")
	}
	if publicUrl == "" {
		panic("handler: public url must not be empty")
	}
	return &Handler{
		expenseRepo:   expenseRepo,
		oauthRepo:     oauthRepo,
		sessionRepo:   sessionRepo,
		userRepo:      userRepo,
		publicUrl:     strings.TrimSuffix(publicUrl, "/"),
		signupEnabled: signupEnabled,
		logins:        newKeyedLimiter(maxFailedLogins, failedLoginWindow),
		registrations: newRateLimiter(maxRegistrations, registrationWindow),
		signups:       newRateLimiter(maxSignups, signupWindow),
		bcryptCost:    bcrypt.DefaultCost,
		now:           time.Now,
		logger:        slog.Default().With("module", "handler"),
	}
}

// SetBcryptCost lowers the password hashing cost, for tests only.
func (h *Handler) SetBcryptCost(cost int) { h.bcryptCost = cost }

// SignupEnabled reports whether new accounts can be created.
func (h *Handler) SignupEnabled() bool { return h.signupEnabled }

// userId returns the signed-in user's id from the context.
func userId(ctx context.Context) (string, error) {
	id := pkg.UserIdFrom(ctx)
	if id == "" {
		return "", pkg.ErrNotSignedIn
	}
	return id, nil
}

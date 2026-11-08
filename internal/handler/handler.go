package handler

import (
	"log/slog"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

type Handler struct {
	expenseRepo repositories.ExpenseRepository
	oauthRepo   repositories.OAuthRepository
	sessionRepo repositories.SessionRepository
	// publicUrl is the server's base URL, used as the OAuth issuer.
	publicUrl string
	// password unlocks the OAuth login page and the UI.
	password string
	logins   *loginLimiter
	now      func() time.Time
	logger   *slog.Logger
}

func NewHandler(
	expenseRepo repositories.ExpenseRepository,
	oauthRepo repositories.OAuthRepository,
	sessionRepo repositories.SessionRepository,
	publicUrl, password string,
) *Handler {
	if expenseRepo == nil || oauthRepo == nil || sessionRepo == nil {
		panic("handler: repositories must not be nil")
	}
	if publicUrl == "" || password == "" {
		panic("handler: public url and password must not be empty")
	}
	return &Handler{
		expenseRepo: expenseRepo,
		oauthRepo:   oauthRepo,
		sessionRepo: sessionRepo,
		publicUrl:   strings.TrimSuffix(publicUrl, "/"),
		password:    password,
		logins:      newLoginLimiter(),
		now:         time.Now,
		logger:      slog.Default().With("module", "handler"),
	}
}

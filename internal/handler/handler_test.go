package handler

import (
	"testing"

	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// HandlerTestSuite holds a handler wired to mock repositories.
type HandlerTestSuite struct {
	handler     *Handler
	expenseRepo *repositories.MockExpenseRepository
	oauthRepo   *repositories.MockOAuthRepository
	sessionRepo *repositories.MockSessionRepository
	settingRepo *repositories.MockSettingRepository
}

const (
	testPublicUrl = "https://expenses.example.com"
	testPassword  = "correct-horse-battery-staple"
)

func setupHandlerTestSuite(t *testing.T) *HandlerTestSuite {
	t.Helper()
	ctrl := gomock.NewController(t)
	expenseRepo := repositories.NewMockExpenseRepository(ctrl)
	oauthRepo := repositories.NewMockOAuthRepository(ctrl)
	sessionRepo := repositories.NewMockSessionRepository(ctrl)
	settingRepo := repositories.NewMockSettingRepository(ctrl)

	return &HandlerTestSuite{
		handler:     NewHandler(expenseRepo, oauthRepo, sessionRepo, settingRepo, testPublicUrl, testPassword),
		expenseRepo: expenseRepo,
		oauthRepo:   oauthRepo,
		sessionRepo: sessionRepo,
		settingRepo: settingRepo,
	}
}

func TestNewHandler_Panics(t *testing.T) {
	ctrl := gomock.NewController(t)
	expenseRepo := repositories.NewMockExpenseRepository(ctrl)
	oauthRepo := repositories.NewMockOAuthRepository(ctrl)
	sessionRepo := repositories.NewMockSessionRepository(ctrl)
	settingRepo := repositories.NewMockSettingRepository(ctrl)

	assert.Panics(t, func() { NewHandler(nil, oauthRepo, sessionRepo, settingRepo, testPublicUrl, testPassword) })
	assert.Panics(t, func() { NewHandler(expenseRepo, nil, sessionRepo, settingRepo, testPublicUrl, testPassword) })
	assert.Panics(t, func() { NewHandler(expenseRepo, oauthRepo, nil, settingRepo, testPublicUrl, testPassword) })
	assert.Panics(t, func() { NewHandler(expenseRepo, oauthRepo, sessionRepo, nil, testPublicUrl, testPassword) })
	assert.Panics(t, func() { NewHandler(expenseRepo, oauthRepo, sessionRepo, settingRepo, "", testPassword) })
	assert.Panics(t, func() { NewHandler(expenseRepo, oauthRepo, sessionRepo, settingRepo, testPublicUrl, "") })
}

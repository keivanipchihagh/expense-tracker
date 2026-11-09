package handler

import (
	"context"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"golang.org/x/crypto/bcrypt"

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
	userRepo    *repositories.MockUserRepository
	categories  *repositories.MockCategoryRepository
}

// defaultUsage is the default categories, none in use: what the test user
// has unless a test says otherwise.
func defaultUsage() []models.CategoryUsage {
	out := make([]models.CategoryUsage, len(models.DefaultCategories))
	for i, c := range models.DefaultCategories {
		out[i] = models.CategoryUsage{Name: c}
	}
	return out
}

const (
	testPublicUrl = "https://expenses.example.com"
	testUserId    = "user-1"
	testEmail     = "alice@example.com"
	testPassword  = "correct-horse-battery-staple"
)

// testHash is testPassword hashed at the cheapest cost, to keep tests fast.
var testHash, _ = bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)

// userCtx is a request context signed in as the test user.
var userCtx = pkg.WithUserId(context.Background(), testUserId)

func testUser() models.User {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	return models.User{Id: testUserId, Email: testEmail, FirstName: "Alice", LastName: "Liddell", PasswordHash: string(testHash), Currency: models.CurrencyEUR, CreatedAt: at, UpdatedAt: at}
}

func setupHandlerTestSuite(t *testing.T) *HandlerTestSuite {
	t.Helper()
	ctrl := gomock.NewController(t)
	expenseRepo := repositories.NewMockExpenseRepository(ctrl)
	oauthRepo := repositories.NewMockOAuthRepository(ctrl)
	sessionRepo := repositories.NewMockSessionRepository(ctrl)
	userRepo := repositories.NewMockUserRepository(ctrl)
	categories := repositories.NewMockCategoryRepository(ctrl)
	// The test user has the default categories; tests about categories
	// use their own controller.
	categories.EXPECT().List(gomock.Any(), gomock.Any()).Return(defaultUsage(), nil).AnyTimes()
	categories.EXPECT().Seed(gomock.Any(), gomock.Any(), models.DefaultCategories, gomock.Any()).Return(nil).AnyTimes()

	h := NewHandler(expenseRepo, oauthRepo, sessionRepo, userRepo, categories, testPublicUrl, true)
	h.SetBcryptCost(bcrypt.MinCost)
	return &HandlerTestSuite{
		handler:     h,
		expenseRepo: expenseRepo,
		oauthRepo:   oauthRepo,
		sessionRepo: sessionRepo,
		userRepo:    userRepo,
		categories:  categories,
	}
}

// knowsUser makes the test user findable by email (and nobody else).
func (ts *HandlerTestSuite) knowsUser() {
	ts.userRepo.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, email string) (models.User, error) {
		if email == testEmail {
			return testUser(), nil
		}
		return models.User{}, repositories.ErrUserNotFound
	}).AnyTimes()
}

func TestNewHandler_Panics(t *testing.T) {
	ctrl := gomock.NewController(t)
	expenseRepo := repositories.NewMockExpenseRepository(ctrl)
	oauthRepo := repositories.NewMockOAuthRepository(ctrl)
	sessionRepo := repositories.NewMockSessionRepository(ctrl)
	userRepo := repositories.NewMockUserRepository(ctrl)

	categories := repositories.NewMockCategoryRepository(ctrl)
	assert.Panics(t, func() { NewHandler(nil, oauthRepo, sessionRepo, userRepo, categories, testPublicUrl, true) })
	assert.Panics(t, func() { NewHandler(expenseRepo, nil, sessionRepo, userRepo, categories, testPublicUrl, true) })
	assert.Panics(t, func() { NewHandler(expenseRepo, oauthRepo, nil, userRepo, categories, testPublicUrl, true) })
	assert.Panics(t, func() { NewHandler(expenseRepo, oauthRepo, sessionRepo, nil, categories, testPublicUrl, true) })
	assert.Panics(t, func() { NewHandler(expenseRepo, oauthRepo, sessionRepo, userRepo, nil, testPublicUrl, true) })
	assert.Panics(t, func() { NewHandler(expenseRepo, oauthRepo, sessionRepo, userRepo, categories, "", true) })
}

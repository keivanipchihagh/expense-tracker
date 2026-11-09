package api

import (
	"net/http"
	"testing"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// testUserId is who the test routes act as.
const testUserId = "user-1"

// newTestRoutes returns the API routes backed by a handler with a mock
// expense repository, which the test sets expectations on. Requests act as
// testUserId, as if the auth middleware had let them through.
func newTestRoutes(t *testing.T) (http.Handler, *repositories.MockExpenseRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := repositories.NewMockExpenseRepository(ctrl)
	categories := repositories.NewMockCategoryRepository(ctrl)
	usage := make([]models.CategoryUsage, len(models.DefaultCategories))
	for i, c := range models.DefaultCategories {
		usage[i] = models.CategoryUsage{Name: c}
	}
	categories.EXPECT().List(gomock.Any(), gomock.Any()).Return(usage, nil).AnyTimes()
	h := handler.NewHandler(repo, repositories.NewMockOAuthRepository(ctrl), repositories.NewMockSessionRepository(ctrl), repositories.NewMockUserRepository(ctrl), categories, testPublicUrl, true)
	routes := NewServer(h, "v1.2.3", testPublicUrl).Routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routes.ServeHTTP(w, r.WithContext(pkg.WithUserId(r.Context(), testUserId)))
	}), repo
}

const testPublicUrl = "https://expenses.example.com"

func TestNewServer_Panics(t *testing.T) {
	ctrl := gomock.NewController(t)
	h := handler.NewHandler(repositories.NewMockExpenseRepository(ctrl), repositories.NewMockOAuthRepository(ctrl), repositories.NewMockSessionRepository(ctrl), repositories.NewMockUserRepository(ctrl), repositories.NewMockCategoryRepository(ctrl), testPublicUrl, true)

	assert.Panics(t, func() { NewServer(nil, "v1.2.3", testPublicUrl) }, "nil handler")
	assert.Panics(t, func() { NewServer(h, "", testPublicUrl) }, "empty version")
	assert.Panics(t, func() { NewServer(h, "v1.2.3", "") }, "empty public url")
}

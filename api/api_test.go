package api

import (
	"net/http"
	"testing"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// newTestRoutes returns the API routes backed by a handler with a mock
// expense repository, which the test sets expectations on.
func newTestRoutes(t *testing.T) (http.Handler, *repositories.MockExpenseRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := repositories.NewMockExpenseRepository(ctrl)
	h := handler.NewHandler(repo, repositories.NewMockOAuthRepository(ctrl), testPublicUrl, "correct-horse-battery-staple")
	return NewServer(h, "v1.2.3", testPublicUrl).Routes(), repo
}

const testPublicUrl = "https://expenses.example.com"

func TestNewServer_Panics(t *testing.T) {
	ctrl := gomock.NewController(t)
	h := handler.NewHandler(repositories.NewMockExpenseRepository(ctrl), repositories.NewMockOAuthRepository(ctrl), testPublicUrl, "correct-horse-battery-staple")

	assert.Panics(t, func() { NewServer(nil, "v1.2.3", testPublicUrl) }, "nil handler")
	assert.Panics(t, func() { NewServer(h, "", testPublicUrl) }, "empty version")
	assert.Panics(t, func() { NewServer(h, "v1.2.3", "") }, "empty public url")
}

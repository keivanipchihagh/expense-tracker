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
	return NewServer(handler.NewHandler(repo), "v1.2.3").Routes(), repo
}

func TestNewServer_Panics(t *testing.T) {
	ctrl := gomock.NewController(t)
	h := handler.NewHandler(repositories.NewMockExpenseRepository(ctrl))

	assert.Panics(t, func() { NewServer(nil, "v1.2.3") }, "nil handler")
	assert.Panics(t, func() { NewServer(h, "") }, "empty version")
}

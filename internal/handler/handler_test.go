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
}

func setupHandlerTestSuite(t *testing.T) *HandlerTestSuite {
	t.Helper()
	ctrl := gomock.NewController(t)
	expenseRepo := repositories.NewMockExpenseRepository(ctrl)

	return &HandlerTestSuite{
		handler:     NewHandler(expenseRepo),
		expenseRepo: expenseRepo,
	}
}

func TestNewHandler_PanicsOnNilRepository(t *testing.T) {
	assert.Panics(t, func() { NewHandler(nil) })
}

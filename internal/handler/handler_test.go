package handler

import (
	"testing"

	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// HandlerTestSuite holds a handler wired to mock repositories.
type HandlerTestSuite struct {
	handler *Handler
	logRepo *repositories.MockLogRepository
}

func setupHandlerTestSuite(t *testing.T) *HandlerTestSuite {
	t.Helper()
	ctrl := gomock.NewController(t)
	logRepo := repositories.NewMockLogRepository(ctrl)

	return &HandlerTestSuite{
		handler: NewHandler(logRepo),
		logRepo: logRepo,
	}
}

func TestNewHandler_PanicsOnNilRepository(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	NewHandler(nil)
}

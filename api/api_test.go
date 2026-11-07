package api

import (
	"net/http"
	"testing"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

// newTestRoutes returns the API routes backed by a handler with mock repositories.
func newTestRoutes(t *testing.T) http.Handler {
	t.Helper()
	ctrl := gomock.NewController(t)
	h := handler.NewHandler(repositories.NewMockLogRepository(ctrl))
	return NewServer(h, "v1.2.3").Routes()
}

func TestNewServer_Panics(t *testing.T) {
	ctrl := gomock.NewController(t)
	h := handler.NewHandler(repositories.NewMockLogRepository(ctrl))

	assert.Panics(t, func() { NewServer(nil, "v1.2.3") }, "nil handler")
	assert.Panics(t, func() { NewServer(h, "") }, "empty version")
}

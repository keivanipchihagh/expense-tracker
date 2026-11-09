package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestExportData(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
	expense := models.Expense{Id: "e1", UserId: testUserId, Amount: "12.50", Category: models.CategoryFood}
	ts.expenseRepo.EXPECT().List(gomock.Any(), testUserId, models.ExpenseFilter{}).Return([]models.Expense{expense}, nil)
	ts.expenseRepo.EXPECT().History(gomock.Any(), testUserId).Return([]models.Log{
		{ExpenseId: "e1", UserId: testUserId, Action: models.LogActionCreate, After: json.RawMessage(`{"id":"e1"}`), CreatedAt: testNow},
	}, nil)
	connected := testNow.Add(-time.Hour)
	ts.oauthRepo.EXPECT().ListConnectedApps(gomock.Any(), testUserId, testNow).Return([]models.ConnectedApp{
		{Client: models.OAuthClient{Id: "c1", Name: "Claude", CreatedAt: connected}, LastActiveAt: testNow},
	}, nil)

	data, err := ts.handler.ExportData(userCtx)
	require.NoError(t, err)
	assert.Equal(t, testNow.UTC(), data.ExportedAt)
	assert.Equal(t, testEmail, data.Account.Email)
	assert.Equal(t, []models.Expense{expense}, data.Expenses)
	assert.Equal(t, []HistoryEntry{{ExpenseId: "e1", Action: "create", After: json.RawMessage(`{"id":"e1"}`), At: testNow}}, data.History)
	assert.Equal(t, []ExportedApp{{Name: "Claude", ConnectedAt: connected, LastActiveAt: testNow}}, data.ConnectedApps)

	b, err := json.Marshal(data)
	require.NoError(t, err)
	assert.NotContains(t, string(b), string(testHash), "the password hash is never exported")
	assert.NotContains(t, string(b), "c1", "nor internal client ids")
}

func TestExportData_Errors(t *testing.T) {
	_, err := setupOAuthSuite(t).handler.ExportData(context.Background())
	assert.Same(t, pkg.ErrNotSignedIn, err)

	ts := setupOAuthSuite(t)
	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
	ts.expenseRepo.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))
	_, err = ts.handler.ExportData(userCtx)
	assert.Same(t, pkg.InternalError, err)
}

func TestDeleteAccount(t *testing.T) {
	t.Run("wrong password deletes nothing", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		err := ts.handler.DeleteAccount(userCtx, "not-the-password")
		assert.Equal(t, map[string]string{"password": "is wrong"}, validationFields(t, err))
	})
	t.Run("right password deletes the account", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		ts.userRepo.EXPECT().Delete(gomock.Any(), testUserId).Return(nil)
		assert.NoError(t, ts.handler.DeleteAccount(userCtx, testPassword))
	})
	t.Run("signed out", func(t *testing.T) {
		assert.Same(t, pkg.ErrNotSignedIn, setupOAuthSuite(t).handler.DeleteAccount(context.Background(), testPassword))
	})
	t.Run("already gone", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		ts.userRepo.EXPECT().Delete(gomock.Any(), testUserId).Return(repositories.ErrUserNotFound)
		assert.Same(t, pkg.ErrNotSignedIn, ts.handler.DeleteAccount(userCtx, testPassword))
	})
	t.Run("database error", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		ts.userRepo.EXPECT().Delete(gomock.Any(), testUserId).Return(errors.New("disk full"))
		assert.Same(t, pkg.InternalError, ts.handler.DeleteAccount(userCtx, testPassword))
	})
}

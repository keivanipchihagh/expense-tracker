package handler

import (
	"context"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateExpense(t *testing.T) {
	ts := setupHandlerTestSuite(t)

	expense, err := ts.handler.CreateExpense(context.Background(), map[string]any{"amount": "12.50"})
	require.NoError(t, err)
	assert.Equal(t, models.Expense{}, expense)
}

func TestListExpenses(t *testing.T) {
	tests := []struct {
		name     string
		dateFrom time.Time
		dateTo   time.Time
	}{
		{name: "open range"},
		{name: "bounded range", dateFrom: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), dateTo: time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupHandlerTestSuite(t)

			expenses, err := ts.handler.ListExpenses(context.Background(), tt.dateFrom, tt.dateTo)
			require.NoError(t, err)
			assert.NotNil(t, expenses)
			assert.Empty(t, expenses)
		})
	}
}

func TestGetExpense(t *testing.T) {
	ts := setupHandlerTestSuite(t)

	expense, err := ts.handler.GetExpense(context.Background(), "e1")
	require.NoError(t, err)
	assert.Equal(t, models.Expense{}, expense)
}

func TestUpdateExpense(t *testing.T) {
	ts := setupHandlerTestSuite(t)

	expense, err := ts.handler.UpdateExpense(context.Background(), "e1", map[string]any{"merchant": nil})
	require.NoError(t, err)
	assert.Equal(t, models.Expense{}, expense)
}

func TestDeleteExpense(t *testing.T) {
	ts := setupHandlerTestSuite(t)

	require.NoError(t, ts.handler.DeleteExpense(context.Background(), "e1"))
}

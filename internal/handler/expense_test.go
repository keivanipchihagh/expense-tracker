package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

var errDatabase = errors.New("database is on fire")

func validInput() map[string]any {
	return map[string]any{
		"amount":      json.Number("12.5"),
		"occurred_at": "2026-11-07T13:20:00+01:00",
		"category":    "food",
	}
}

func storedExpense() models.Expense {
	merchant, note := "Rewe", "with the team"
	created := time.Date(2026, 11, 7, 12, 21, 0, 0, time.UTC)
	return models.Expense{
		Id:         "0193f2a1-0000-7000-8000-000000000001",
		UserId:     testUserId,
		Amount:     "12.50",
		OccurredAt: time.Date(2026, 11, 7, 13, 20, 0, 0, time.FixedZone("", 3600)),
		Category:   models.CategoryFood,
		Merchant:   &merchant,
		Note:       &note,
		CreatedAt:  created,
		UpdatedAt:  created,
	}
}

func validationFields(t *testing.T, err error) map[string]string {
	t.Helper()
	var appErr *pkg.Error
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, "validation_failed", appErr.Code)
	return appErr.Fields
}

func TestCreateExpense_Success(t *testing.T) {
	ts := setupHandlerTestSuite(t)

	input := validInput()
	input["merchant"] = "  Rewe  "
	input["note"] = "with the team"

	var stored models.Expense
	ts.expenseRepo.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, e models.Expense) error { stored = e; return nil })

	got, err := ts.handler.CreateExpense(userCtx, input)
	require.NoError(t, err)
	assert.Equal(t, stored, got, "returns exactly what was stored")

	id, err := uuid.Parse(got.Id)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), id.Version())

	assert.Equal(t, "12.50", got.Amount, "amount is canonicalized to two decimals")
	assert.Equal(t, "2026-11-07T13:20:00+01:00", got.OccurredAt.Format(time.RFC3339), "offset is kept")
	assert.Equal(t, models.CategoryFood, got.Category)
	assert.Equal(t, "Rewe", *got.Merchant, "text is trimmed")
	assert.Equal(t, "with the team", *got.Note)
	assert.Equal(t, got.CreatedAt, got.UpdatedAt)
	assert.Equal(t, time.UTC, got.CreatedAt.Location())
}

func TestCreateExpense_OptionalFieldsDefaultToNil(t *testing.T) {
	ts := setupHandlerTestSuite(t)
	ts.expenseRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	input := validInput()
	input["merchant"] = nil
	input["note"] = "   "

	got, err := ts.handler.CreateExpense(userCtx, input)
	require.NoError(t, err)
	assert.Nil(t, got.Merchant, "null means no value")
	assert.Nil(t, got.Note, "a blank string means no value")
}

func TestCreateExpense_Validation(t *testing.T) {
	tests := []struct {
		name   string
		modify func(in map[string]any)
		want   map[string]string
	}{
		{
			name:   "all required fields missing",
			modify: func(in map[string]any) { clear(in) },
			want: map[string]string{
				"amount": "is required", "occurred_at": "is required", "category": "is required",
			},
		},
		{name: "required field null", modify: func(in map[string]any) { in["category"] = nil }, want: map[string]string{"category": "is required"}},
		{name: "unknown field", modify: func(in map[string]any) { in["tip"] = "2" }, want: map[string]string{"tip": "unknown field"}},
		{name: "server-set field", modify: func(in map[string]any) { in["id"] = "x" }, want: map[string]string{"id": "unknown field"}},
		{name: "currency is no longer a field", modify: func(in map[string]any) { in["currency"] = "EUR" }, want: map[string]string{"currency": "unknown field"}},
		{name: "too many decimals", modify: func(in map[string]any) { in["amount"] = "1.234" }, want: map[string]string{"amount": "must have at most 2 decimal places"}},
		{name: "negative amount", modify: func(in map[string]any) { in["amount"] = json.Number("-3") }, want: map[string]string{"amount": "must be greater than zero"}},
		{name: "amount not a number", modify: func(in map[string]any) { in["amount"] = true }, want: map[string]string{"amount": `must be a decimal number or string, e.g. "12.50"`}},
		{name: "naive timestamp", modify: func(in map[string]any) { in["occurred_at"] = "2026-11-07T13:20:00" }, want: map[string]string{"occurred_at": "must be an RFC 3339 timestamp with a UTC offset, e.g. 2026-11-07T13:20:00+01:00"}},
		{name: "timestamp not a string", modify: func(in map[string]any) { in["occurred_at"] = json.Number("1") }, want: map[string]string{"occurred_at": "must be an RFC 3339 timestamp with a UTC offset, e.g. 2026-11-07T13:20:00+01:00"}},
		{name: "unknown category", modify: func(in map[string]any) { in["category"] = "rent" }, want: map[string]string{"category": "must be one of " + joinValues(models.Categories)}},
		{name: "merchant not a string", modify: func(in map[string]any) { in["merchant"] = json.Number("5") }, want: map[string]string{"merchant": "must be a string"}},
		{name: "merchant too long", modify: func(in map[string]any) { in["merchant"] = strings.Repeat("é", models.MaxMerchantLength+1) }, want: map[string]string{"merchant": "must be at most 200 characters"}},
		{name: "note too long", modify: func(in map[string]any) { in["note"] = strings.Repeat("x", models.MaxNoteLength+1) }, want: map[string]string{"note": "must be at most 2000 characters"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupHandlerTestSuite(t) // no repository call expected

			input := validInput()
			tt.modify(input)

			_, err := ts.handler.CreateExpense(userCtx, input)
			assert.Equal(t, tt.want, validationFields(t, err))
		})
	}
}

func TestCreateExpense_RepositoryError(t *testing.T) {
	ts := setupHandlerTestSuite(t)
	ts.expenseRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errDatabase)

	_, err := ts.handler.CreateExpense(userCtx, validInput())
	assert.Same(t, pkg.InternalError, err, "internal details are not exposed")
}

func TestGetExpense(t *testing.T) {
	tests := []struct {
		name    string
		repoErr error
		wantErr error
	}{
		{name: "found"},
		{name: "not found", repoErr: repositories.ErrExpenseNotFound, wantErr: pkg.ErrExpenseNotFound},
		{name: "database error", repoErr: errDatabase, wantErr: pkg.InternalError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupHandlerTestSuite(t)
			stored := storedExpense()
			if tt.repoErr != nil {
				stored = models.Expense{}
			}
			ts.expenseRepo.EXPECT().Get(gomock.Any(), testUserId, "id-1").Return(stored, tt.repoErr)

			got, err := ts.handler.GetExpense(userCtx, "id-1")
			if tt.wantErr != nil {
				assert.Same(t, tt.wantErr, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, stored, got)
		})
	}
}

func TestListExpenses(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)

	t.Run("passes the filter through", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		want := []models.Expense{storedExpense()}
		ts.expenseRepo.EXPECT().List(gomock.Any(), testUserId, models.ExpenseFilter{
			DateFrom: from, DateTo: to, Category: models.CategoryGroceries, Merchant: "rewe",
		}).Return(want, nil)

		got, err := ts.handler.ListExpenses(userCtx, models.ExpenseFilter{
			DateFrom: from, DateTo: to, Category: models.CategoryGroceries, Merchant: "  rewe ",
		})
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("same day is allowed", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		ts.expenseRepo.EXPECT().List(gomock.Any(), testUserId, models.ExpenseFilter{DateFrom: from, DateTo: from}).Return([]models.Expense{}, nil)

		_, err := ts.handler.ListExpenses(userCtx, models.ExpenseFilter{DateFrom: from, DateTo: from})
		require.NoError(t, err)
	})

	t.Run("blank merchant doesn't filter", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		ts.expenseRepo.EXPECT().List(gomock.Any(), testUserId, models.ExpenseFilter{}).Return([]models.Expense{}, nil)

		_, err := ts.handler.ListExpenses(userCtx, models.ExpenseFilter{Merchant: "   "})
		require.NoError(t, err)
	})

	t.Run("invalid filters", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)

		_, err := ts.handler.ListExpenses(userCtx, models.ExpenseFilter{
			DateFrom: to, DateTo: from, Category: "pets", Merchant: strings.Repeat("x", models.MaxMerchantLength+1),
		})
		assert.Equal(t, map[string]string{
			"date_to":  "must not be before date_from",
			"category": "must be one of " + joinValues(models.Categories),
			"merchant": "must be at most 200 characters",
		}, validationFields(t, err))
	})

	t.Run("database error", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		ts.expenseRepo.EXPECT().List(gomock.Any(), testUserId, gomock.Any()).Return(nil, errDatabase)

		_, err := ts.handler.ListExpenses(userCtx, models.ExpenseFilter{})
		assert.Same(t, pkg.InternalError, err)
	})
}

func TestUpdateExpense_Success(t *testing.T) {
	tests := []struct {
		name   string
		patch  map[string]any
		verify func(t *testing.T, before, after models.Expense)
	}{
		{
			name:  "changes only the sent fields",
			patch: map[string]any{"amount": "20", "category": "groceries"},
			verify: func(t *testing.T, before, after models.Expense) {
				assert.Equal(t, "20.00", after.Amount)
				assert.Equal(t, models.CategoryGroceries, after.Category)
				assert.Equal(t, before.Merchant, after.Merchant)
				assert.Equal(t, before.Note, after.Note)
				assert.Equal(t, before.OccurredAt, after.OccurredAt)
			},
		},
		{
			name:  "null clears optional fields",
			patch: map[string]any{"merchant": nil, "note": nil},
			verify: func(t *testing.T, _, after models.Expense) {
				assert.Nil(t, after.Merchant)
				assert.Nil(t, after.Note)
			},
		},
		{
			name:  "new occurred_at keeps its own offset",
			patch: map[string]any{"occurred_at": "2026-11-08T09:00:00-05:00"},
			verify: func(t *testing.T, _, after models.Expense) {
				assert.Equal(t, "2026-11-08T09:00:00-05:00", after.OccurredAt.Format(time.RFC3339))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupHandlerTestSuite(t)
			before := storedExpense()
			ts.expenseRepo.EXPECT().Get(gomock.Any(), testUserId, before.Id).Return(before, nil)

			var written models.Expense
			ts.expenseRepo.EXPECT().Update(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, e models.Expense) error { written = e; return nil })

			got, err := ts.handler.UpdateExpense(userCtx, before.Id, tt.patch)
			require.NoError(t, err)
			assert.Equal(t, written, got)
			assert.Equal(t, before.Id, got.Id)
			assert.Equal(t, before.CreatedAt, got.CreatedAt)
			assert.True(t, got.UpdatedAt.After(before.UpdatedAt), "updated_at moves forward")
			tt.verify(t, before, got)
		})
	}
}

func TestUpdateExpense_NoChanges(t *testing.T) {
	tests := []struct {
		name  string
		patch map[string]any
	}{
		{name: "empty patch", patch: map[string]any{}},
		{name: "same values", patch: map[string]any{"amount": "12.5", "merchant": "Rewe", "occurred_at": "2026-11-07T13:20:00+01:00"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupHandlerTestSuite(t)
			before := storedExpense()
			ts.expenseRepo.EXPECT().Get(gomock.Any(), testUserId, before.Id).Return(before, nil)
			// no Update expected

			got, err := ts.handler.UpdateExpense(userCtx, before.Id, tt.patch)
			require.NoError(t, err)
			assert.Equal(t, before, got)
		})
	}
}

func TestUpdateExpense_Validation(t *testing.T) {
	tests := []struct {
		name  string
		patch map[string]any
		want  map[string]string
	}{
		{name: "required field cannot be cleared", patch: map[string]any{"amount": nil, "category": nil}, want: map[string]string{"amount": "is required", "category": "is required"}},
		{name: "unknown field", patch: map[string]any{"created_at": "2026-01-01T00:00:00Z"}, want: map[string]string{"created_at": "unknown field"}},
		{name: "invalid category", patch: map[string]any{"category": "rent"}, want: map[string]string{"category": "must be one of " + joinValues(models.Categories)}},
		{name: "new amount has at most two decimals", patch: map[string]any{"amount": "1.234"}, want: map[string]string{"amount": "must have at most 2 decimal places"}},
		{name: "currency is no longer a field", patch: map[string]any{"currency": "USD"}, want: map[string]string{"currency": "unknown field"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupHandlerTestSuite(t)
			before := storedExpense()
			ts.expenseRepo.EXPECT().Get(gomock.Any(), testUserId, before.Id).Return(before, nil)

			_, err := ts.handler.UpdateExpense(userCtx, before.Id, tt.patch)
			assert.Equal(t, tt.want, validationFields(t, err))
		})
	}
}

func TestUpdateExpense_Errors(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		ts.expenseRepo.EXPECT().Get(gomock.Any(), testUserId, "missing").Return(models.Expense{}, repositories.ErrExpenseNotFound)

		_, err := ts.handler.UpdateExpense(userCtx, "missing", map[string]any{"amount": "1"})
		assert.Same(t, pkg.ErrExpenseNotFound, err)
	})

	t.Run("deleted between read and write", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		before := storedExpense()
		ts.expenseRepo.EXPECT().Get(gomock.Any(), testUserId, before.Id).Return(before, nil)
		ts.expenseRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(repositories.ErrExpenseNotFound)

		_, err := ts.handler.UpdateExpense(userCtx, before.Id, map[string]any{"amount": "1"})
		assert.Same(t, pkg.ErrExpenseNotFound, err)
	})

	t.Run("database error on write", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		before := storedExpense()
		ts.expenseRepo.EXPECT().Get(gomock.Any(), testUserId, before.Id).Return(before, nil)
		ts.expenseRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errDatabase)

		_, err := ts.handler.UpdateExpense(userCtx, before.Id, map[string]any{"amount": "1"})
		assert.Same(t, pkg.InternalError, err)
	})
}

func TestDeleteExpense(t *testing.T) {
	tests := []struct {
		name    string
		repoErr error
		wantErr error
	}{
		{name: "deleted"},
		{name: "not found", repoErr: repositories.ErrExpenseNotFound, wantErr: pkg.ErrExpenseNotFound},
		{name: "database error", repoErr: errDatabase, wantErr: pkg.InternalError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupHandlerTestSuite(t)
			ts.expenseRepo.EXPECT().Delete(gomock.Any(), testUserId, "id-1").Return(tt.repoErr)

			err := ts.handler.DeleteExpense(userCtx, "id-1")
			if tt.wantErr != nil {
				assert.Same(t, tt.wantErr, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

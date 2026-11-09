package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newTestExpenseRepo(t *testing.T) (ExpenseRepository, *sqlite.Client) {
	t.Helper()
	client := newTestClient(t)
	ctx := context.Background()

	logRepo, err := NewLogRepository(ctx, client)
	require.NoError(t, err)
	repo, err := NewExpenseRepository(ctx, client, logRepo)
	require.NoError(t, err)
	return repo, client
}

//go:fix inline
func strPtr(s string) *string { return new(s) }

// newExpense returns a valid expense occurring at the given RFC 3339 time.
func newExpense(id, occurredAt string) models.Expense {
	t, err := time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		panic(err)
	}
	created := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	return models.Expense{
		Id:         id,
		Amount:     "12.50",
		Currency:   models.CurrencyEUR,
		OccurredAt: t,
		Category:   models.CategoryFood,
		CreatedAt:  created,
		UpdatedAt:  created,
	}
}

func TestNewExpenseRepository_Idempotent(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	logRepo, err := NewLogRepository(ctx, client)
	require.NoError(t, err)

	_, err = NewExpenseRepository(ctx, client, logRepo)
	require.NoError(t, err)
	_, err = NewExpenseRepository(ctx, client, logRepo)
	require.NoError(t, err)
}

func TestNewExpenseRepository_PanicsOnNil(t *testing.T) {
	client := newTestClient(t)
	assert.Panics(t, func() { _, _ = NewExpenseRepository(context.Background(), nil, &LogRepositoryImpl{}) })
	assert.Panics(t, func() { _, _ = NewExpenseRepository(context.Background(), client, nil) })
}

func TestExpenseRepository_CreateAndGet(t *testing.T) {
	tests := []struct {
		name    string
		expense models.Expense
	}{
		{
			name: "all fields set",
			expense: func() models.Expense {
				e := newExpense("e1", "2026-11-07T13:20:00.123+01:00")
				e.Merchant = new("Rewe")
				e.Note = new("with the team")
				return e
			}(),
		},
		{name: "optional fields nil", expense: newExpense("e2", "2026-11-07T08:00:00Z")},
		{name: "negative offset", expense: newExpense("e3", "2026-11-07T23:30:00-05:00")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, _ := newTestExpenseRepo(t)
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, tt.expense))

			got, err := repo.Get(ctx, tt.expense.Id)
			require.NoError(t, err)

			// Compare the JSON form: it shows the offset and every field as the API sends them.
			want, _ := json.Marshal(tt.expense)
			have, _ := json.Marshal(got)
			assert.JSONEq(t, string(want), string(have))
			assert.Equal(t, tt.expense.OccurredAt.Format(time.RFC3339Nano), got.OccurredAt.Format(time.RFC3339Nano), "offset survives the round trip")
		})
	}
}

func TestExpenseRepository_CreateDuplicateId(t *testing.T) {
	repo, client := newTestExpenseRepo(t)
	ctx := context.Background()
	e := newExpense("e1", "2026-11-07T13:20:00+01:00")

	require.NoError(t, repo.Create(ctx, e))
	assert.Error(t, repo.Create(ctx, e))
	assert.Len(t, readLogs(t, client), 1, "the failed insert writes no log")
}

func TestExpenseRepository_GetNotFound(t *testing.T) {
	repo, _ := newTestExpenseRepo(t)

	_, err := repo.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrExpenseNotFound)
}

func TestExpenseRepository_List(t *testing.T) {
	repo, _ := newTestExpenseRepo(t)
	ctx := context.Background()

	// Local dates differ from UTC dates near midnight on purpose.
	for _, e := range []models.Expense{
		newExpense("late-new-york", "2026-11-07T23:30:00-05:00"), // UTC: 11-08 04:30, local date 11-07
		newExpense("early-berlin", "2026-11-08T00:30:00+01:00"),  // UTC: 11-07 23:30, local date 11-08
		newExpense("oct", "2026-10-31T12:00:00Z"),
		newExpense("dec", "2026-12-01T12:00:00Z"),
		newExpense("mid", "2026-11-15T12:00:00Z"),
	} {
		require.NoError(t, repo.Create(ctx, e))
	}

	day := func(s string) time.Time {
		d, err := time.Parse(time.DateOnly, s)
		require.NoError(t, err)
		return d
	}
	ids := func(es []models.Expense) []string {
		out := []string{}
		for _, e := range es {
			out = append(out, e.Id)
		}
		return out
	}

	tests := []struct {
		name     string
		from, to time.Time
		want     []string
	}{
		{name: "everything, chronological by instant", want: []string{"oct", "early-berlin", "late-new-york", "mid", "dec"}},
		{name: "november", from: day("2026-11-01"), to: day("2026-11-30"), want: []string{"early-berlin", "late-new-york", "mid"}},
		{name: "local date 11-07 only", from: day("2026-11-07"), to: day("2026-11-07"), want: []string{"late-new-york"}},
		{name: "local date 11-08 only", from: day("2026-11-08"), to: day("2026-11-08"), want: []string{"early-berlin"}},
		{name: "open start", to: day("2026-11-07"), want: []string{"oct", "late-new-york"}},
		{name: "open end", from: day("2026-11-15"), want: []string{"mid", "dec"}},
		{name: "empty range", from: day("2027-01-01"), to: day("2027-01-31"), want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.List(ctx, models.ExpenseFilter{DateFrom: tt.from, DateTo: tt.to})
			require.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, tt.want, ids(got))
		})
	}
}

func TestExpenseRepository_ListFilters(t *testing.T) {
	repo, _ := newTestExpenseRepo(t)
	ctx := context.Background()

	add := func(id, occurredAt string, category models.Category, merchant *string) {
		e := newExpense(id, occurredAt)
		e.Category = category
		e.Merchant = merchant
		require.NoError(t, repo.Create(ctx, e))
	}
	add("rewe-city", "2026-11-01T10:00:00+01:00", models.CategoryGroceries, new("Rewe City"))
	add("rewe-upper", "2026-11-02T10:00:00+01:00", models.CategoryFood, new("REWE"))
	add("aldi", "2026-11-03T10:00:00+01:00", models.CategoryGroceries, new("Aldi"))
	add("no-merchant", "2026-11-04T10:00:00+01:00", models.CategoryGroceries, nil)
	add("percent", "2026-11-05T10:00:00+01:00", models.CategoryShopping, new("100% Shop"))
	add("underscore", "2026-11-06T10:00:00+01:00", models.CategoryShopping, new("a_b"))
	add("rewe-december", "2026-12-01T10:00:00+01:00", models.CategoryGroceries, new("Rewe"))

	november := models.ExpenseFilter{
		DateFrom: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		DateTo:   time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
	}
	with := func(f models.ExpenseFilter, category models.Category, merchant string) models.ExpenseFilter {
		f.Category, f.Merchant = category, merchant
		return f
	}

	tests := []struct {
		name   string
		filter models.ExpenseFilter
		want   []string
	}{
		{name: "category", filter: with(november, models.CategoryGroceries, ""), want: []string{"rewe-city", "aldi", "no-merchant"}},
		{name: "merchant part, ignoring case", filter: with(november, "", "rewe"), want: []string{"rewe-city", "rewe-upper"}},
		{name: "category and merchant combine", filter: with(november, models.CategoryGroceries, "rewe"), want: []string{"rewe-city"}},
		{name: "dates still apply", filter: with(models.ExpenseFilter{}, models.CategoryGroceries, "rewe"), want: []string{"rewe-city", "rewe-december"}},
		{name: "percent is literal", filter: with(november, "", "%"), want: []string{"percent"}},
		{name: "underscore is literal", filter: with(november, "", "_"), want: []string{"underscore"}},
		{name: "backslash is literal", filter: with(november, "", `\`), want: []string{}},
		{name: "no match", filter: with(november, models.CategoryTravel, ""), want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.List(ctx, tt.filter)
			require.NoError(t, err)
			ids := []string{}
			for _, e := range got {
				ids = append(ids, e.Id)
			}
			assert.Equal(t, tt.want, ids)
		})
	}
}

func TestExpenseRepository_Update(t *testing.T) {
	repo, client := newTestExpenseRepo(t)
	ctx := context.Background()

	original := newExpense("e1", "2026-11-07T13:20:00+01:00")
	original.Merchant = new("Rewe")
	require.NoError(t, repo.Create(ctx, original))

	changed := original
	changed.Amount = "20.00"
	changed.Category = models.CategoryGroceries
	changed.Merchant = nil
	changed.Note = new("corrected")
	changed.UpdatedAt = original.UpdatedAt.Add(time.Hour)
	changed.CreatedAt = original.CreatedAt.Add(24 * time.Hour) // must be ignored
	require.NoError(t, repo.Update(ctx, changed))

	got, err := repo.Get(ctx, "e1")
	require.NoError(t, err)
	assert.Equal(t, "20.00", got.Amount)
	assert.Equal(t, models.CategoryGroceries, got.Category)
	assert.Nil(t, got.Merchant)
	assert.Equal(t, "corrected", *got.Note)
	assert.True(t, got.UpdatedAt.Equal(changed.UpdatedAt))
	assert.True(t, got.CreatedAt.Equal(original.CreatedAt), "created_at never changes")

	logs := readLogs(t, client)
	require.Len(t, logs, 2)
	assert.Equal(t, "update", logs[1].action)

	var before, after map[string]any
	require.NoError(t, json.Unmarshal([]byte(logs[1].before.String), &before))
	require.NoError(t, json.Unmarshal([]byte(logs[1].after.String), &after))
	assert.Equal(t, "12.50", before["amount"])
	assert.Equal(t, "Rewe", before["merchant"])
	assert.Equal(t, "20.00", after["amount"])
	assert.Nil(t, after["merchant"])
}

func TestExpenseRepository_UpdateNotFound(t *testing.T) {
	repo, client := newTestExpenseRepo(t)

	err := repo.Update(context.Background(), newExpense("missing", "2026-11-07T13:20:00Z"))
	assert.ErrorIs(t, err, ErrExpenseNotFound)
	assert.Empty(t, readLogs(t, client))
}

func TestExpenseRepository_Delete(t *testing.T) {
	repo, client := newTestExpenseRepo(t)
	ctx := context.Background()

	e := newExpense("e1", "2026-11-07T13:20:00+01:00")
	require.NoError(t, repo.Create(ctx, e))
	require.NoError(t, repo.Delete(ctx, "e1"))

	_, err := repo.Get(ctx, "e1")
	assert.ErrorIs(t, err, ErrExpenseNotFound, "delete is permanent")

	logs := readLogs(t, client)
	require.Len(t, logs, 2)
	assert.Equal(t, "delete", logs[1].action)
	assert.True(t, logs[1].before.Valid, "the deleted row survives in the log")
	assert.False(t, logs[1].after.Valid)

	var before map[string]any
	require.NoError(t, json.Unmarshal([]byte(logs[1].before.String), &before))
	assert.Equal(t, "e1", before["id"])
}

func TestExpenseRepository_DeleteNotFound(t *testing.T) {
	repo, client := newTestExpenseRepo(t)

	assert.ErrorIs(t, repo.Delete(context.Background(), "missing"), ErrExpenseNotFound)
	assert.Empty(t, readLogs(t, client))
}

func TestExpenseRepository_LogFailureRollsBack(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()
	errLog := errors.New("log write failed")

	ctrl := gomock.NewController(t)
	logRepo := NewMockLogRepository(ctrl)
	repo, err := NewExpenseRepository(ctx, client, logRepo)
	require.NoError(t, err)

	logRepo.EXPECT().Create(gomock.Any(), gomock.Any(), gomock.Any()).Return(errLog)

	err = repo.Create(ctx, newExpense("e1", "2026-11-07T13:20:00Z"))
	require.ErrorIs(t, err, errLog)

	_, err = repo.Get(ctx, "e1")
	assert.ErrorIs(t, err, ErrExpenseNotFound, "the expense is not stored when its log fails")
}

func TestExpenseRepository_CreateWritesLog(t *testing.T) {
	repo, client := newTestExpenseRepo(t)
	e := newExpense("e1", "2026-11-07T13:20:00+01:00")
	require.NoError(t, repo.Create(context.Background(), e))

	logs := readLogs(t, client)
	require.Len(t, logs, 1)
	assert.Equal(t, "create", logs[0].action)
	assert.Equal(t, sql.NullString{}, logs[0].before)

	want, _ := json.Marshal(e)
	assert.JSONEq(t, string(want), logs[0].after.String)
}

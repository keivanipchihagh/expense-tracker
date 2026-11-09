package api

import (
	"net/url"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func expenseAt(id, amount string, category models.Category, occurredAt string) models.Expense {
	t, err := time.Parse(time.RFC3339, occurredAt)
	if err != nil {
		panic(err)
	}
	return models.Expense{Id: id, Amount: amount, Category: category, OccurredAt: t}
}

func TestBuildResults(t *testing.T) {
	merchant := "Rewe"
	expenses := []models.Expense{ // oldest first, as the repository returns them
		expenseAt("e1", "10.00", models.CategoryFood, "2026-11-01T12:00:00+01:00"),
		expenseAt("e2", "30.00", models.CategoryGroceries, "2026-11-02T09:30:00+01:00"),
		expenseAt("e3", "5.50", models.CategoryTransport, "2026-11-03T08:00:00-05:00"),
		expenseAt("e4", "0.10", models.CategoryFood, "2026-11-04T18:00:00+01:00"),
	}
	expenses[1].Merchant = &merchant

	got := buildResults(dateRange{From: "2026-11-01", To: "2026-11-30"}, expenses, models.CurrencyGBP)

	ids := []string{}
	for _, r := range got.Rows {
		ids = append(ids, r.Id)
	}
	assert.Equal(t, []string{"e4", "e3", "e2", "e1"}, ids, "newest first")

	require.Len(t, got.Days, 4, "one group per local date")
	assert.Equal(t, "Wednesday, 4 November 2026", got.Days[0].Label)
	assert.Equal(t, "Sunday, 1 November 2026", got.Days[3].Label)
	assert.Equal(t, expenseRow{Id: "e2", Date: "2026-11-02", Time: "09:30", Offset: "+01:00", Amount: "30.00", Category: "groceries", Merchant: "Rewe"}, got.Rows[2])

	assert.Equal(t, models.CurrencyGBP, got.Currency)
	assert.Equal(t, "£", got.Symbol, "amounts are shown in the display currency")
	require.NotNil(t, got.Summary)
	assert.Equal(t, "45.60", got.Summary.Total, "one exact decimal sum over every expense")
	assert.Equal(t, 4, got.Summary.Count)
	assert.Equal(t, []categoryTotal{
		{Category: "groceries", Total: "30.00", Percent: 66},
		{Category: "food", Total: "10.10", Percent: 22},
		{Category: "transport", Total: "5.50", Percent: 12},
	}, got.Summary.Categories, "largest category first")
}

func TestBuildResults_Empty(t *testing.T) {
	got := buildResults(dateRange{}, []models.Expense{}, models.CurrencyEUR)
	assert.Empty(t, got.Rows)
	assert.Nil(t, got.Summary, "no total card without expenses")
	assert.Equal(t, "€", got.Symbol)
}

func TestResultsData_CurrencyOptions(t *testing.T) {
	got := resultsData{Currency: models.CurrencyUSD}.CurrencyOptions()
	assert.Equal(t, []currencyOption{
		{Code: "EUR", Symbol: "€"},
		{Code: "USD", Symbol: "$", Selected: true},
		{Code: "GBP", Symbol: "£"},
	}, got)
}

func TestFormValues_OccurredAt(t *testing.T) {
	tests := []struct {
		name    string
		local   string
		offset  string
		want    string
		wantErr string
	}{
		{name: "with seconds", local: "2026-11-07T13:20:05", offset: "60", want: "2026-11-07T13:20:05+01:00"},
		{name: "without seconds", local: "2026-11-07T13:20", offset: "60", want: "2026-11-07T13:20:00+01:00"},
		{name: "negative offset", local: "2026-11-07T23:30", offset: "-300", want: "2026-11-07T23:30:00-05:00"},
		{name: "half-hour offset", local: "2026-11-07T10:00", offset: "330", want: "2026-11-07T10:00:00+05:30"},
		{name: "utc", local: "2026-11-07T10:00", offset: "0", want: "2026-11-07T10:00:00Z"},
		{name: "missing offset", local: "2026-11-07T10:00", offset: "", wantErr: "time zone offset is missing or invalid"},
		{name: "absurd offset", local: "2026-11-07T10:00", offset: "9999", wantErr: "time zone offset is missing or invalid"},
		{name: "bad date", local: "yesterday", offset: "60", wantErr: "must be a date and time"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formValues{OccurredLocal: tt.local, TzOffset: tt.offset}.occurredAt()
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormValuesFrom_RoundTrips(t *testing.T) {
	note := "with the team"
	e := expenseAt("e1", "12.50", models.CategoryFood, "2026-11-07T13:20:05-05:00")
	e.Note = &note

	v := formValuesFrom(e)
	assert.Equal(t, "2026-11-07T13:20:05", v.OccurredLocal)
	assert.Equal(t, "-300", v.TzOffset)
	assert.Equal(t, "", v.Merchant)
	assert.Equal(t, "with the team", v.Note)

	occurredAt, err := v.occurredAt()
	require.NoError(t, err)
	assert.Equal(t, "2026-11-07T13:20:05-05:00", occurredAt, "editing without changes keeps the time and offset")
}

func TestRangeFromQuery(t *testing.T) {
	now := time.Date(2026, 2, 14, 10, 0, 0, 0, time.UTC)

	assert.Equal(t, dateRange{"2026-02-01", "2026-02-28"}, rangeFromQuery(url.Values{}, now), "defaults to the current month")
	assert.Equal(t, dateRange{"2026-01-05", "2026-01-10"}, rangeFromQuery(url.Values{"from": {"2026-01-05"}, "to": {"2026-01-10"}}, now))
	assert.Equal(t, dateRange{}, rangeFromQuery(url.Values{"from": {""}, "to": {""}}, now), "empty parameters mean all time")
	assert.Equal(t, dateRange{To: "2026-01-10"}, rangeFromQuery(url.Values{"from": {"junk"}, "to": {"2026-01-10"}}, now), "invalid dates are dropped")
}

func TestPresets(t *testing.T) {
	now := time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC)
	got := presets(now, dateRange{"2026-02-01", "2026-02-28"})

	assert.Equal(t, preset{Label: "This month", dateRange: dateRange{"2026-03-01", "2026-03-31"}}, got[0])
	assert.Equal(t, preset{Label: "Last month", dateRange: dateRange{"2026-02-01", "2026-02-28"}, Active: true}, got[1])
	assert.Equal(t, preset{Label: "This year", dateRange: dateRange{"2026-01-01", "2026-12-31"}}, got[2])
	assert.Equal(t, preset{Label: "All time", dateRange: dateRange{}}, got[3])

	for _, p := range presets(now, dateRange{"2026-03-05", "2026-03-06"}) {
		assert.False(t, p.Active, "a custom range matches no preset")
	}
}

func TestDateRange_Label(t *testing.T) {
	assert.Equal(t, "All expenses", dateRange{}.Label())
	assert.Equal(t, "2026-11-01 – 2026-11-30", dateRange{"2026-11-01", "2026-11-30"}.Label())
	assert.Equal(t, "2026-11-07", dateRange{"2026-11-07", "2026-11-07"}.Label())
	assert.Equal(t, "From 2026-11-01", dateRange{From: "2026-11-01"}.Label())
	assert.Equal(t, "Until 2026-11-30", dateRange{To: "2026-11-30"}.Label())
}

func TestIsPublicPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/health":            true,
		"/":                  true,
		"/login":             true,
		"/ui/expenses":       true,
		"/static/style.css":  true,
		"/oauth/token":       true,
		"/.well-known/x":     true,
		"/api/v1/expenses":   false,
		"/api/v1/expenses/1": false,
		"/mcp":               false,
	} {
		assert.Equal(t, want, IsPublicPath(path), path)
	}
}

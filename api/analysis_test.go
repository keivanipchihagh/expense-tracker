package api

import (
	"strings"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func spent(id, amount string, category models.Category, merchant, occurredAt string) models.Expense {
	e := expenseAt(id, amount, category, occurredAt)
	if merchant != "" {
		e.Merchant = &merchant
	}
	return e
}

func TestAnalysisPeriods(t *testing.T) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)
	got := map[string]analysisPeriod{}
	for _, p := range analysisPeriods(now) {
		got[p.Key] = p
	}

	check := func(key, from, to, prevFrom, prevTo string) {
		t.Helper()
		p := got[key]
		assert.Equal(t, day(from), p.From, key)
		assert.Equal(t, day(to), p.To, key)
		assert.Equal(t, day(prevFrom), p.PrevFrom, key)
		assert.Equal(t, day(prevTo), p.PrevTo, key)
	}
	check("month", "2026-11-01", "2026-11-30", "2026-10-01", "2026-10-31")
	check("last-month", "2026-10-01", "2026-10-31", "2026-09-01", "2026-09-30")
	check("3m", "2026-09-01", "2026-11-30", "2026-06-01", "2026-08-31")
	check("6m", "2026-06-01", "2026-11-30", "2025-12-01", "2026-05-31")
	check("12m", "2025-12-01", "2026-11-30", "2024-12-01", "2025-11-30")

	all := got["all"]
	assert.True(t, all.From.IsZero(), "all time has no fixed start")
	assert.Equal(t, day("2026-11-09"), all.To)
	assert.False(t, all.HasPrevious())

	assert.Equal(t, "month", analysisPeriodFor("", now).Key, "defaults to this month")
	assert.Equal(t, "month", analysisPeriodFor("decade", now).Key, "unknown keys fall back")
	assert.Equal(t, "3m", analysisPeriodFor("3m", now).Key)

	march := analysisPeriodFor("last-month", time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	assert.Equal(t, day("2026-02-01"), march.From, "month arithmetic doesn't overflow on the 31st")
	assert.Equal(t, day("2026-02-28"), march.To)
}

func TestParseScope(t *testing.T) {
	rent := spent("r", "1000", models.CategoryHousing, "", "2026-11-01T09:00:00+01:00")
	food := spent("f", "10", models.CategoryFood, "", "2026-11-01T12:00:00+01:00")

	all := parseScope("")
	assert.True(t, all.includes(rent) && all.includes(food))

	noHousing := parseScope("no-housing")
	assert.True(t, noHousing.ExcludeHousing)
	assert.False(t, noHousing.includes(rent))
	assert.True(t, noHousing.includes(food))

	onlyFood := parseScope("food")
	assert.Equal(t, models.CategoryFood, onlyFood.Category)
	assert.False(t, onlyFood.includes(rent))
	assert.True(t, onlyFood.includes(food))

	assert.Equal(t, analysisScope{}, parseScope("pets"), "unknown values mean everything")
}

func TestNiceScale(t *testing.T) {
	tests := []struct {
		in, top float64
	}{
		{0, 1}, {1, 1}, {3.5, 4}, {9, 10}, {130, 200}, {364, 400}, {1000, 1000}, {1298.73, 2000}, {3149, 4000}, {25000, 40000},
	}
	for _, tt := range tests {
		top, ticks := niceScale(tt.in)
		assert.Equal(t, tt.top, top, "niceScale(%v)", tt.in)
		assert.GreaterOrEqual(t, top, tt.in)
		require.Len(t, ticks, 5)
		assert.Equal(t, 0.0, ticks[0])
		assert.Equal(t, top, ticks[4])
	}
}

func TestFormatTick(t *testing.T) {
	for in, want := range map[float64]string{
		0: "0", 50: "50", 2.5: "2.5", 1000: "1,000", 7500: "7,500", 10000: "10K", 25000: "25K", 12500: "12.5K", 2_500_000: "2.5M",
	} {
		assert.Equal(t, want, formatTick(in), "%v", in)
	}
}

func TestRangeLabel(t *testing.T) {
	assert.Equal(t, "1 – 30 Nov 2026", rangeLabel(day("2026-11-01"), day("2026-11-30")))
	assert.Equal(t, "1 Sep – 30 Nov 2026", rangeLabel(day("2026-09-01"), day("2026-11-30")))
	assert.Equal(t, "1 Dec 2025 – 30 Nov 2026", rangeLabel(day("2025-12-01"), day("2026-11-30")))
	assert.Equal(t, "9 Nov 2026", rangeLabel(day("2026-11-09"), day("2026-11-09")))
}

func TestNewDelta(t *testing.T) {
	up := newDelta(mustDecimal("129"), mustDecimal("100"), "last month")
	require.NotNil(t, up)
	assert.Equal(t, delta{Text: "+29%", Up: true, Against: "last month"}, *up)

	down := newDelta(mustDecimal("75"), mustDecimal("100"), "last month")
	require.NotNil(t, down)
	assert.Equal(t, "-25%", down.Text)
	assert.False(t, down.Up)

	flat := newDelta(mustDecimal("100"), mustDecimal("100"), "last month")
	require.NotNil(t, flat)
	assert.Equal(t, "0%", flat.Text)

	assert.Nil(t, newDelta(mustDecimal("50"), mustDecimal("0"), "last month"), "no percentage of nothing")
}

// november is this month on 9 Nov 2026, with October to compare against.
func november() (time.Time, analysisPeriod, []models.Expense, []models.Expense) {
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)
	current := []models.Expense{ // oldest first, like the repository
		spent("rent", "1250.00", models.CategoryHousing, "Landlord", "2026-11-01T09:00:00+01:00"),
		spent("shop", "48.73", models.CategoryGroceries, "Rewe", "2026-11-01T11:05:00+01:00"),
		spent("bahn", "58.00", models.CategoryTransport, "Deutsche Bahn", "2026-11-02T08:15:00+01:00"),
		spent("lunch", "34.50", models.CategoryFood, "", "2026-11-04T13:10:00+01:00"),
		spent("pizza", "6.75", models.CategoryFood, "Joe's Pizza", "2026-11-04T12:20:00-05:00"),
		spent("shop2", "22.18", models.CategoryGroceries, "Rewe", "2026-11-05T18:40:00+01:00"),
	}
	previous := []models.Expense{
		spent("oct-rent", "1250.00", models.CategoryHousing, "Landlord", "2026-10-01T09:00:00+02:00"),
		spent("oct-shop", "61.42", models.CategoryGroceries, "Rewe", "2026-10-12T11:00:00+02:00"),
	}
	return now, analysisPeriodFor("month", now), current, previous
}

func TestBuildAnalysis_Figures(t *testing.T) {
	now, period, current, previous := november()
	got := buildAnalysis(now, period, analysisScope{}, current, previous, models.CurrencyEUR)

	assert.False(t, got.Empty)
	assert.Equal(t, "€", got.Symbol)
	assert.Equal(t, "1 – 30 Nov 2026", got.RangeLabel)
	assert.Empty(t, got.TrackedFrom, "October has expenses, so November is fully tracked")
	assert.Equal(t, "1420.16", got.Total, "exact decimal sum")
	assert.Equal(t, 6, got.Count)
	assert.Equal(t, 9, got.Days, "1 to 9 November so far")
	assert.Equal(t, "157.80", got.PerDay, "1420.16 / 9")
	assert.Equal(t, "236.69", got.Average, "1420.16 / 6")
	assert.Equal(t, &largestExpense{Amount: "1250.00", Label: "Landlord", Date: "1 Nov 2026"}, got.Largest)

	// Against October by the same day (9th): only October's rent.
	require.NotNil(t, got.TotalDelta)
	assert.Equal(t, delta{Text: "+14%", Up: true, Against: "last month at this point"}, *got.TotalDelta)
}

func TestBuildAnalysis_Categories(t *testing.T) {
	now, period, current, previous := november()
	got := buildAnalysis(now, period, analysisScope{}, current, previous, models.CurrencyEUR)

	require.Len(t, got.Categories, 4)
	assert.Equal(t, categoryRow{Category: "housing", Total: "1250.00", Share: 88, Length: 100, Href: "/?category=housing&from=2026-11-01&to=2026-11-30"}, got.Categories[0])
	assert.Equal(t, "groceries", got.Categories[1].Category, "largest first")
	assert.Equal(t, "70.91", got.Categories[1].Total)
	assert.Equal(t, 6, got.Categories[1].Length, "bar length is relative to the largest category")
	assert.Equal(t, "food", got.Categories[3].Category)
	assert.Equal(t, "41.25", got.Categories[3].Total, "lunch in Berlin and pizza in New York both count")

	single := buildAnalysis(now, period, parseScope("food"), current, previous, models.CurrencyEUR)
	assert.Nil(t, single.Categories, "one category needs no breakdown")
	assert.Equal(t, "41.25", single.Total)
}

func TestBuildAnalysis_Merchants(t *testing.T) {
	now, period, current, previous := november()
	got := buildAnalysis(now, period, analysisScope{}, current, previous, models.CurrencyEUR)

	require.Len(t, got.Merchants, 4)
	assert.Equal(t, merchantRow{Name: "Landlord", Count: 1, Total: "1250.00", Share: 88, Href: "/?from=2026-11-01&merchant=Landlord&to=2026-11-30"}, got.Merchants[0])
	assert.Equal(t, merchantRow{Name: "Rewe", Count: 2, Total: "70.91", Share: 5, Href: "/?from=2026-11-01&merchant=Rewe&to=2026-11-30"}, got.Merchants[1], "purchases are counted and summed")
	assert.Equal(t, 1, got.Unnamed, "the lunch without a merchant is counted, not listed")

	many := []models.Expense{}
	for i, name := range strings.Fields("a b c d e f g h i j") {
		many = append(many, spent(name, "1"+strings.Repeat("0", i%3), models.CategoryOther, name, "2026-11-02T10:00:00+01:00"))
	}
	assert.Len(t, buildAnalysis(now, period, analysisScope{}, many, previous, models.CurrencyEUR).Merchants, topMerchants)
}

func TestBuildAnalysis_ScopeWithoutHousing(t *testing.T) {
	now, period, current, previous := november()
	got := buildAnalysis(now, period, parseScope("no-housing"), current, previous, models.CurrencyGBP)

	assert.Equal(t, "170.16", got.Total)
	assert.Equal(t, "£", got.Symbol)
	for _, c := range got.Categories {
		assert.NotEqual(t, "housing", c.Category)
	}
	assert.Equal(t, "Rewe", got.Merchants[0].Name, "rent's landlord is gone")

	// October without rent had nothing by the 9th: no percentage, but the
	// comparison still shows because October has expenses later on.
	assert.Nil(t, got.TotalDelta)
	require.NotNil(t, got.Pace)
	assert.Equal(t, "0.00", got.Pace.PreviousSameDay)
	assert.Equal(t, "61.42", got.Pace.PreviousTotal)

	for _, p := range got.Periods {
		assert.Contains(t, string(p.Query), "scope=no-housing", "switching period keeps the scope")
	}
	assert.True(t, got.ScopeOptions[1].Selected)
}

func TestBuildAnalysis_Timeline(t *testing.T) {
	now, period, current, previous := november()
	tl := buildAnalysis(now, period, analysisScope{}, current, previous, models.CurrencyEUR).Timeline

	assert.Equal(t, "day", tl.Unit)
	require.Len(t, tl.Buckets, 30, "every day of November, the rest still to come")
	first := tl.Buckets[0]
	assert.Equal(t, "1298.73", first.Total)
	assert.Equal(t, "1 Nov", first.Label)
	assert.True(t, first.ShowLabel)
	assert.Equal(t, "Sun, 1 Nov 2026: €1298.73", first.Title)
	assert.InDelta(t, 1298.73/2000*chartHeight, first.H, 0.01, "scaled to the axis top")
	assert.Equal(t, chartHeight, first.Y+first.H, "columns stand on the baseline")
	assert.LessOrEqual(t, first.W, maxBarWidth)
	assert.Equal(t, chartHeight, first.BaseY+first.BaseH, "the square strip sits on the baseline")

	assert.Equal(t, "41.25", tl.Buckets[3].Total, "4 Nov in each expense's own time zone")
	assert.Zero(t, tl.Buckets[2].H, "no column on a day without spending")
	assert.False(t, tl.Buckets[8].Future, "today has happened")
	assert.True(t, tl.Buckets[9].Future)
	assert.Contains(t, tl.Buckets[9].Title, "still to come")

	assert.Equal(t, []string{"€2,000", "€1,500", "€1,000", "€500", "€0"}, tl.Ticks)
	assert.Equal(t, "157.80", tl.Average, "average over the days so far, not the whole month")
	assert.InDelta(t, chartHeight-157.80/2000*chartHeight, tl.AvgY, 0.01)

	for i, b := range tl.Buckets {
		assert.InDelta(t, float64(i)*chartWidth/30, b.SlotX, 0.001, "hover targets tile the width")
	}
}

func TestBuildTimeline_Units(t *testing.T) {
	today := day("2026-11-09")
	byDay := map[string]decimal.Decimal{"2026-10-07": mustDecimal("10"), "2026-10-12": mustDecimal("5"), "2026-11-03": mustDecimal("7")}

	weeks := buildTimeline(day("2026-06-01"), day("2026-11-30"), today, byDay, "€")
	assert.Equal(t, "week", weeks.Unit)
	assert.Equal(t, "1 Jun", weeks.Buckets[0].Label, "1 June 2026 is a Monday")
	var octWeek bucket
	for _, b := range weeks.Buckets {
		if b.Label == "5 Oct" {
			octWeek = b
		}
	}
	assert.Equal(t, "10.00", octWeek.Total, "a week runs Monday to Sunday")
	assert.Equal(t, "5 Oct – 11 Oct 2026: €10.00", octWeek.Title)

	partial := buildTimeline(day("2026-10-07"), day("2026-12-31"), today, byDay, "€")
	assert.Equal(t, "7 Oct", partial.Buckets[0].Label, "the first week starts on the period's first day")

	months := buildTimeline(day("2025-12-01"), day("2026-11-30"), today, byDay, "€")
	assert.Equal(t, "month", months.Unit)
	require.Len(t, months.Buckets, 12)
	assert.Equal(t, "Dec", months.Buckets[0].Label)
	assert.Equal(t, "15.00", months.Buckets[10].Total, "October")
	assert.Equal(t, "October 2026: €15.00", months.Buckets[10].Title)
	assert.Equal(t, "1.83", months.Average, "22 over the 12 months that have started")
}

func TestBuildAnalysis_Pace(t *testing.T) {
	now, period, current, previous := november()
	p := buildAnalysis(now, period, analysisScope{}, current, previous, models.CurrencyEUR).Pace
	require.NotNil(t, p)

	assert.True(t, p.InProgress)
	assert.Equal(t, 9, p.SameDay)
	assert.Equal(t, "1420.16", p.Current)
	assert.Equal(t, "1250.00", p.PreviousSameDay)
	assert.Equal(t, "1311.42", p.PreviousTotal)
	assert.Equal(t, "This month", p.CurrentName)
	assert.Equal(t, "last month", p.PrevName)
	assert.Equal(t, "Day 31", p.EndLabel, "October has 31 days")

	assert.Len(t, strings.Fields(p.CurrentPoints), 9, "this month is drawn up to today")
	assert.Len(t, strings.Fields(p.PreviousPoints), 31, "last month in full")
	assert.True(t, strings.HasPrefix(p.CurrentArea, "0,200 "), "the area closes on the baseline")
	require.Len(t, p.Hits, 31)
	assert.Equal(t, "Day 1 · 1 Nov: €1298.73 · 1 Oct: €1250.00", p.Hits[0].Title)
	assert.Equal(t, "Day 31 · 31 Oct: €1311.42", p.Hits[30].Title)

	last := analysisPeriodFor("last-month", now)
	done := buildAnalysis(now, last, analysisScope{}, previous, nil, models.CurrencyEUR)
	assert.Nil(t, done.Pace, "nothing to compare against")
	assert.Nil(t, done.TotalDelta)
}

func TestBuildAnalysis_TrackingStartsInsidePeriod(t *testing.T) {
	now, _, current, _ := november()
	got := buildAnalysis(now, analysisPeriodFor("6m", now), analysisScope{}, current, nil, models.CurrencyEUR)

	assert.Equal(t, "1 Jun – 30 Nov 2026", got.RangeLabel)
	assert.Equal(t, "1 Nov 2026", got.TrackedFrom)
	assert.Equal(t, 9, got.Days, "per day counts from the first expense, not from June")
	assert.Equal(t, "day", got.Timeline.Unit, "and so does the chart")
	assert.Nil(t, got.Pace, "the 6 months before are empty")
	assert.Equal(t, "/?category=housing&from=2026-06-01&to=2026-11-30", string(got.Categories[0].Href), "links still cover the period")
}

func TestBuildAnalysis_AllTime(t *testing.T) {
	now, _, current, previous := november()
	all := append(append([]models.Expense{}, previous...), current...)
	got := buildAnalysis(now, analysisPeriodFor("all", now), analysisScope{}, all, nil, models.CurrencyEUR)

	assert.Equal(t, "1 Oct – 9 Nov 2026", got.RangeLabel, "from the first expense to today")
	assert.Equal(t, 40, got.Days)
	assert.Empty(t, got.TrackedFrom)
	assert.Nil(t, got.Pace)
}

func TestBuildAnalysis_Empty(t *testing.T) {
	now, period, _, _ := november()
	got := buildAnalysis(now, period, analysisScope{}, nil, nil, models.CurrencyEUR)
	assert.True(t, got.Empty)
	assert.Len(t, got.Periods, 6, "the filters still show")
	assert.Len(t, got.ScopeOptions, 2+len(models.Categories))
}

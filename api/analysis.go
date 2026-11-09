package api

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/shopspring/decimal"
)

// Chart geometry, in SVG viewBox units. The plots stretch to the card's
// width (preserveAspectRatio="none") and are chartHeight CSS pixels tall,
// so y units are pixels; text lives in HTML around the SVG so it never
// stretches.
const (
	chartWidth  = 1000.0
	chartHeight = 200.0
	// maxBarWidth caps a column at roughly 24px on a typical card.
	maxBarWidth = 36.0
	// topMerchants is how many merchants the merchants table lists.
	topMerchants = 8
)

// AnalysisPage handles GET /analysis: totals, categories, spending over
// time, pace against the previous period and top merchants, all for one
// period and scope.
func (s *Server) AnalysisPage(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	q := r.URL.Query()
	period := analysisPeriodFor(q.Get("period"), now)
	scope := parseScope(q.Get("scope"))

	current, err := s.handler.ListExpenses(r.Context(), models.ExpenseFilter{DateFrom: period.From, DateTo: period.To})
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	var previous []models.Expense
	if period.HasPrevious() {
		previous, err = s.handler.ListExpenses(r.Context(), models.ExpenseFilter{DateFrom: period.PrevFrom, DateTo: period.PrevTo})
		if err != nil {
			s.renderUiError(w, r, err)
			return
		}
	}
	currency, err := s.handler.DisplayCurrency(r.Context())
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}

	s.renderSignedIn(w, r, http.StatusOK, "analysis.html", pageData{
		Title:    "Analysis",
		Nav:      "analysis",
		Analysis: buildAnalysis(now, period, scope, current, previous, currency),
	})
}

// analysisPeriod is one of the period presets. From and To are inclusive
// local dates; From is zero for "All time". The previous period has the
// same number of months and is zero when there is none.
type analysisPeriod struct {
	Key   string
	Label string
	From  time.Time
	To    time.Time
	// PrevFrom and PrevTo bound the period compared against.
	PrevFrom time.Time
	PrevTo   time.Time
	// PrevName names the previous period in sentences: "last month".
	PrevName string
}

func (p analysisPeriod) HasPrevious() bool { return !p.PrevFrom.IsZero() }

// analysisPeriods returns the presets, in the order the page shows them.
func analysisPeriods(now time.Time) []analysisPeriod {
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	// months returns the n months ending with the current one (shifted back
	// by `back` months), and the n months before them.
	months := func(key, label, prevName string, n, back int) analysisPeriod {
		end := month.AddDate(0, 1-back, -1)
		start := month.AddDate(0, 1-back-n, 0)
		return analysisPeriod{
			Key: key, Label: label, From: start, To: end,
			PrevFrom: start.AddDate(0, -n, 0), PrevTo: start.AddDate(0, 0, -1), PrevName: prevName,
		}
	}
	return []analysisPeriod{
		months("month", "This month", "last month", 1, 0),
		months("last-month", "Last month", "the month before", 1, 1),
		months("3m", "3 months", "the 3 months before", 3, 0),
		months("6m", "6 months", "the 6 months before", 6, 0),
		months("12m", "12 months", "the 12 months before", 12, 0),
		{Key: "all", Label: "All time", To: today},
	}
}

// analysisPeriodFor returns the preset with the given key, or this month.
func analysisPeriodFor(key string, now time.Time) analysisPeriod {
	periods := analysisPeriods(now)
	for _, p := range periods {
		if p.Key == key {
			return p
		}
	}
	return periods[0]
}

// analysisScope narrows the expenses analysed: every category, every
// category except housing (rent dwarfs everything else), or one category.
type analysisScope struct {
	Value          string
	Category       models.Category
	ExcludeHousing bool
}

const scopeNoHousing = "no-housing"

func parseScope(v string) analysisScope {
	if v == scopeNoHousing {
		return analysisScope{Value: v, ExcludeHousing: true}
	}
	if c := models.Category(v); c.IsValid() {
		return analysisScope{Value: v, Category: c}
	}
	return analysisScope{}
}

func (s analysisScope) includes(e models.Expense) bool {
	switch {
	case s.Category != "":
		return e.Category == s.Category
	case s.ExcludeHousing:
		return e.Category != models.CategoryHousing
	}
	return true
}

func (s analysisScope) filter(expenses []models.Expense) []models.Expense {
	out := make([]models.Expense, 0, len(expenses))
	for _, e := range expenses {
		if s.includes(e) {
			out = append(out, e)
		}
	}
	return out
}

// analysisData is everything the analysis page shows.
type analysisData struct {
	Periods      []periodLink
	ScopeOptions []scopeOption
	Period       string
	Scope        string
	Symbol       string
	// RangeLabel describes the dates analysed, e.g. "1 – 30 Nov 2026".
	RangeLabel string
	// TrackedFrom is set when the first expense falls inside the period
	// and nothing came before it: the figures start from that date.
	TrackedFrom string
	Empty       bool

	Total      string
	TotalDelta *delta
	PerDay     string
	Days       int
	Count      int
	Average    string
	Largest    *largestExpense

	// Categories is nil when the scope is a single category.
	Categories []categoryRow
	Timeline   timeline
	Pace       *pace
	Merchants  []merchantRow
	// Unnamed counts expenses without a merchant (left out of Merchants).
	Unnamed int
}

type periodLink struct {
	Label  string
	Query  template.URL
	Active bool
}

type scopeOption struct {
	Value, Label string
	Selected     bool
}

// delta compares a figure with the previous period.
type delta struct {
	// Text is the signed change, e.g. "+12%".
	Text string
	// Up is true when spending went up.
	Up bool
	// Against names what was compared, e.g. "last month at this point".
	Against string
}

type largestExpense struct {
	Amount, Label, Date string
}

type categoryRow struct {
	Category string
	Total    string
	Share    int // percent of the total
	Length   int // bar length, percent of the largest category
	Href     template.URL
}

type merchantRow struct {
	Name  string
	Count int
	Total string
	Share int
	Href  template.URL
}

// timeline is a column chart of spending per day, week or month.
type timeline struct {
	// Unit is "day", "week" or "month".
	Unit    string
	Buckets []bucket
	// Ticks are the y-axis labels from top to bottom.
	Ticks []string
	// AvgY is the y of the average line; Average is its value.
	AvgY    float64
	Average string
}

type bucket struct {
	Label     string
	ShowLabel bool
	// Title is the hover text: the bucket's dates and total.
	Title  string
	Total  string
	Future bool
	// Geometry, in viewBox units. Base is the square-cornered strip that
	// keeps the column flat on the baseline under its rounded top.
	X, Y, W, H, BaseY, BaseH float64
	// SlotX and SlotW are the hover target: the whole column slot.
	SlotX, SlotW float64
}

// pace compares cumulative spending day by day with the previous period.
type pace struct {
	CurrentPoints  string
	CurrentArea    string
	PreviousPoints string
	Ticks          []string
	// So far: this period's total up to today (or in full when it's over),
	// and the previous period's total at the same day.
	Current, PreviousSameDay, PreviousTotal string
	SameDay                                 int
	InProgress                              bool
	Delta                                   *delta
	// CurrentName and PrevName label the two lines: "This month", "last month".
	CurrentName, PrevName string
	StartLabel, EndLabel  string
	Hits                  []paceHit
}

type paceHit struct {
	X, W  float64
	Title string
}

// buildAnalysis computes the page from the current and previous periods'
// expenses (oldest first). Amounts are summed exactly; floats are only used
// for chart geometry.
func buildAnalysis(now time.Time, period analysisPeriod, scope analysisScope, current, previous []models.Expense, currency models.Currency) analysisData {
	current = scope.filter(current)
	previous = scope.filter(previous)
	symbol := currency.Symbol()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	out := analysisData{Period: period.Key, Scope: scope.Value, Symbol: symbol}
	for _, p := range analysisPeriods(now) {
		v := url.Values{"period": {p.Key}}
		if scope.Value != "" {
			v.Set("scope", scope.Value)
		}
		out.Periods = append(out.Periods, periodLink{Label: p.Label, Query: template.URL(v.Encode()), Active: p.Key == period.Key})
	}
	out.ScopeOptions = append(out.ScopeOptions,
		scopeOption{Value: "", Label: "All categories", Selected: scope.Value == ""},
		scopeOption{Value: scopeNoHousing, Label: "Everything except housing", Selected: scope.ExcludeHousing},
	)
	for _, c := range models.Categories {
		out.ScopeOptions = append(out.ScopeOptions, scopeOption{Value: string(c), Label: titleCase(string(c)), Selected: scope.Category == c})
	}

	// "All time" starts at the first expense.
	start := period.From
	if start.IsZero() {
		start = today
		if len(current) > 0 {
			start = localDate(current[0])
		}
	}
	end := period.To
	out.RangeLabel = rangeLabel(start, end)
	if len(current) == 0 {
		out.Empty = true
		return out
	}
	// Links to the expense list cover the whole period.
	linkStart := start
	// With no expenses before the period, tracking began during it: the
	// averages and the time chart start at the first expense rather than
	// counting days that were never tracked.
	if len(previous) == 0 {
		if first := localDate(current[0]); first.After(start) {
			start = first
			out.TrackedFrom = first.Format("2 Jan 2006")
		}
	}

	lastDay := minDate(end, today) // the last day that has happened
	inProgress := end.After(today)
	elapsed := daysBetween(start, lastDay) + 1

	byDay := map[string]decimal.Decimal{}
	var total decimal.Decimal
	categories := map[models.Category]decimal.Decimal{}
	type merchantSum struct {
		total decimal.Decimal
		count int
	}
	merchants := map[string]*merchantSum{}
	var largest models.Expense
	var largestAmount decimal.Decimal
	for _, e := range current {
		amount := mustDecimal(e.Amount)
		total = total.Add(amount)
		byDay[localDate(e).Format(time.DateOnly)] = byDay[localDate(e).Format(time.DateOnly)].Add(amount)
		categories[e.Category] = categories[e.Category].Add(amount)
		if e.Merchant != nil {
			m := merchants[*e.Merchant]
			if m == nil {
				m = &merchantSum{}
				merchants[*e.Merchant] = m
			}
			m.total = m.total.Add(amount)
			m.count++
		} else {
			out.Unnamed++
		}
		if amount.GreaterThan(largestAmount) {
			largest, largestAmount = e, amount
		}
	}

	// Figures
	out.Total = total.StringFixed(2)
	out.Count = len(current)
	out.Days = elapsed
	out.PerDay = total.Div(decimal.NewFromInt(int64(elapsed))).StringFixed(2)
	out.Average = total.Div(decimal.NewFromInt(int64(len(current)))).StringFixed(2)
	label := titleCase(string(largest.Category))
	if largest.Merchant != nil {
		label = *largest.Merchant
	}
	out.Largest = &largestExpense{Amount: largestAmount.StringFixed(2), Label: label, Date: localDate(largest).Format("2 Jan 2006")}

	// Categories, largest first; pointless for a single category.
	if scope.Category == "" {
		out.Categories = categoryRows(categories, total, linkStart, end)
	}

	// Merchants, largest first.
	names := make([]string, 0, len(merchants))
	for name := range merchants {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if c := merchants[b].total.Cmp(merchants[a].total); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	for _, name := range names[:min(len(names), topMerchants)] {
		m := merchants[name]
		out.Merchants = append(out.Merchants, merchantRow{
			Name: name, Count: m.count, Total: m.total.StringFixed(2), Share: percent(m.total, total),
			Href: listHref(linkStart, end, "merchant", name),
		})
	}

	out.Timeline = buildTimeline(start, end, today, byDay, symbol)

	// Comparing against a period with nothing in it says nothing.
	if period.HasPrevious() && len(previous) > 0 {
		out.Pace = buildPace(period, start, elapsed, inProgress, byDay, previous, symbol)
		out.TotalDelta = out.Pace.Delta
	}
	return out
}

func categoryRows(categories map[models.Category]decimal.Decimal, total decimal.Decimal, start, end time.Time) []categoryRow {
	rows := make([]categoryRow, 0, len(categories))
	var largest decimal.Decimal
	for c, sum := range categories {
		rows = append(rows, categoryRow{Category: string(c), Total: sum.StringFixed(2), Share: percent(sum, total)})
		if sum.GreaterThan(largest) {
			largest = sum
		}
	}
	slices.SortFunc(rows, func(a, b categoryRow) int {
		if c := mustDecimal(b.Total).Cmp(mustDecimal(a.Total)); c != 0 {
			return c
		}
		return strings.Compare(a.Category, b.Category)
	})
	for i := range rows {
		rows[i].Length = percent(mustDecimal(rows[i].Total), largest)
		rows[i].Href = listHref(start, end, "category", rows[i].Category)
	}
	return rows
}

// buildTimeline sums days into columns: days for up to two months, weeks
// (starting Monday) for up to about six, months beyond that.
func buildTimeline(start, end, today time.Time, byDay map[string]decimal.Decimal, symbol string) timeline {
	days := daysBetween(start, end) + 1
	unit := "month"
	switch {
	case days <= 62:
		unit = "day"
	case days <= 190:
		unit = "week"
	}

	type span struct {
		from, to time.Time
		total    decimal.Decimal
	}
	var spans []span
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		var key time.Time
		switch unit {
		case "day":
			key = d
		case "week":
			key = d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)) // Monday
		default:
			key = time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
		if n := len(spans); n == 0 || !spans[n-1].from.Equal(maxDate(key, start)) {
			spans = append(spans, span{from: maxDate(key, start)})
		}
		s := &spans[len(spans)-1]
		s.to = d
		s.total = s.total.Add(byDay[d.Format(time.DateOnly)])
	}

	var largest, happened decimal.Decimal
	elapsed := 0
	for _, s := range spans {
		if s.total.GreaterThan(largest) {
			largest = s.total
		}
		if !s.from.After(today) {
			happened = happened.Add(s.total)
			elapsed++
		}
	}
	top, ticks := niceScale(largest.InexactFloat64())
	t := timeline{Unit: unit}
	for _, tick := range slices.Backward(ticks) {
		t.Ticks = append(t.Ticks, symbol+formatTick(tick))
	}

	slot := chartWidth / float64(len(spans))
	width := math.Min(slot*0.72, maxBarWidth)
	labelEvery := int(math.Ceil(float64(len(spans)) / 7))
	for i, s := range spans {
		b := bucket{
			Total:     s.total.StringFixed(2),
			Future:    s.from.After(today),
			ShowLabel: i%labelEvery == 0,
			SlotX:     float64(i) * slot,
			SlotW:     slot,
		}
		switch unit {
		case "day":
			b.Label = s.from.Format("2 Jan")
			b.Title = s.from.Format("Mon, 2 Jan 2006")
		case "week":
			b.Label = s.from.Format("2 Jan")
			b.Title = s.from.Format("2 Jan") + " – " + s.to.Format("2 Jan 2006")
		default:
			b.Label = s.from.Format("Jan")
			b.Title = s.from.Format("January 2006")
		}
		b.Title += ": " + symbol + b.Total
		if b.Future {
			b.Title += " (still to come)"
		}
		if s.total.IsPositive() {
			b.H = math.Max(s.total.InexactFloat64()/top*chartHeight, 2)
			b.W = width
			b.X = b.SlotX + (slot-width)/2
			b.Y = chartHeight - b.H
			b.BaseH = math.Min(b.H, 4)
			b.BaseY = chartHeight - b.BaseH
		}
		t.Buckets = append(t.Buckets, b)
	}

	if elapsed > 0 {
		avg := happened.Div(decimal.NewFromInt(int64(elapsed)))
		t.Average = avg.StringFixed(2)
		t.AvgY = chartHeight - avg.InexactFloat64()/top*chartHeight
	}
	return t
}

// buildPace accumulates spending day by day for this period and the
// previous one, lined up by day number (day 1 of each, day 2, ...).
func buildPace(period analysisPeriod, start time.Time, elapsed int, inProgress bool, byDay map[string]decimal.Decimal, previous []models.Expense, symbol string) *pace {
	curDays := daysBetween(start, period.To) + 1
	prevDays := daysBetween(period.PrevFrom, period.PrevTo) + 1
	span := max(curDays, prevDays)

	prevByDay := map[string]decimal.Decimal{}
	for _, e := range previous {
		key := localDate(e).Format(time.DateOnly)
		prevByDay[key] = prevByDay[key].Add(mustDecimal(e.Amount))
	}
	cumulate := func(from time.Time, n int, sums map[string]decimal.Decimal) []decimal.Decimal {
		out := make([]decimal.Decimal, n)
		var running decimal.Decimal
		for i := range n {
			running = running.Add(sums[from.AddDate(0, 0, i).Format(time.DateOnly)])
			out[i] = running
		}
		return out
	}
	cur := cumulate(start, elapsed, byDay)
	prev := cumulate(period.PrevFrom, prevDays, prevByDay)

	top, ticks := niceScale(math.Max(cur[len(cur)-1].InexactFloat64(), prev[len(prev)-1].InexactFloat64()))
	x := func(i int) float64 {
		if span == 1 {
			return chartWidth / 2
		}
		return float64(i) / float64(span-1) * chartWidth
	}
	y := func(v decimal.Decimal) float64 { return chartHeight - v.InexactFloat64()/top*chartHeight }
	points := func(values []decimal.Decimal) string {
		parts := make([]string, len(values))
		for i, v := range values {
			parts[i] = fmt.Sprintf("%.1f,%.1f", x(i), y(v))
		}
		return strings.Join(parts, " ")
	}

	sameDay := min(elapsed, prevDays)
	p := &pace{
		CurrentPoints:   points(cur),
		PreviousPoints:  points(prev),
		Current:         cur[len(cur)-1].StringFixed(2),
		PreviousSameDay: prev[sameDay-1].StringFixed(2),
		PreviousTotal:   prev[len(prev)-1].StringFixed(2),
		SameDay:         elapsed,
		InProgress:      inProgress,
		CurrentName:     period.Label,
		PrevName:        period.PrevName,
		StartLabel:      "Day 1",
		EndLabel:        "Day " + strconv.Itoa(span),
	}
	p.CurrentArea = fmt.Sprintf("0,%.0f %s %.1f,%.0f", chartHeight, p.CurrentPoints, x(len(cur)-1), chartHeight)
	for _, tick := range slices.Backward(ticks) {
		p.Ticks = append(p.Ticks, symbol+formatTick(tick))
	}

	against := period.PrevName
	if inProgress {
		against += " at this point"
	}
	p.Delta = newDelta(cur[len(cur)-1], prev[sameDay-1], against)

	slot := chartWidth / float64(span)
	for i := range span {
		parts := []string{"Day " + strconv.Itoa(i+1)}
		if i < len(cur) {
			parts = append(parts, start.AddDate(0, 0, i).Format("2 Jan")+": "+symbol+cur[i].StringFixed(2))
		}
		if i < len(prev) {
			parts = append(parts, period.PrevFrom.AddDate(0, 0, i).Format("2 Jan")+": "+symbol+prev[i].StringFixed(2))
		}
		p.Hits = append(p.Hits, paceHit{X: float64(i) * slot, W: slot, Title: strings.Join(parts, " · ")})
	}
	return p
}

// newDelta is the change from before to now, or nil when before is zero
// (a percentage of nothing means nothing).
func newDelta(now, before decimal.Decimal, against string) *delta {
	if !before.IsPositive() {
		return nil
	}
	change := now.Sub(before).Div(before).Mul(decimal.NewFromInt(100)).Round(0)
	text := change.String() + "%"
	if change.IsPositive() {
		text = "+" + text
	}
	return &delta{Text: text, Up: change.IsPositive(), Against: against}
}

// niceScale returns an axis top at or above v and its five evenly spaced
// ticks from 0, using steps of 1, 2, 2.5 or 5 times a power of ten.
func niceScale(v float64) (float64, []float64) {
	if v <= 0 {
		v = 1
	}
	raw := v / 4
	magnitude := math.Pow(10, math.Floor(math.Log10(raw)))
	step := 10 * magnitude
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*magnitude >= raw {
			step = m * magnitude
			break
		}
	}
	// Drop float noise such as 0.25000000000000006.
	step = math.Round(step*1e6) / 1e6
	ticks := make([]float64, 5)
	for i := range ticks {
		ticks[i] = step * float64(i)
	}
	return step * 4, ticks
}

// formatTick writes an axis value in full below 10,000 and compactly above:
// 2,500 / 25K / 2.5M.
func formatTick(v float64) string {
	switch {
	case v >= 1_000_000:
		return strings.TrimSuffix(strconv.FormatFloat(v/1_000_000, 'f', 1, 64), ".0") + "M"
	case v >= 10_000:
		return strings.TrimSuffix(strconv.FormatFloat(v/1000, 'f', 1, 64), ".0") + "K"
	case v != math.Trunc(v):
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	s := strconv.FormatInt(int64(v), 10)
	if len(s) > 3 {
		s = s[:len(s)-3] + "," + s[len(s)-3:]
	}
	return s
}

// listHref links to the expense list for the dates with one filter set.
func listHref(start, end time.Time, key, value string) template.URL {
	v := url.Values{"from": {start.Format(time.DateOnly)}, "to": {end.Format(time.DateOnly)}, key: {value}}
	return template.URL("/?" + v.Encode())
}

// rangeLabel writes a date range compactly: "1 – 30 Nov 2026",
// "1 Sep – 30 Nov 2026", "1 Dec 2025 – 30 Nov 2026".
func rangeLabel(start, end time.Time) string {
	switch {
	case start.Equal(end):
		return start.Format("2 Jan 2006")
	case start.Year() != end.Year():
		return start.Format("2 Jan 2006") + " – " + end.Format("2 Jan 2006")
	case start.Month() != end.Month():
		return start.Format("2 Jan") + " – " + end.Format("2 Jan 2006")
	}
	return start.Format("2") + " – " + end.Format("2 Jan 2006")
}

func percent(part, whole decimal.Decimal) int {
	if !whole.IsPositive() {
		return 0
	}
	return int(part.Div(whole).Mul(decimal.NewFromInt(100)).Round(0).IntPart())
}

// localDate is the expense's date in its own time zone, at midnight UTC so
// dates compare and step simply.
func localDate(e models.Expense) time.Time {
	t := e.OccurredAt
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func daysBetween(a, b time.Time) int { return int(math.Round(b.Sub(a).Hours() / 24)) }

func minDate(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxDate(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// mustDecimal parses a stored amount; stored amounts are validated, so an
// unreadable one counts as zero rather than failing the page.
func mustDecimal(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

func titleCase(s string) string { return templateFuncs["title"].(func(string) string)(s) }

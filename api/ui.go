package api

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/shopspring/decimal"
)

//go:embed web
var webFS embed.FS

// sessionCookieName is the UI session cookie's name over plain HTTP. Over
// HTTPS the __Host- prefix is added: browsers then accept the cookie only
// with Secure, Path=/ and no Domain, so subdomains can't set or replace it.
const sessionCookieName = "et_session"

// uiCsp allows only same-origin scripts and styles; nothing inline.
const uiCsp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

// templateFuncs are available in every template.
var templateFuncs = template.FuncMap{
	// title upper-cases the first letter: "groceries" → "Groceries".
	"title": func(s string) string {
		if s == "" {
			return s
		}
		r := []rune(s)
		return strings.ToUpper(string(r[0])) + string(r[1:])
	},
}

// pages are parsed once: each page gets the layout and the shared partials.
var pages = func() map[string]*template.Template {
	out := map[string]*template.Template{}
	for _, page := range []string{"index.html", "login.html", "apps.html", "error.html"} {
		out[page] = template.Must(template.New(page).Funcs(templateFuncs).ParseFS(webFS,
			"web/templates/layout.html", "web/templates/results.html", "web/templates/form.html", "web/templates/"+page))
	}
	return out
}()

// partials renders the HTMX fragments: "results", "form" and "saved".
var partials = template.Must(template.New("partials").Funcs(templateFuncs).ParseFS(webFS, "web/templates/results.html", "web/templates/form.html"))

func staticFiles() http.Handler {
	sub, err := fs.Sub(webFS, "web/static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServerFS(sub))
}

// LoginPage handles GET /login.
func (s *Server) LoginPage(w http.ResponseWriter, r *http.Request) {
	if s.signedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderPage(w, http.StatusOK, "login.html", pageData{Title: "Sign in"})
}

// LoginSubmit handles POST /login.
func (s *Server) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPage(w, http.StatusBadRequest, "login.html", pageData{Title: "Sign in", Error: "The form could not be read."})
		return
	}

	token, expiresAt, err := s.handler.Login(r.Context(), r.PostForm.Get("password"))
	switch {
	case errors.Is(err, handler.ErrWrongPassword):
		s.renderPage(w, http.StatusUnauthorized, "login.html", pageData{Title: "Sign in", Error: "Wrong password."})
		return
	case errors.Is(err, handler.ErrTooManyLogins):
		s.renderPage(w, http.StatusTooManyRequests, "login.html", pageData{Title: "Sign in", Error: "Too many failed sign-ins. Try again in 15 minutes."})
		return
	case err != nil:
		s.renderPage(w, http.StatusInternalServerError, "login.html", pageData{Title: "Sign in", Error: "Something went wrong. Try again."})
		return
	}

	http.SetCookie(w, s.sessionCookie(token, expiresAt))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout handles POST /logout.
func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	if c, err := r.Cookie(s.cookieName()); err == nil {
		_ = s.handler.Logout(r.Context(), c.Value)
	}
	expired := s.sessionCookie("", time.Unix(0, 0))
	expired.MaxAge = -1
	http.SetCookie(w, expired)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// IndexPage handles GET /: the expense list for the requested date range,
// category and merchant.
func (s *Server) IndexPage(w http.ResponseWriter, r *http.Request) {
	results, err := s.results(r, filterFromQuery(r.URL.Query(), time.Now()))
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderPage(w, http.StatusOK, "index.html", pageData{
		Title:    "Expenses",
		SignedIn: true,
		Nav:      "expenses",
		Results:  results,
	})
}

// ResultsPartial handles GET /ui/expenses: the totals and table for a
// range and filters.
func (s *Server) ResultsPartial(w http.ResponseWriter, r *http.Request) {
	filter := filterFromQuery(r.URL.Query(), time.Now())
	results, err := s.results(r, filter)
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	// Keep the address bar in sync when a filter form is submitted.
	w.Header().Set("HX-Push-Url", "/?"+filter.query(filter.dateRange))
	s.renderPartial(w, http.StatusOK, "results", results)
}

// NewExpenseForm handles GET /ui/expenses/new.
func (s *Server) NewExpenseForm(w http.ResponseWriter, r *http.Request) {
	s.renderPartial(w, http.StatusOK, "form", formData{
		Range:  filterFromQuery(r.URL.Query(), time.Now()),
		Values: formValues{Currency: string(models.CurrencyEUR), Category: string(models.CategoryFood)},
	})
}

// EditExpenseForm handles GET /ui/expenses/{id}/edit.
func (s *Server) EditExpenseForm(w http.ResponseWriter, r *http.Request) {
	e, err := s.handler.GetExpense(r.Context(), r.PathValue("id"))
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderPartial(w, http.StatusOK, "form", formData{
		Id:     e.Id,
		Range:  filterFromQuery(r.URL.Query(), time.Now()),
		Values: formValuesFrom(e),
	})
}

// CreateExpenseSubmit handles POST /ui/expenses.
func (s *Server) CreateExpenseSubmit(w http.ResponseWriter, r *http.Request) {
	s.saveExpense(w, r, "")
}

// UpdateExpenseSubmit handles POST /ui/expenses/{id}.
func (s *Server) UpdateExpenseSubmit(w http.ResponseWriter, r *http.Request) {
	s.saveExpense(w, r, r.PathValue("id"))
}

// DeleteExpenseSubmit handles POST /ui/expenses/{id}/delete.
func (s *Server) DeleteExpenseSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err := s.handler.DeleteExpense(r.Context(), r.PathValue("id")); err != nil && !errors.Is(err, pkg.ErrExpenseNotFound) {
		s.renderUiError(w, r, err)
		return
	}
	s.renderSaved(w, r, filterFromQuery(r.PostForm, time.Now()))
}

// saveExpense creates (id == "") or updates an expense from the form. On a
// validation error the form is shown again with the problems.
func (s *Server) saveExpense(w http.ResponseWriter, r *http.Request, id string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	rng := filterFromQuery(r.PostForm, time.Now())
	values := formValuesFromForm(r.PostForm)

	input, fieldErrors := values.toInput()
	var err error
	if len(fieldErrors) == 0 {
		if id == "" {
			_, err = s.handler.CreateExpense(r.Context(), input)
		} else {
			_, err = s.handler.UpdateExpense(r.Context(), id, input)
		}
	}

	var appErr *pkg.Error
	if errors.As(err, &appErr) && appErr.Code == "validation_failed" {
		fieldErrors = appErr.Fields
		err = nil
	}
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	if len(fieldErrors) > 0 {
		// 200 so HTMX swaps the form back in with the errors.
		s.renderPartial(w, http.StatusOK, "form", formData{Id: id, Range: rng, Values: values, Errors: fieldErrors})
		return
	}
	s.renderSaved(w, r, rng)
}

// renderSaved closes the editor and refreshes the results in one response.
func (s *Server) renderSaved(w http.ResponseWriter, r *http.Request, rng listFilter) {
	results, err := s.results(r, rng)
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderPartial(w, http.StatusOK, "saved", results)
}

// requireSession lets the request through only with a valid session cookie.
// Without one, pages redirect to /login; HTMX requests are told to do so.
// State-changing requests must also come from this origin.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !sameOrigin(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		if !s.signedIn(r) {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) cookieName() string {
	if s.secureCookies {
		return "__Host-" + sessionCookieName
	}
	return sessionCookieName
}

func (s *Server) signedIn(r *http.Request) bool {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return false
	}
	ok, err := s.handler.ValidateSession(r.Context(), c.Value)
	if err != nil {
		slog.ErrorContext(r.Context(), "session check failed", "error", err)
		return false
	}
	return ok
}

// sessionCookie is HttpOnly so scripts can't read it, and SameSite=Strict so
// browsers never send it with requests started by other sites.
func (s *Server) sessionCookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     s.cookieName(),
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteStrictMode,
	}
}

// crossOrigin is the standard library's CSRF check. It trusts the browser's
// Sec-Fetch-Site header and falls back to comparing Origin with Host. Origin
// alone isn't enough: with Referrer-Policy: no-referrer, browsers send
// "Origin: null" even for same-origin form posts.
var crossOrigin = http.NewCrossOriginProtection()

// sameOrigin reports whether a request may change state: safe methods and
// non-browser requests pass, browser requests must come from this origin.
// The session cookie is still required either way.
func sameOrigin(r *http.Request) bool {
	return crossOrigin.Check(r) == nil
}

func (s *Server) results(r *http.Request, filter listFilter) (resultsData, error) {
	from, _ := time.Parse(time.DateOnly, filter.From)
	to, _ := time.Parse(time.DateOnly, filter.To)
	expenses, err := s.handler.ListExpenses(r.Context(), models.ExpenseFilter{
		DateFrom: from,
		DateTo:   to,
		Category: models.Category(filter.Category),
		Merchant: filter.Merchant,
	})
	if err != nil {
		return resultsData{}, err
	}
	results := buildResults(filter.dateRange, expenses)
	results.Filter = filter
	results.Presets = presets(time.Now(), filter.dateRange)
	for i := range results.Presets {
		// Switching the date range keeps the category and merchant filters.
		results.Presets[i].Query = template.URL(filter.query(results.Presets[i].dateRange))
	}
	return results, nil
}

func (s *Server) renderPage(w http.ResponseWriter, status int, page string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", uiCsp)
	w.WriteHeader(status)
	if err := pages[page].ExecuteTemplate(w, page, data); err != nil {
		slog.Error("render page failed", "page", page, "error", err)
	}
}

func (s *Server) renderPartial(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", uiCsp)
	w.WriteHeader(status)
	if err := partials.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("render partial failed", "partial", name, "error", err)
	}
}

func (s *Server) renderUiError(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := http.StatusInternalServerError, "Something went wrong."
	if appErr, ok := errors.AsType[*pkg.Error](err); ok {
		status, msg = appErr.Status, appErr.Message
	} else {
		slog.ErrorContext(r.Context(), "ui request failed", "error", err)
	}
	http.Error(w, msg, status)
}

type pageData struct {
	Title    string
	SignedIn bool
	// Status is the HTTP status shown on the error page.
	Status int
	// Nav marks the active top-bar link: "expenses" or "apps".
	Nav     string
	Error   string
	Results resultsData
	Apps    []appRow
}

// appRow is a connected app as shown on the apps page.
type appRow struct {
	Id, Name, Initial, RedirectHost, ConnectedAt, LastActiveAt string
}

func appRows(apps []models.ConnectedApp) []appRow {
	out := make([]appRow, 0, len(apps))
	for _, a := range apps {
		host := ""
		if len(a.Client.RedirectUris) > 0 {
			if u, err := url.Parse(a.Client.RedirectUris[0]); err == nil && u.Host != "" {
				host = u.Host
			} else {
				host = a.Client.RedirectUris[0]
			}
		}
		name := a.Client.Name
		if name == "" {
			name = "Unnamed client"
		}
		out = append(out, appRow{
			Id:           a.Client.Id,
			Name:         name,
			Initial:      string([]rune(name)[:1]),
			RedirectHost: host,
			ConnectedAt:  a.Client.CreatedAt.Format("2 Jan 2006"),
			LastActiveAt: a.LastActiveAt.Format("2 Jan 2006 15:04 MST"),
		})
	}
	return out
}

// AppsPage handles GET /apps: the apps signed in through OAuth.
func (s *Server) AppsPage(w http.ResponseWriter, r *http.Request) {
	apps, err := s.handler.ListConnectedApps(r.Context())
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderPage(w, http.StatusOK, "apps.html", pageData{Title: "Connected apps", SignedIn: true, Nav: "apps", Apps: appRows(apps)})
}

// RevokeAppSubmit handles POST /ui/apps/{id}/revoke. The empty response
// removes the app's row.
func (s *Server) RevokeAppSubmit(w http.ResponseWriter, r *http.Request) {
	if err := s.handler.RevokeApp(r.Context(), r.PathValue("id")); err != nil && !errors.Is(err, pkg.ErrAppNotFound) {
		s.renderUiError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// dateRange holds YYYY-MM-DD bounds; an empty bound is open.
type dateRange struct {
	From string
	To   string
}

// Label describes the range for the heading above the results.
func (r dateRange) Label() string {
	switch {
	case r.From == "" && r.To == "":
		return "All expenses"
	case r.From == "":
		return "Until " + r.To
	case r.To == "":
		return "From " + r.From
	case r.From == r.To:
		return r.From
	}
	return r.From + " – " + r.To
}

// rangeFromQuery reads from/to. Invalid dates are dropped. With neither
// parameter present, the range is the current month.
func rangeFromQuery(v url.Values, now time.Time) dateRange {
	if !v.Has("from") && !v.Has("to") {
		return currentMonth(now)
	}
	valid := func(s string) string {
		if _, err := time.Parse(time.DateOnly, s); err != nil {
			return ""
		}
		return s
	}
	return dateRange{From: valid(v.Get("from")), To: valid(v.Get("to"))}
}

func currentMonth(now time.Time) dateRange {
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return dateRange{From: first.Format(time.DateOnly), To: first.AddDate(0, 1, -1).Format(time.DateOnly)}
}

// listFilter is what the expense list shows: a date range plus optional
// category and merchant filters.
type listFilter struct {
	dateRange
	Category string
	Merchant string
}

// filterFromQuery reads from/to (see rangeFromQuery), category and merchant.
// An unknown category is dropped; the merchant search is trimmed and cut to
// the merchant length limit.
func filterFromQuery(v url.Values, now time.Time) listFilter {
	f := listFilter{dateRange: rangeFromQuery(v, now)}
	if c := models.Category(v.Get("category")); c.IsValid() {
		f.Category = string(c)
	}
	f.Merchant = strings.TrimSpace(v.Get("merchant"))
	if r := []rune(f.Merchant); len(r) > models.MaxMerchantLength {
		f.Merchant = string(r[:models.MaxMerchantLength])
	}
	return f
}

// Active reports whether a category or merchant filter is set.
func (f listFilter) Active() bool { return f.Category != "" || f.Merchant != "" }

// query encodes rng with this filter's category and merchant, for links.
func (f listFilter) query(rng dateRange) string {
	v := url.Values{"from": {rng.From}, "to": {rng.To}}
	if f.Category != "" {
		v.Set("category", f.Category)
	}
	if f.Merchant != "" {
		v.Set("merchant", f.Merchant)
	}
	return v.Encode()
}

// ClearQuery keeps the date range and drops the other filters.
func (f listFilter) ClearQuery() template.URL {
	return template.URL(listFilter{dateRange: f.dateRange}.query(f.dateRange))
}

type preset struct {
	Label string
	dateRange
	// Active is true when the preset is the range being shown.
	Active bool
	// Query is the URL query for this range with the current filters.
	Query template.URL
}

// presets returns the quick ranges, marking the one equal to current.
func presets(now time.Time, current dateRange) []preset {
	thisMonth := currentMonth(now)
	firstOfMonth, _ := time.Parse(time.DateOnly, thisMonth.From)
	lastMonthStart := firstOfMonth.AddDate(0, -1, 0)
	out := []preset{
		{Label: "This month", dateRange: thisMonth},
		{Label: "Last month", dateRange: dateRange{lastMonthStart.Format(time.DateOnly), firstOfMonth.AddDate(0, 0, -1).Format(time.DateOnly)}},
		{Label: "This year", dateRange: dateRange{fmt.Sprintf("%d-01-01", now.Year()), fmt.Sprintf("%d-12-31", now.Year())}},
		{Label: "All time", dateRange: dateRange{}},
	}
	for i := range out {
		out[i].Active = out[i].dateRange == current
	}
	return out
}

type resultsData struct {
	Range   dateRange
	Filter  listFilter
	Presets []preset
	Rows    []expenseRow
	Days    []dayGroup
	Totals  []currencyTotal
}

// dayGroup is the expenses of one local date, newest first.
type dayGroup struct {
	Label string
	Rows  []expenseRow
}

type expenseRow struct {
	Id, Date, Time, Offset, Amount, Currency, Symbol, Category, Merchant, Note string
}

type currencyTotal struct {
	Currency   string
	Symbol     string
	Total      string
	Count      int
	Categories []categoryTotal
}

type categoryTotal struct {
	Category string
	Total    string
	Percent  int
}

// buildResults turns expenses (oldest first) into rows (newest first) and
// per-currency totals with a category breakdown, largest category first.
// Sums use exact decimals; different currencies are never added together.
func buildResults(rng dateRange, expenses []models.Expense) resultsData {
	out := resultsData{Range: rng}

	type sums struct {
		total      decimal.Decimal
		count      int
		categories map[string]decimal.Decimal
	}
	byCurrency := map[string]*sums{}

	for _, e := range slices.Backward(expenses) {
		row := expenseRow{
			Id:       e.Id,
			Date:     e.OccurredAt.Format(time.DateOnly),
			Time:     e.OccurredAt.Format("15:04"),
			Offset:   e.OccurredAt.Format("-07:00"),
			Amount:   e.Amount,
			Currency: string(e.Currency),
			Symbol:   e.Currency.Symbol(),
			Category: string(e.Category),
			Merchant: deref(e.Merchant),
			Note:     deref(e.Note),
		}
		out.Rows = append(out.Rows, row)

		label := e.OccurredAt.Format("Monday, 2 January 2006")
		if n := len(out.Days); n == 0 || out.Days[n-1].Label != label {
			out.Days = append(out.Days, dayGroup{Label: label})
		}
		out.Days[len(out.Days)-1].Rows = append(out.Days[len(out.Days)-1].Rows, row)

		amount, err := decimal.NewFromString(e.Amount)
		if err != nil {
			continue // stored amounts are validated; skip anything unreadable
		}
		cur := string(e.Currency)
		if byCurrency[cur] == nil {
			byCurrency[cur] = &sums{categories: map[string]decimal.Decimal{}}
		}
		s := byCurrency[cur]
		s.total = s.total.Add(amount)
		s.count++
		s.categories[string(e.Category)] = s.categories[string(e.Category)].Add(amount)
	}

	for _, cur := range models.Currencies {
		s, ok := byCurrency[string(cur)]
		if !ok {
			continue
		}
		t := currencyTotal{Currency: string(cur), Symbol: cur.Symbol(), Total: s.total.StringFixed(2), Count: s.count}
		for cat, sum := range s.categories {
			pct := 0
			if s.total.IsPositive() {
				pct = int(sum.Div(s.total).Mul(decimal.NewFromInt(100)).Round(0).IntPart())
			}
			t.Categories = append(t.Categories, categoryTotal{Category: cat, Total: sum.StringFixed(2), Percent: pct})
		}
		slices.SortFunc(t.Categories, func(a, b categoryTotal) int {
			da, _ := decimal.NewFromString(a.Total)
			db, _ := decimal.NewFromString(b.Total)
			if c := db.Cmp(da); c != 0 {
				return c
			}
			return strings.Compare(a.Category, b.Category)
		})
		out.Totals = append(out.Totals, t)
	}
	return out
}

type formData struct {
	Id     string
	Range  listFilter
	Values formValues
	Errors map[string]string
}

// currencyOption is one entry of the form's currency select box.
type currencyOption struct {
	Code   string
	Symbol string
}

// Currencies and Categories feed the form's select boxes.
func (formData) Currencies() []currencyOption {
	out := make([]currencyOption, len(models.Currencies))
	for i, c := range models.Currencies {
		out[i] = currencyOption{Code: string(c), Symbol: c.Symbol()}
	}
	return out
}
func (formData) Categories() []string { return toStrings(models.Categories) }

// CategoryOptions feeds the category filter's select box.
func (resultsData) CategoryOptions() []string { return toStrings(models.Categories) }

func toStrings[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

// formValues are the raw form fields, kept as typed so a rejected form can
// be shown again unchanged.
type formValues struct {
	Amount        string
	Currency      string
	OccurredLocal string // YYYY-MM-DDTHH:MM[:SS] in the expense's own time zone
	TzOffset      string // minutes east of UTC
	Category      string
	Merchant      string
	Note          string
}

func formValuesFromForm(v url.Values) formValues {
	return formValues{
		Amount:        v.Get("amount"),
		Currency:      v.Get("currency"),
		OccurredLocal: v.Get("occurred_local"),
		TzOffset:      v.Get("tz_offset"),
		Category:      v.Get("category"),
		Merchant:      v.Get("merchant"),
		Note:          v.Get("note"),
	}
}

func formValuesFrom(e models.Expense) formValues {
	_, offset := e.OccurredAt.Zone()
	return formValues{
		Amount:        e.Amount,
		Currency:      string(e.Currency),
		OccurredLocal: e.OccurredAt.Format("2006-01-02T15:04:05"),
		TzOffset:      strconv.Itoa(offset / 60),
		Category:      string(e.Category),
		Merchant:      deref(e.Merchant),
		Note:          deref(e.Note),
	}
}

// toInput converts the form into the handler's input map. Every field is
// sent, so an update replaces all of them; blank merchant or note clears it.
func (v formValues) toInput() (map[string]any, map[string]string) {
	occurredAt, err := v.occurredAt()
	if err != nil {
		return nil, map[string]string{"occurred_at": err.Error()}
	}
	return map[string]any{
		"amount":      v.Amount,
		"currency":    v.Currency,
		"occurred_at": occurredAt,
		"category":    v.Category,
		"merchant":    v.Merchant,
		"note":        v.Note,
	}, nil
}

// occurredAt combines the local date-time and the offset into RFC 3339.
func (v formValues) occurredAt() (string, error) {
	minutes, err := strconv.Atoi(v.TzOffset)
	if err != nil || minutes < -14*60 || minutes > 14*60 {
		return "", errors.New("time zone offset is missing or invalid")
	}
	loc := time.FixedZone("", minutes*60)

	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, v.OccurredLocal, loc); err == nil {
			return t.Format(time.RFC3339), nil
		}
	}
	return "", errors.New("must be a date and time")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

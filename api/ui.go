package api

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
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

// uiCsp allows only same-origin scripts and styles; nothing inline. blob:
// images are the receipt photo previewed from the user's own device.
const uiCsp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
	"frame-ancestors 'none'; form-action 'self'; base-uri 'none'"

// templateFuncs are available in every template.
var templateFuncs = template.FuncMap{
	// title upper-cases the first letter: "groceries" → "Groceries".
	// dict builds a map from key/value pairs, to pass several values to a
	// sub-template: {{template "x" (dict "A" .A "B" $.B)}}.
	"dict": func(pairs ...any) map[string]any {
		m := make(map[string]any, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			key, _ := pairs[i].(string)
			m[key] = pairs[i+1]
		}
		return m
	},
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
	for _, page := range []string{"index.html", "login.html", "signup.html", "settings.html", "apps.html", "analysis.html", "authorize.html", "error.html"} {
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
	data := pageData{Title: "Sign in", SignupEnabled: s.handler.SignupEnabled()}
	if r.URL.Query().Has("deleted") {
		data.Notice = "Your account and all its data were deleted."
	}
	s.renderPage(w, http.StatusOK, "login.html", data)
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

	email := r.PostForm.Get("email")
	page := func(status int, msg string) {
		s.renderPage(w, status, "login.html", pageData{Title: "Sign in", Error: msg, Form: map[string]string{"email": email}, SignupEnabled: s.handler.SignupEnabled()})
	}
	token, expiresAt, err := s.handler.Login(r.Context(), email, r.PostForm.Get("password"))
	switch {
	case errors.Is(err, handler.ErrWrongPassword):
		page(http.StatusUnauthorized, "Wrong email or password.")
		return
	case errors.Is(err, handler.ErrTooManyLogins):
		page(http.StatusTooManyRequests, "Too many failed sign-ins for this email. Try again in 15 minutes.")
		return
	case err != nil:
		page(http.StatusInternalServerError, "Something went wrong. Try again.")
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
	s.renderSignedIn(w, r, http.StatusOK, "index.html", pageData{
		Title:     "Expenses",
		Nav:       "expenses",
		Results:   results,
		Assistant: s.handler.ScanEnabled(),
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
	s.renderForm(w, r, http.StatusOK, formData{
		Range:       filterFromQuery(r.URL.Query(), time.Now()),
		Values:      formValues{Category: string(models.CategoryFood)},
		Symbol:      s.currencySymbol(r),
		ScanEnabled: s.handler.ScanEnabled(),
	})
}

// EditExpenseForm handles GET /ui/expenses/{id}/edit.
func (s *Server) EditExpenseForm(w http.ResponseWriter, r *http.Request) {
	e, err := s.handler.GetExpense(r.Context(), r.PathValue("id"))
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderForm(w, r, http.StatusOK, formData{
		Id:         e.Id,
		Range:      filterFromQuery(r.URL.Query(), time.Now()),
		Values:     formValuesFrom(e),
		Symbol:     s.currencySymbol(r),
		HasReceipt: s.hasReceipt(r, e.Id),
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
	// A new expense may carry its scanned receipt, so the form can be
	// multipart.
	if err := r.ParseMultipartForm(handler.MaxReceiptBytes); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			http.Error(w, "the receipt is too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	rng := filterFromExpenseForm(r.PostForm, time.Now())
	values := formValuesFromForm(r.PostForm)
	receipt, err := uploadedFile(r, "receipt")
	if err != nil {
		http.Error(w, "the receipt couldn't be read", http.StatusBadRequest)
		return
	}

	input, fieldErrors := values.toInput()
	if len(receipt) > 0 && id == "" {
		if _, err := handler.CheckReceiptImage(receipt); err != nil {
			if appErr, ok := errors.AsType[*pkg.Error](err); ok {
				if fieldErrors == nil {
					fieldErrors = map[string]string{}
				}
				fieldErrors["receipt"] = "The receipt photo " + appErr.Fields["receipt"] + "."
			}
		}
	}
	var created models.Expense
	if len(fieldErrors) == 0 {
		if id == "" {
			created, err = s.handler.CreateExpense(r.Context(), input)
		} else {
			_, err = s.handler.UpdateExpense(r.Context(), id, input)
		}
	}
	if err == nil && len(fieldErrors) == 0 {
		// The expense is saved; a receipt that fails to save now (the image
		// was checked above) is logged rather than undoing it.
		switch {
		case id == "" && len(receipt) > 0:
			if err := s.handler.AttachReceipt(r.Context(), created.Id, receipt); err != nil {
				slog.ErrorContext(r.Context(), "keep receipt failed", "expense_id", created.Id, "error", err)
			}
		case id != "" && r.PostForm.Get("remove_receipt") == "1":
			if err := s.handler.RemoveReceipt(r.Context(), id); err != nil {
				slog.ErrorContext(r.Context(), "remove receipt failed", "expense_id", id, "error", err)
			}
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
		s.renderForm(w, r, http.StatusOK, formData{
			Id: id, Range: rng, Values: values, Errors: fieldErrors, Symbol: s.currencySymbol(r),
			ScanEnabled: id == "" && s.handler.ScanEnabled(),
			// The page script puts the scanned photo back into the form.
			Scanned:    id == "" && len(receipt) > 0,
			HasReceipt: id != "" && s.hasReceipt(r, id),
		})
		return
	}
	s.renderSaved(w, r, rng)
}

// renderForm renders the add/edit form with the user's categories. A new
// expense without a category (or with one the user doesn't have) starts on
// "food" if they have it, else their first category.
func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, status int, data formData) {
	categories, err := s.handler.CategoryNames(r.Context())
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	data.Categories = toStrings(categories)
	if data.FromDraft && !models.HasCategory(categories, models.Category(data.Values.Category)) {
		// Left for the user to choose.
		data.Values.Category = ""
	} else if data.Id == "" && !models.HasCategory(categories, models.Category(data.Values.Category)) {
		data.Values.Category = ""
		if models.HasCategory(categories, models.CategoryFood) {
			data.Values.Category = string(models.CategoryFood)
		} else if len(categories) > 0 {
			data.Values.Category = string(categories[0])
		}
	}
	s.renderPartial(w, status, "form", data)
}

// uploadedFile returns the bytes of the named file field, or nil if none
// was sent.
func uploadedFile(r *http.Request, field string) ([]byte, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	file, _, err := r.FormFile(field)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, handler.MaxReceiptBytes+1))
}

// hasReceipt reports whether the user's expense has a receipt (false if it
// can't be told; already logged).
func (s *Server) hasReceipt(r *http.Request, id string) bool {
	ids, err := s.handler.ReceiptIds(r.Context())
	return err == nil && ids[id]
}

// currencySymbol is the display currency's symbol for forms. It falls back
// to the default currency if the setting can't be read (already logged).
func (s *Server) currencySymbol(r *http.Request) string {
	c, err := s.handler.DisplayCurrency(r.Context())
	if err != nil {
		return models.DefaultCurrency.Symbol()
	}
	return c.Symbol()
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
		uid := s.sessionUser(r)
		if uid == "" {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		// Everything downstream acts as this user.
		next(w, r.WithContext(pkg.WithUserId(r.Context(), uid)))
	}
}

func (s *Server) cookieName() string {
	if s.secureCookies {
		return "__Host-" + sessionCookieName
	}
	return sessionCookieName
}

func (s *Server) signedIn(r *http.Request) bool { return s.sessionUser(r) != "" }

// sessionUser returns the user the session cookie belongs to, or "".
func (s *Server) sessionUser(r *http.Request) string {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return ""
	}
	uid, ok, err := s.handler.ValidateSession(r.Context(), c.Value)
	if err != nil {
		slog.ErrorContext(r.Context(), "session check failed", "error", err)
		return ""
	}
	if !ok {
		return ""
	}
	return uid
}

// renderSignedIn renders a page for the signed-in user, with their profile
// menu in the top bar.
func (s *Server) renderSignedIn(w http.ResponseWriter, r *http.Request, status int, page string, data pageData) {
	user, err := s.handler.CurrentUser(r.Context())
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	data.SignedIn = true
	data.Profile = &user
	s.renderPage(w, status, page, data)
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
	categories, err := s.handler.CategoryNames(r.Context())
	if err != nil {
		return resultsData{}, err
	}
	// A category the user doesn't have doesn't filter.
	if !models.HasCategory(categories, models.Category(filter.Category)) {
		filter.Category = ""
	}
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
	currency, err := s.handler.DisplayCurrency(r.Context())
	if err != nil {
		return resultsData{}, err
	}
	results := buildResults(filter.dateRange, expenses, currency)
	receipts, err := s.handler.ReceiptIds(r.Context())
	if err != nil {
		return resultsData{}, err
	}
	for _, day := range results.Days {
		for i := range day.Rows {
			day.Rows[i].HasReceipt = receipts[day.Rows[i].Id]
		}
	}
	results.CategoryOptions = toStrings(categories)
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
	// Nav marks the active page: "expenses", "analysis", "apps" or "settings".
	Nav      string
	Error    string
	Results  resultsData
	Apps     []appRow
	Analysis analysisData
	// Authorize is the OAuth login page; nil on its error page.
	Authorize *loginPage
	// Profile is the signed-in user, for the profile menu in the top bar.
	Profile *models.User
	// SignupEnabled shows the sign-up link on the sign-in page.
	SignupEnabled bool
	// Form holds submitted values to show again, keyed by field name.
	Form map[string]string
	// Errors maps form fields to what's wrong with them.
	Errors map[string]string
	// Notice is a success message, e.g. "Saved."
	Notice string
	// Assistant shows the floating composer (type, say or snap an expense).
	Assistant bool
	// Section is the part of the page a form was sent from; its messages
	// are shown there and it gets the focus (so the browser scrolls to it).
	Section string
	// Account is the signed-in user on the account page.
	Account *accountView
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
	s.renderSignedIn(w, r, http.StatusOK, "apps.html", pageData{Title: "Connected apps", Nav: "apps", Apps: appRows(apps)})
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
	if c, problem := handler.NormalizeCategory(v.Get("category")); problem == "" {
		f.Category = string(c)
	}
	f.Merchant = strings.TrimSpace(v.Get("merchant"))
	if r := []rune(f.Merchant); len(r) > models.MaxMerchantLength {
		f.Merchant = string(r[:models.MaxMerchantLength])
	}
	return f
}

// filterFromExpenseForm reads the list's filters from the add/edit form.
// The expense has category and merchant fields of its own, so the form
// sends the list's as filter_category and filter_merchant.
func filterFromExpenseForm(v url.Values, now time.Time) listFilter {
	q := url.Values{"category": {v.Get("filter_category")}, "merchant": {v.Get("filter_merchant")}}
	for _, key := range []string{"from", "to"} {
		if v.Has(key) {
			q.Set(key, v.Get(key))
		}
	}
	return filterFromQuery(q, now)
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
	// Summary is nil when there are no expenses.
	Summary *summaryTotal
	// Currency is the display currency; Symbol is its symbol, e.g. "€".
	Currency models.Currency
	Symbol   string
	// CategoryOptions are the user's categories, for the filter.
	CategoryOptions []string
}

// dayGroup is the expenses of one local date, newest first.
type dayGroup struct {
	Label string
	Rows  []expenseRow
}

type expenseRow struct {
	Id, Date, Time, Offset, Amount, Category, Merchant, Note string
	// HasReceipt shows the receipt as the row's thumbnail.
	HasReceipt bool
}

type summaryTotal struct {
	Total      string
	Count      int
	Categories []categoryTotal
}

// shownCategories is how many categories the summary lists before folding
// the rest behind "Show more". Folding a single category saves nothing, so
// up to one more than this is shown in full.
const shownCategories = 5

// Top is the categories listed up front, largest first.
func (s summaryTotal) Top() []categoryTotal {
	if len(s.Categories) <= shownCategories+1 {
		return s.Categories
	}
	return s.Categories[:shownCategories]
}

// Rest is the categories folded behind "Show more".
func (s summaryTotal) Rest() []categoryTotal {
	if len(s.Categories) <= shownCategories+1 {
		return nil
	}
	return s.Categories[shownCategories:]
}

type categoryTotal struct {
	Category string
	Total    string
	Percent  int
}

// buildResults turns expenses (oldest first) into rows (newest first) and a
// total with a category breakdown, largest category first. Sums use exact
// decimals. currency is only used for display; nothing is converted.
func buildResults(rng dateRange, expenses []models.Expense, currency models.Currency) resultsData {
	out := resultsData{Range: rng, Currency: currency, Symbol: currency.Symbol()}

	var total decimal.Decimal
	categories := map[string]decimal.Decimal{}
	for _, e := range slices.Backward(expenses) {
		row := expenseRow{
			Id:       e.Id,
			Date:     e.OccurredAt.Format(time.DateOnly),
			Time:     e.OccurredAt.Format("15:04"),
			Offset:   e.OccurredAt.Format("-07:00"),
			Amount:   e.Amount,
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
		total = total.Add(amount)
		categories[string(e.Category)] = categories[string(e.Category)].Add(amount)
	}

	if len(expenses) == 0 {
		return out
	}
	summary := &summaryTotal{Total: total.StringFixed(2), Count: len(expenses)}
	for cat, sum := range categories {
		pct := 0
		if total.IsPositive() {
			// At least 1, so a small category still shows a sliver of bar.
			pct = max(int(sum.Div(total).Mul(decimal.NewFromInt(100)).Round(0).IntPart()), 1)
		}
		summary.Categories = append(summary.Categories, categoryTotal{Category: cat, Total: sum.StringFixed(2), Percent: pct})
	}
	slices.SortFunc(summary.Categories, func(a, b categoryTotal) int {
		da, _ := decimal.NewFromString(a.Total)
		db, _ := decimal.NewFromString(b.Total)
		if c := db.Cmp(da); c != 0 {
			return c
		}
		return strings.Compare(a.Category, b.Category)
	})
	out.Summary = summary
	return out
}

type formData struct {
	Id     string
	Range  listFilter
	Values formValues
	Errors map[string]string
	// Symbol is the display currency's symbol, shown next to the amount.
	Symbol string
	// ScanEnabled shows the receipt scanner on a new expense. Scanned means
	// the values were read off a receipt and need checking; ScanError and
	// ScanWarnings say what went wrong or looks doubtful.
	ScanEnabled  bool
	Scanned      bool
	ScanError    string
	ScanWarnings []string
	// HasReceipt shows the kept receipt when editing, with a way to remove it.
	HasReceipt bool
	// FromDraft means the values were read by the model (from a receipt or
	// text): fields it wasn't sure of stay empty, the category included.
	// FromText is the message they were read from.
	FromDraft bool
	FromText  string
	// Categories are the user's, for the select box (see renderForm).
	Categories []string
}

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
	OccurredLocal string // YYYY-MM-DDTHH:MM[:SS] in the expense's own time zone
	TzOffset      string // minutes east of UTC
	Category      string
	Merchant      string
	Note          string
}

func formValuesFromForm(v url.Values) formValues {
	return formValues{
		Amount:        v.Get("amount"),
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

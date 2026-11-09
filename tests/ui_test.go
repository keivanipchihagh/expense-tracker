package tests

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// browser is an HTTP client with a cookie jar that doesn't follow redirects.
func browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func send(t *testing.T, c *http.Client, method, u string, form url.Values, headers map[string]string) (int, http.Header, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, u, body)
	require.NoError(t, err)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, string(raw)
}

func signIn(t *testing.T, s *testServer, c *http.Client) {
	t.Helper()
	status, header, _ := send(t, c, http.MethodPost, s.url+"/login", url.Values{"email": {s.email}, "password": {s.password}}, nil)
	require.Equal(t, http.StatusSeeOther, status)
	require.Equal(t, "/", header.Get("Location"))
}

func TestUI_RequiresSignIn(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)

	status, header, _ := send(t, c, http.MethodGet, s.url+"/", nil, nil)
	assert.Equal(t, http.StatusSeeOther, status)
	assert.Equal(t, "/login", header.Get("Location"))

	status, header, _ = send(t, c, http.MethodGet, s.url+"/ui/expenses", nil, map[string]string{"HX-Request": "true"})
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Equal(t, "/login", header.Get("HX-Redirect"), "HTMX requests are told to go to the login page")

	status, _, page := send(t, c, http.MethodGet, s.url+"/login", nil, nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `name="password"`)

	status, _, _ = send(t, c, http.MethodGet, s.url+"/static/htmx.min.js", nil, nil)
	assert.Equal(t, http.StatusOK, status, "static assets are public")
}

func TestUI_Login(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)

	status, _, page := send(t, c, http.MethodPost, s.url+"/login", url.Values{"email": {email}, "password": {"wrong"}}, nil)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, page, "Wrong email or password.")

	status, header, _ := send(t, c, http.MethodPost, s.url+"/login", url.Values{"email": {email}, "password": {password}}, nil)
	require.Equal(t, http.StatusSeeOther, status)

	cookie := header.Get("Set-Cookie")
	assert.Contains(t, cookie, "et_session=")
	assert.Contains(t, cookie, "HttpOnly")
	assert.Contains(t, cookie, "SameSite=Strict")

	status, headers, page := send(t, c, http.MethodGet, s.url+"/", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, "Add expense")
	assert.Contains(t, page, "No expenses in this range.")
	assert.Contains(t, headers.Get("Content-Security-Policy"), "script-src 'self'")

	status, header, _ = send(t, c, http.MethodGet, s.url+"/login", nil, nil)
	assert.Equal(t, http.StatusSeeOther, status, "signed-in users skip the login page")
	assert.Equal(t, "/", header.Get("Location"))
}

func TestUI_Layout(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	toggle := `role="switch" aria-checked="false" aria-label="Dark theme" title="Dark theme" data-theme-toggle`

	status, _, page := send(t, c, http.MethodGet, s.url+"/login", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `<script src="/static/theme.js"></script>`, "loaded without defer so the theme is set before paint")
	assert.Contains(t, page, toggle, "signed-out pages have the switch")
	assert.Contains(t, page, `<a href="https://github.com/keivanipchihagh/expense-tracker" rel="noopener noreferrer">GitHub</a>`, "the footer links to the repository")

	status, header, script := send(t, c, http.MethodGet, s.url+"/static/theme.js", nil, nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, header.Get("Content-Type"), "javascript")
	assert.Contains(t, script, "data-theme-toggle")

	signIn(t, s, c)
	status, _, page = send(t, c, http.MethodGet, s.url+"/", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, toggle, "signed-in pages have the switch")
}

func TestUI_ExpenseLifecycle(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)
	hx := map[string]string{"HX-Request": "true"}
	rangeQuery := "from=2026-11-01&to=2026-11-30"

	// The new-expense form
	status, _, form := send(t, c, http.MethodGet, s.url+"/ui/expenses/new?"+rangeQuery, nil, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, form, `hx-post="/ui/expenses"`)
	assert.Contains(t, form, `<span class="amount-symbol" aria-hidden="true">€</span>`, "the amount shows the display currency")
	assert.NotContains(t, form, `name="currency"`, "there is no per-expense currency")
	assert.Contains(t, form, `<label for="occurred-local">When</label>`)
	assert.Contains(t, form, `<button type="button" data-days-ago="0" aria-pressed="false">Today</button>`)
	assert.Contains(t, form, `<button type="button" data-days-ago="1" aria-pressed="false">Yesterday</button>`)
	assert.Contains(t, form, `<button type="button" data-days-ago="2" aria-pressed="false">2 days ago</button>`)

	// A rejected submission shows the form again with the problem
	bad := url.Values{
		"from": {"2026-11-01"}, "to": {"2026-11-30"}, "tz_offset": {"60"},
		"amount": {"1.234"}, "occurred_local": {"2026-11-07T13:20"}, "category": {"food"},
	}
	status, _, form = send(t, c, http.MethodPost, s.url+"/ui/expenses", bad, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, form, "must have at most 2 decimal places")
	assert.Contains(t, form, `value="1.234"`, "what you typed is kept")

	// A valid submission closes the editor and refreshes the results
	good := url.Values{
		"from": {"2026-11-01"}, "to": {"2026-11-30"}, "tz_offset": {"60"},
		"amount": {"12.5"}, "occurred_local": {"2026-11-07T13:20"},
		"category": {"food"}, "merchant": {"Rewe"}, "note": {"team lunch"},
	}
	status, _, saved := send(t, c, http.MethodPost, s.url+"/ui/expenses", good, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, saved, `<div id="editor" hx-swap-oob="true"></div>`)
	assert.Contains(t, saved, "Rewe")
	assert.Contains(t, saved, "€12.50")
	assert.Contains(t, saved, `13:20 <span class="offset">&#43;01:00</span>`, "+ is HTML-escaped; browsers show +01:00")
	assert.Contains(t, saved, "Saturday, 7 November 2026", "expenses are grouped by local date")

	// The created expense, via the API
	s.login(t)
	var listed []map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses", "", &listed))
	require.Len(t, listed, 1)
	id := listed[0]["id"].(string)
	assert.Equal(t, "2026-11-07T13:20:00+01:00", listed[0]["occurred_at"])

	// Edit: the form is prefilled, and saving changes the expense
	status, _, form = send(t, c, http.MethodGet, s.url+"/ui/expenses/"+id+"/edit?"+rangeQuery, nil, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, form, `value="2026-11-07T13:20:00"`)
	assert.Contains(t, form, `name="tz_offset" value="60"`)

	edit := url.Values{}
	maps.Copy(edit, good)
	edit.Set("amount", "20")
	edit.Set("merchant", "")
	status, _, saved = send(t, c, http.MethodPost, s.url+"/ui/expenses/"+id, edit, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, saved, "€20.00")
	assert.NotContains(t, saved, "Rewe", "a blank merchant clears it")

	// Delete
	status, _, saved = send(t, c, http.MethodPost, s.url+"/ui/expenses/"+id+"/delete", url.Values{"from": {"2026-11-01"}, "to": {"2026-11-30"}}, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, saved, "No expenses in this range.")

	assert.Equal(t, []string{"create", "update", "delete"}, s.logActions(t))
}

func TestUI_TotalsOnPage(t *testing.T) {
	s := newTestServer(t)
	s.login(t)
	for _, body := range []string{
		`{"amount":"10","occurred_at":"2026-11-02T12:00:00+01:00","category":"food"}`,
		`{"amount":"30","occurred_at":"2026-11-03T12:00:00+01:00","category":"groceries"}`,
		`{"amount":"7.25","occurred_at":"2026-11-04T12:00:00-05:00","category":"transport"}`,
		`{"amount":"99","occurred_at":"2026-10-31T12:00:00+01:00","category":"travel"}`,
	} {
		require.Equal(t, http.StatusCreated, s.call(t, http.MethodPost, "/api/v1/expenses", body, nil))
	}

	c := browser(t)
	signIn(t, s, c)
	status, _, page := send(t, c, http.MethodGet, s.url+"/?from=2026-11-01&to=2026-11-30", nil, nil)
	require.Equal(t, http.StatusOK, status)

	assert.Contains(t, page, "€47.25", "one total over every expense")
	assert.Contains(t, page, "€7.25")
	assert.NotContains(t, page, "$7.25", "everything is shown in the display currency")
	assert.NotContains(t, page, "99.00", "October is outside the range")
	assert.Contains(t, page, `value="63"`, "groceries is 63% of spending")
	assert.NotContains(t, page, "breakdown-more", "three categories need no folding")
}

func TestUI_Filters(t *testing.T) {
	s := newTestServer(t)
	s.login(t)
	for _, body := range []string{
		`{"amount":"10","occurred_at":"2026-11-02T12:00:00+01:00","category":"groceries","merchant":"Rewe City"}`,
		`{"amount":"30","occurred_at":"2026-11-03T12:00:00+01:00","category":"groceries","merchant":"Aldi"}`,
		`{"amount":"5","occurred_at":"2026-11-04T12:00:00+01:00","category":"food","merchant":"REWE"}`,
	} {
		require.Equal(t, http.StatusCreated, s.call(t, http.MethodPost, "/api/v1/expenses", body, nil))
	}
	c := browser(t)
	signIn(t, s, c)
	hx := map[string]string{"HX-Request": "true"}

	// Both filters combine, and the totals follow them.
	status, header, page := send(t, c, http.MethodGet, s.url+"/ui/expenses?from=2026-11-01&to=2026-11-30&category=groceries&merchant=+rewe+", nil, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "/?category=groceries&from=2026-11-01&merchant=rewe&to=2026-11-30", header.Get("HX-Push-Url"))
	assert.Contains(t, page, "Rewe City")
	assert.NotContains(t, page, "Aldi")
	assert.NotContains(t, page, "€5.00")
	assert.Contains(t, page, "€10.00")
	assert.Contains(t, page, `<option value="groceries" selected>`)
	assert.Contains(t, page, `name="merchant" value="rewe"`)

	// Date presets and Clear keep or drop the filters.
	assert.Contains(t, page, `href="/?category=groceries&amp;from=&amp;merchant=rewe&amp;to="`, "All time keeps the filters")
	assert.Contains(t, page, `class="clear-filters" href="/?from=2026-11-01&amp;to=2026-11-30"`, "Clear keeps only the range")

	// The add form carries the filters so saving returns to the same view.
	status, _, form := send(t, c, http.MethodGet, s.url+"/ui/expenses/new?from=2026-11-01&to=2026-11-30&category=groceries&merchant=rewe", nil, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, form, `name="filter_category" value="groceries"`)
	assert.Contains(t, form, `name="filter_merchant" value="rewe"`, "named so they don't clash with the expense's own fields")

	// An unknown category is ignored rather than failing the page.
	status, _, page = send(t, c, http.MethodGet, s.url+"/?from=2026-11-01&to=2026-11-30&category=pets", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, "Aldi")

	status, _, page = send(t, c, http.MethodGet, s.url+"/?from=2026-11-01&to=2026-11-30&merchant=lidl", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, "No expenses match these filters.")
}

func TestUI_DisplayCurrency(t *testing.T) {
	s := newTestServer(t)
	s.login(t)
	require.Equal(t, http.StatusCreated, s.call(t, http.MethodPost, "/api/v1/expenses",
		`{"amount":"12.5","occurred_at":"2026-11-02T12:00:00+01:00","category":"food"}`, nil))
	c := browser(t)
	signIn(t, s, c)
	hx := map[string]string{"HX-Request": "true"}
	monthPage := s.url + "/?from=2026-11-01&to=2026-11-30"

	// EUR until the user picks another one, on the account page.
	_, _, page := send(t, c, http.MethodGet, monthPage, nil, nil)
	assert.Contains(t, page, "€12.50")
	assert.NotContains(t, page, `name="currency"`, "the picker lives on the settings page now")
	status, _, account := send(t, c, http.MethodGet, s.url+"/settings", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, account, `<option value="EUR" selected>EUR (€)</option>`)
	assert.Contains(t, account, `<option value="GBP" >GBP (£)</option>`)

	status, _, account = send(t, c, http.MethodPost, s.url+"/settings", url.Values{"currency": {"GBP"}}, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, account, "Saved.")
	assert.Contains(t, account, `<option value="GBP" selected>GBP (£)</option>`)

	_, _, page = send(t, c, http.MethodGet, monthPage, nil, nil)
	assert.Contains(t, page, "£12.50")
	assert.NotContains(t, page, "€12.50")
	_, _, results := send(t, c, http.MethodGet, s.url+"/ui/expenses?from=2026-11-01&to=2026-11-30", nil, hx)
	assert.Contains(t, results, "£12.50", "partials use it too")
	_, _, form := send(t, c, http.MethodGet, s.url+"/ui/expenses/new", nil, hx)
	assert.Contains(t, form, `<span class="amount-symbol" aria-hidden="true">£</span>`, "and the add form")

	// Nothing is converted: the stored amount is unchanged.
	var listed []map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses", "", &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, "12.50", listed[0]["amount"])

	// It's saved on the account, so another browser sees it.
	other := browser(t)
	signIn(t, s, other)
	_, _, page = send(t, other, http.MethodGet, monthPage, nil, nil)
	assert.Contains(t, page, "£12.50")

	// Unknown currencies are rejected and change nothing.
	status, _, account = send(t, c, http.MethodPost, s.url+"/settings", url.Values{"currency": {"XYZ"}}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	_, _, page = send(t, c, http.MethodGet, monthPage, nil, nil)
	assert.Contains(t, page, "£12.50")
}

func TestUI_SettingsNeedSessionAndSameOrigin(t *testing.T) {
	s := newTestServer(t)

	status, header, _ := send(t, browser(t), http.MethodGet, s.url+"/settings", nil, nil)
	assert.Equal(t, http.StatusSeeOther, status, "signed-out visitors go to the login page")
	assert.Equal(t, "/login", header.Get("Location"))
	status, _, _ = send(t, browser(t), http.MethodPost, s.url+"/settings", url.Values{"currency": {"GBP"}}, nil)
	assert.Equal(t, http.StatusSeeOther, status)

	c := browser(t)
	signIn(t, s, c)
	crossSite := map[string]string{"Sec-Fetch-Site": "cross-site"}
	status, _, _ = send(t, c, http.MethodPost, s.url+"/settings", url.Values{"first_name": {"Mallory"}}, crossSite)
	assert.Equal(t, http.StatusForbidden, status, "other sites can't change your profile")
	status, _, _ = send(t, c, http.MethodPost, s.url+"/settings/password", url.Values{"current_password": {password}, "new_password": {"attacker-chosen-1"}}, crossSite)
	assert.Equal(t, http.StatusForbidden, status, "or your password")

	_, _, account := send(t, c, http.MethodGet, s.url+"/settings", nil, nil)
	assert.NotContains(t, account, "Mallory", "unchanged")
}

func TestUI_Analysis(t *testing.T) {
	s := newTestServer(t)

	status, header, _ := send(t, browser(t), http.MethodGet, s.url+"/analysis", nil, nil)
	require.Equal(t, http.StatusSeeOther, status, "signed-out visitors go to the login page")
	assert.Equal(t, "/login", header.Get("Location"))

	c := browser(t)
	signIn(t, s, c)

	// No expenses yet: the filters show, the charts don't.
	status, header, page := send(t, c, http.MethodGet, s.url+"/analysis", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, header.Get("Content-Security-Policy"), "style-src 'self'", "charts need no inline styles")
	assert.Contains(t, page, `<a href="/analysis" class="active">Analysis</a>`)
	assert.Contains(t, page, "No expenses in this period.")
	assert.NotContains(t, page, "<svg class=\"chart\"")

	// Expenses this month, with the date taken from the clock like the page does.
	s.login(t)
	month := time.Now().Format("2006-01")
	for _, body := range []string{
		`{"amount":"1000","occurred_at":"` + month + `-01T09:00:00+01:00","category":"housing","merchant":"Landlord"}`,
		`{"amount":"30","occurred_at":"` + month + `-01T12:00:00+01:00","category":"groceries","merchant":"Rewe"}`,
		`{"amount":"20","occurred_at":"` + month + `-01T18:00:00+01:00","category":"groceries","merchant":"Rewe"}`,
		`{"amount":"7.5","occurred_at":"` + month + `-01T13:00:00+01:00","category":"food"}`,
	} {
		require.Equal(t, http.StatusCreated, s.call(t, http.MethodPost, "/api/v1/expenses", body, nil))
	}

	status, _, page = send(t, c, http.MethodGet, s.url+"/analysis?period=month", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `<p class="kpi-value">€1057.50</p>`)
	assert.Contains(t, page, "Where it went")
	assert.Contains(t, page, `<span class="cat">housing</span>`)
	assert.Contains(t, page, "Spending per day")
	assert.Contains(t, page, `<svg class="chart"`)
	assert.Contains(t, page, "Top merchants")
	assert.Contains(t, page, `<td class="num">2</td><td class="num">€50.00</td>`, "Rewe: two purchases")
	assert.Contains(t, page, "1 expense without a merchant isn't listed.")
	assert.NotContains(t, page, "Compared with", "nothing last month to compare against")
	assert.NotContains(t, page, "style=", "no inline styles anywhere")

	// Without housing.
	_, _, page = send(t, c, http.MethodGet, s.url+"/analysis?period=month&scope=no-housing", nil, nil)
	assert.Contains(t, page, `<p class="kpi-value">€57.50</p>`)
	assert.NotContains(t, page, `<span class="cat">housing</span>`)
	assert.Contains(t, page, `<option value="no-housing" selected>Everything except housing</option>`)
	assert.Contains(t, page, `href="/analysis?period=3m&amp;scope=no-housing"`, "period links keep the scope")

	// One category: no breakdown, and the totals follow.
	_, _, page = send(t, c, http.MethodGet, s.url+"/analysis?period=month&scope=groceries", nil, nil)
	assert.Contains(t, page, `<p class="kpi-value">€50.00</p>`)
	assert.NotContains(t, page, "Where it went")

	// The display currency applies here too.
	require.Equal(t, http.StatusOK, func() int {
		status, _, _ := send(t, c, http.MethodPost, s.url+"/settings", url.Values{"currency": {"USD"}}, nil)
		return status
	}())
	_, _, page = send(t, c, http.MethodGet, s.url+"/analysis?period=all", nil, nil)
	assert.Contains(t, page, `<p class="kpi-value">$1057.50</p>`)

	// Unknown parameters fall back instead of failing.
	status, _, page = send(t, c, http.MethodGet, s.url+"/analysis?period=decade&scope=pets", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `aria-current="true"
       hx-get="/analysis?period=month"`, "this month is selected")
}

func TestUI_Export(t *testing.T) {
	s := newTestServer(t)
	s.login(t)
	for _, body := range []string{
		`{"amount":"48.73","occurred_at":"2026-11-01T11:05:00+01:00","category":"groceries","merchant":"Rewe","note":"Weekly shop, with \"quotes\""}`,
		`{"amount":"6.75","occurred_at":"2026-11-04T12:20:00-05:00","category":"food","merchant":"=HYPERLINK(\"http://evil\")"}`,
		`{"amount":"99","occurred_at":"2026-10-31T12:00:00+01:00","category":"travel"}`,
	} {
		require.Equal(t, http.StatusCreated, s.call(t, http.MethodPost, "/api/v1/expenses", body, nil))
	}

	status, header, _ := send(t, browser(t), http.MethodGet, s.url+"/ui/export?format=csv", nil, nil)
	assert.Equal(t, http.StatusSeeOther, status, "signed-out visitors go to the login page")
	assert.Equal(t, "/login", header.Get("Location"))

	c := browser(t)
	signIn(t, s, c)
	november := "from=2026-11-01&to=2026-11-30"

	// The list links to both formats with its own dates and filters.
	_, _, page := send(t, c, http.MethodGet, s.url+"/?"+november+"&category=groceries", nil, nil)
	assert.Contains(t, page, `href="/ui/export?category=groceries&amp;format=csv&amp;from=2026-11-01&amp;to=2026-11-30" download>CSV</a>`)
	assert.Contains(t, page, `href="/ui/export?category=groceries&amp;format=json&amp;from=2026-11-01&amp;to=2026-11-30" download>JSON</a>`)
	assert.Contains(t, page, `<details class="export-menu" data-menu>`, "a dropdown in the filter row")
	assert.Contains(t, page, `<summary aria-label="Download" title="Download what the list shows">`, "opened by a download icon")

	_, _, empty := send(t, c, http.MethodGet, s.url+"/?from=2027-01-01&to=2027-01-31", nil, nil)
	assert.NotContains(t, empty, "export-menu", "nothing to export, no menu")

	// CSV
	status, header, body := send(t, c, http.MethodGet, s.url+"/ui/export?format=csv&"+november, nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "text/csv; charset=utf-8", header.Get("Content-Type"))
	assert.Equal(t, `attachment; filename="expenses_2026-11-01_2026-11-30.csv"`, header.Get("Content-Disposition"))
	lines := strings.Split(strings.TrimSpace(body), "\n")
	require.Len(t, lines, 3, "a header and November's two expenses, oldest first")
	assert.Equal(t, "id,occurred_at,amount,category,merchant,note,created_at,updated_at", lines[0])
	assert.Contains(t, lines[1], `,2026-11-01T11:05:00+01:00,48.73,groceries,Rewe,"Weekly shop, with ""quotes""",`, "quoted per RFC 4180")
	assert.Contains(t, lines[2], `,2026-11-04T12:20:00-05:00,6.75,food,"'=HYPERLINK(""http://evil"")",,`, "formulas are defused; the offset is kept")

	// JSON, with the filters applied.
	status, header, body = send(t, c, http.MethodGet, s.url+"/ui/export?format=json&"+november+"&merchant=rew", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "application/json; charset=utf-8", header.Get("Content-Type"))
	assert.Equal(t, `attachment; filename="expenses_2026-11-01_2026-11-30.json"`, header.Get("Content-Disposition"))
	var exported []map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &exported))
	require.Len(t, exported, 1)
	assert.Equal(t, "48.73", exported[0]["amount"])
	assert.Equal(t, "Rewe", exported[0]["merchant"])
	assert.NotContains(t, exported[0], "currency")

	// All time
	_, header, body = send(t, c, http.MethodGet, s.url+"/ui/export?format=json&from=&to=", nil, nil)
	assert.Equal(t, `attachment; filename="expenses_all.json"`, header.Get("Content-Disposition"))
	require.NoError(t, json.Unmarshal([]byte(body), &exported))
	assert.Len(t, exported, 3)

	status, _, _ = send(t, c, http.MethodGet, s.url+"/ui/export?format=xlsx", nil, nil)
	assert.Equal(t, http.StatusBadRequest, status)
}

func TestUI_RejectsCrossOriginPosts(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	evil := map[string]string{"Origin": "https://evil.example", "HX-Request": "true"}
	status, _, _ := send(t, c, http.MethodPost, s.url+"/ui/expenses", url.Values{"amount": {"1"}}, evil)
	assert.Equal(t, http.StatusForbidden, status)

	status, _, _ = send(t, c, http.MethodPost, s.url+"/login", url.Values{"email": {email}, "password": {password}}, evil)
	assert.Equal(t, http.StatusForbidden, status)
}

func TestUI_AcceptsSameOriginPostsWithNullOrigin(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)

	// What Chrome sends for a form post when the page has Referrer-Policy: no-referrer.
	headers := map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin"}
	status, _, _ := send(t, c, http.MethodPost, s.url+"/login", url.Values{"email": {email}, "password": {password}}, headers)
	assert.Equal(t, http.StatusSeeOther, status)
}

func TestUI_RejectsCrossSiteFetchMetadata(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	headers := map[string]string{"Sec-Fetch-Site": "cross-site", "HX-Request": "true"}
	status, _, _ := send(t, c, http.MethodPost, s.url+"/ui/expenses", url.Values{"amount": {"1"}}, headers)
	assert.Equal(t, http.StatusForbidden, status)
}

func TestUI_Logout(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	status, header, _ := send(t, c, http.MethodPost, s.url+"/logout", url.Values{}, nil)
	require.Equal(t, http.StatusSeeOther, status)
	assert.Equal(t, "/login", header.Get("Location"))

	status, _, _ = send(t, c, http.MethodGet, s.url+"/", nil, nil)
	assert.Equal(t, http.StatusSeeOther, status, "the session no longer works")
}

func TestUI_SessionDoesNotUnlockApi(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	status, _, _ := send(t, c, http.MethodGet, s.url+"/api/v1/expenses", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, status, "the JSON API accepts only bearer tokens")
}

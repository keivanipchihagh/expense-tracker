package tests

import (
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

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
	status, header, _ := send(t, c, http.MethodPost, s.url+"/login", url.Values{"password": {password}}, nil)
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

	status, _, page := send(t, c, http.MethodPost, s.url+"/login", url.Values{"password": {"wrong"}}, nil)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, page, "Wrong password")

	status, header, _ := send(t, c, http.MethodPost, s.url+"/login", url.Values{"password": {password}}, nil)
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
	assert.Contains(t, form, `<option value="EUR" selected>EUR (€)</option>`)
	assert.Contains(t, form, `<option value="USD" >USD ($)</option>`)

	// A rejected submission shows the form again with the problem
	bad := url.Values{
		"from": {"2026-11-01"}, "to": {"2026-11-30"}, "tz_offset": {"60"},
		"amount": {"1.234"}, "currency": {"EUR"}, "occurred_local": {"2026-11-07T13:20"}, "category": {"food"},
	}
	status, _, form = send(t, c, http.MethodPost, s.url+"/ui/expenses", bad, hx)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, form, "must have at most 2 decimal places for EUR")
	assert.Contains(t, form, `value="1.234"`, "what you typed is kept")

	// A valid submission closes the editor and refreshes the results
	good := url.Values{
		"from": {"2026-11-01"}, "to": {"2026-11-30"}, "tz_offset": {"60"},
		"amount": {"12.5"}, "currency": {"EUR"}, "occurred_local": {"2026-11-07T13:20"},
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
		`{"amount":"10","currency":"EUR","occurred_at":"2026-11-02T12:00:00+01:00","category":"food"}`,
		`{"amount":"30","currency":"EUR","occurred_at":"2026-11-03T12:00:00+01:00","category":"groceries"}`,
		`{"amount":"7.25","currency":"USD","occurred_at":"2026-11-04T12:00:00-05:00","category":"transport"}`,
		`{"amount":"99","currency":"EUR","occurred_at":"2026-10-31T12:00:00+01:00","category":"travel"}`,
	} {
		require.Equal(t, http.StatusCreated, s.call(t, http.MethodPost, "/api/v1/expenses", body, nil))
	}

	c := browser(t)
	signIn(t, s, c)
	status, _, page := send(t, c, http.MethodGet, s.url+"/?from=2026-11-01&to=2026-11-30", nil, nil)
	require.Equal(t, http.StatusOK, status)

	assert.Contains(t, page, "€40.00")
	assert.Contains(t, page, "$7.25")
	assert.NotContains(t, page, "99.00", "October is outside the range")
	assert.Contains(t, page, `value="75"`, "groceries is 75% of EUR spending")
}

func TestUI_RejectsCrossOriginPosts(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	evil := map[string]string{"Origin": "https://evil.example", "HX-Request": "true"}
	status, _, _ := send(t, c, http.MethodPost, s.url+"/ui/expenses", url.Values{"amount": {"1"}}, evil)
	assert.Equal(t, http.StatusForbidden, status)

	status, _, _ = send(t, c, http.MethodPost, s.url+"/login", url.Values{"password": {password}}, evil)
	assert.Equal(t, http.StatusForbidden, status)
}

func TestUI_AcceptsSameOriginPostsWithNullOrigin(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)

	// What Chrome sends for a form post when the page has Referrer-Policy: no-referrer.
	headers := map[string]string{"Origin": "null", "Sec-Fetch-Site": "same-origin"}
	status, _, _ := send(t, c, http.MethodPost, s.url+"/login", url.Values{"password": {password}}, headers)
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

package tests

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	bobEmail    = "bob@example.com"
	bobPassword = "bob-has-a-long-password"
)

// sessionCookieCount counts the session cookies a browser holds for the server.
func hasSession(t *testing.T, s *testServer, c *http.Client) bool {
	t.Helper()
	status, _, _ := send(t, c, http.MethodGet, s.url+"/settings", nil, nil)
	return status == http.StatusOK
}

// --- Sign-up -------------------------------------------------------------

func TestSignUp_UI(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)

	_, _, login := send(t, c, http.MethodGet, s.url+"/login", nil, nil)
	assert.Contains(t, login, `<a href="/signup">Create an account</a>`)
	assert.Contains(t, login, `name="email"`)

	status, _, page := send(t, c, http.MethodGet, s.url+"/signup", nil, nil)
	require.Equal(t, http.StatusOK, status)
	for _, field := range []string{"first_name", "last_name", "email", "password"} {
		assert.Contains(t, page, `name="`+field+`"`)
	}
	assert.NotContains(t, page, `name="username"`, "there are no usernames")

	// Problems are shown next to the fields, with what was typed kept (not the password).
	status, _, page = send(t, c, http.MethodPost, s.url+"/signup", url.Values{"email": {"not-an-email"}, "password": {"short"}}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "First name is required", "names are required")
	assert.Contains(t, page, "Last name is required")
	assert.Contains(t, page, "Email must be an email address")
	assert.Contains(t, page, "Password must be at least 10 characters")
	assert.Contains(t, page, `value="not-an-email"`)
	assert.NotContains(t, page, `value="short"`, "passwords are never echoed back")

	// Taken email.
	names := url.Values{"first_name": {"Bob"}, "last_name": {"Builder"}}
	withNames := func(v url.Values) url.Values {
		maps.Copy(v, names)
		return v
	}
	status, _, page = send(t, c, http.MethodPost, s.url+"/signup", withNames(url.Values{"email": {"Alice@Example.com"}, "password": {bobPassword}}), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "Email is taken", "emails are case-insensitive")

	// Success signs in and lands on an empty expense list.
	status, header, _ := send(t, c, http.MethodPost, s.url+"/signup", withNames(url.Values{"email": {" Bob@Example.com "}, "password": {bobPassword}}), nil)
	require.Equal(t, http.StatusSeeOther, status)
	assert.Equal(t, "/", header.Get("Location"))
	assert.Contains(t, header.Get("Set-Cookie"), "et_session=")
	_, _, home := send(t, c, http.MethodGet, s.url+"/", nil, nil)
	assert.Contains(t, home, `aria-label="Account menu" title="Bob Builder">BB</summary>`, "the profile icon shows the initials")
	assert.Contains(t, home, `<span class="menu-sub">bob@example.com</span>`, "the menu names the account by email")
	assert.Equal(t, 1, strings.Count(home, "Sign out"), "sign out appears once, in the menu")
	assert.Equal(t, 1, strings.Count(home, `href="/apps"`), "Apps is linked once, from the menu")
	assert.Contains(t, home, "No expenses in this range.")

	// Signed in, the sign-up page sends you home.
	status, header, _ = send(t, c, http.MethodGet, s.url+"/signup", nil, nil)
	assert.Equal(t, http.StatusSeeOther, status)
	assert.Equal(t, "/", header.Get("Location"))

	// The new account signs in with its normalized email.
	fresh := browser(t)
	status, _, _ = send(t, fresh, http.MethodPost, s.url+"/login", url.Values{"email": {"BOB@example.com"}, "password": {bobPassword}}, nil)
	assert.Equal(t, http.StatusSeeOther, status)
}

func TestSignUp_RejectsCrossSite(t *testing.T) {
	s := newTestServer(t)
	status, _, _ := send(t, browser(t), http.MethodPost, s.url+"/signup",
		url.Values{"email": {bobEmail}, "password": {bobPassword}, "first_name": {"Bob"}, "last_name": {"B"}},
		map[string]string{"Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusForbidden, status, "another site can't sign a visitor up")
}

func TestSignUp_Disabled(t *testing.T) {
	s := newTestServerWith(t, false)
	c := browser(t)

	_, _, login := send(t, c, http.MethodGet, s.url+"/login", nil, nil)
	assert.NotContains(t, login, "/signup", "no sign-up link")
	_, _, page := send(t, c, http.MethodGet, s.url+"/signup", nil, nil)
	assert.Contains(t, page, "Sign-up is turned off on this server.")
	assert.NotContains(t, page, `name="password"`)

	status, _, page := send(t, c, http.MethodPost, s.url+"/signup", url.Values{"email": {bobEmail}, "password": {bobPassword}, "first_name": {"Bob"}, "last_name": {"B"}}, nil)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Contains(t, page, "Sign-up is turned off")

	resp, err := http.Post(s.url+"/api/v1/users", "application/json", strings.NewReader(`{"email":"bob@example.com","password":"bob-has-a-long-password","first_name":"Bob","last_name":"B"}`))
	require.NoError(t, err)
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	decode(t, resp, &body)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "signup_disabled", body.Error.Code)

	// Existing accounts still sign in.
	signIn(t, s, c)
}

func TestSignUp_Api(t *testing.T) {
	s := newTestServer(t)
	post := func(body string) (int, map[string]any) {
		resp, err := http.Post(s.url+"/api/v1/users", "application/json", strings.NewReader(body))
		require.NoError(t, err)
		var out map[string]any
		decode(t, resp, &out)
		return resp.StatusCode, out
	}

	status, body := post(`{"email":"bob@example.com","password":"bob-has-a-long-password"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, map[string]any{"first_name": "is required", "last_name": "is required"}, body["error"].(map[string]any)["fields"], "names are required")

	status, body = post(`{"username":"bob","email":"bob@example.com","password":"bob-has-a-long-password","first_name":"Bob","last_name":"B"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, map[string]any{"username": "unknown field"}, body["error"].(map[string]any)["fields"], "there are no usernames")

	status, user := post(`{"email":"BOB@example.com","password":"bob-has-a-long-password","first_name":"Bob","last_name":"Builder"}`)
	require.Equal(t, http.StatusCreated, status, "sign-up needs no token")
	assert.NotContains(t, user, "username")
	assert.Equal(t, "bob@example.com", user["email"])
	assert.Equal(t, "EUR", user["currency"])
	assert.Equal(t, "Bob", user["first_name"])
	assert.Equal(t, "Builder", user["last_name"])
	assert.NotEmpty(t, user["id"])
	assert.NotContains(t, user, "password")
	assert.NotContains(t, user, "password_hash")

	status, body = post(`{"email":"Bob@Example.com","password":"bob-has-a-long-password","first_name":"B","last_name":"B"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, map[string]any{"email": "is taken"}, body["error"].(map[string]any)["fields"])

	status, body = post(`{"email":"carol@example.com","password":12345678901,"admin":true,"first_name":"C","last_name":"C"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, map[string]any{"password": "must be a string", "admin": "unknown field"}, body["error"].(map[string]any)["fields"])

	status, _ = post(`not json`)
	assert.Equal(t, http.StatusBadRequest, status)

	// The new account signs in through OAuth and sees itself.
	bob := &testServer{url: s.url, db: s.db, h: s.h, email: bobEmail, password: bobPassword}
	bob.login(t)
	var me map[string]any
	require.Equal(t, http.StatusOK, bob.call(t, http.MethodGet, "/api/v1/me", "", &me))
	assert.Equal(t, bobEmail, me["email"])

	// /api/v1/users is only public for sign-up; everything else needs a token.
	resp, err := http.Get(s.url + "/api/v1/me")
	require.NoError(t, err)
	decode(t, resp, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp, err = http.Get(s.url + "/api/v1/users")
	require.NoError(t, err)
	decode(t, resp, nil)
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "there's no way to list users")
}

// --- Account -------------------------------------------------------------

func TestAccount_Api(t *testing.T) {
	s := newTestServer(t)
	s.login(t)
	bob := s.as(t, bobEmail, bobPassword)

	var me map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/me", "", &me))
	assert.Equal(t, email, me["email"], "each token belongs to its own user")
	require.Equal(t, http.StatusOK, bob.call(t, http.MethodGet, "/api/v1/me", "", &me))
	assert.Equal(t, bobEmail, me["email"])

	require.Equal(t, http.StatusOK, s.call(t, http.MethodPatch, "/api/v1/me", `{"first_name":"Alice","last_name":"Liddell","currency":"USD"}`, &me))
	assert.Equal(t, "Alice", me["first_name"])
	assert.Equal(t, "Liddell", me["last_name"])
	assert.Equal(t, "USD", me["currency"])
	assert.Equal(t, email, me["email"], "left out, left alone")

	var fail struct {
		Error struct{ Fields map[string]string } `json:"error"`
	}
	assert.Equal(t, http.StatusUnprocessableEntity, s.call(t, http.MethodPatch, "/api/v1/me", `{"username":"mallory","email":"bob@example.com"}`, &fail))
	assert.Equal(t, map[string]string{"username": "unknown field", "email": "cannot be changed"}, fail.Error.Fields)
	fail.Error.Fields = nil
	assert.Equal(t, http.StatusUnprocessableEntity, s.call(t, http.MethodPatch, "/api/v1/me", `{"first_name":""}`, &fail))
	assert.Equal(t, map[string]string{"first_name": "is required"}, fail.Error.Fields, "names can't be cleared")
	fail.Error.Fields = nil
	assert.Equal(t, http.StatusUnprocessableEntity, s.call(t, http.MethodPatch, "/api/v1/me", `{"id":"x","password_hash":"x"}`, &fail))
	assert.Equal(t, map[string]string{"id": "unknown field", "password_hash": "unknown field"}, fail.Error.Fields)

	// Password: the current one is required.
	fail.Error.Fields = nil
	assert.Equal(t, http.StatusUnprocessableEntity, s.call(t, http.MethodPut, "/api/v1/me/password", `{"current_password":"guess-guess-guess","new_password":"brand-new-password"}`, &fail))
	assert.Equal(t, map[string]string{"current_password": "is wrong"}, fail.Error.Fields)
	assert.Equal(t, http.StatusNoContent, s.call(t, http.MethodPut, "/api/v1/me/password", `{"current_password":"`+password+`","new_password":"brand-new-password"}`, nil))

	old := browser(t)
	status, _, _ := send(t, old, http.MethodPost, s.url+"/login", url.Values{"email": {email}, "password": {password}}, nil)
	assert.Equal(t, http.StatusUnauthorized, status, "the old password stops working")
	status, _, _ = send(t, old, http.MethodPost, s.url+"/login", url.Values{"email": {email}, "password": {"brand-new-password"}}, nil)
	assert.Equal(t, http.StatusSeeOther, status, "the new one works")

	require.Equal(t, http.StatusOK, bob.call(t, http.MethodGet, "/api/v1/me", "", &me))
	assert.Equal(t, bobEmail, me["email"], "bob is unaffected")
}

func TestSettings_UI(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	// The top bar: a profile icon whose menu holds settings, apps and sign out.
	_, _, home := send(t, c, http.MethodGet, s.url+"/", nil, nil)
	assert.Contains(t, home, `aria-label="Account menu" title="alice@example.com">A</summary>`, "no name yet: the email's initial")
	assert.Contains(t, home, `<a href="/settings" class="">Settings</a>`)
	assert.Contains(t, home, `<a href="/apps" class="">Apps</a>`)
	assert.Contains(t, home, `<button type="submit" class="menu-item">Sign out</button>`)
	assert.Equal(t, 1, strings.Count(home, `<span class="menu-sub">alice@example.com</span>`), "the email appears once, in the menu")
	assert.NotContains(t, home, "@alice<", "no @username anywhere")
	assert.Equal(t, 1, strings.Count(home, "Sign out"), "and so does sign out")

	status, header, _ := send(t, c, http.MethodGet, s.url+"/account", nil, nil)
	assert.Equal(t, http.StatusMovedPermanently, status, "the old address still works")
	assert.Equal(t, "/settings", header.Get("Location"))

	status, _, page := send(t, c, http.MethodGet, s.url+"/settings", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `<input type="email" value="alice@example.com" readonly aria-readonly="true" class="readonly">`, "the email is shown, not editable")
	assert.NotContains(t, page, `name="email"`, "it isn't submitted with the form")
	assert.NotContains(t, page, "Username")
	assert.NotContains(t, page, "can't be changed")
	assert.NotContains(t, page, "sign in with it")
	assert.Contains(t, page, `class="avatar-button active"`)
	assert.NotContains(t, page, "$2a$", "the password hash never reaches the page")

	// Names are required.
	status, _, page = send(t, c, http.MethodPost, s.url+"/settings", url.Values{"first_name": {"Alice"}, "last_name": {" "}, "currency": {"EUR"}}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "is required")
	assert.Contains(t, page, `value="Alice"`, "what was typed is kept")

	status, _, page = send(t, c, http.MethodPost, s.url+"/settings", url.Values{"first_name": {"Alice"}, "last_name": {"Liddell"}, "currency": {"GBP"}}, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, "Saved.")
	assert.Contains(t, page, `title="Alice Liddell">AL</summary>`, "the profile icon follows")
	assert.Contains(t, page, `<span class="menu-name">Alice Liddell</span>`)

	// Sending an email anyway changes nothing.
	status, _, _ = send(t, c, http.MethodPost, s.url+"/settings", url.Values{"email": {"mallory@example.com"}, "first_name": {"Alice"}, "last_name": {"Liddell"}}, nil)
	assert.Equal(t, http.StatusOK, status)
	status, _, _ = send(t, browser(t), http.MethodPost, s.url+"/login", url.Values{"email": {email}, "password": {password}}, nil)
	assert.Equal(t, http.StatusSeeOther, status, "still signs in with the original email")
	_, _, page = send(t, c, http.MethodGet, s.url+"/settings", nil, nil)
	assert.Contains(t, page, `value="alice@example.com"`)
	assert.NotContains(t, page, "mallory")
}

func TestAccount_PasswordChangeSignsOutOtherBrowsers(t *testing.T) {
	s := newTestServer(t)
	laptop, phone := browser(t), browser(t)
	signIn(t, s, laptop)
	signIn(t, s, phone)
	bob := browser(t)
	s.addUser(t, bobEmail, bobPassword)
	status, _, _ := send(t, bob, http.MethodPost, s.url+"/login", url.Values{"email": {bobEmail}, "password": {bobPassword}}, nil)
	require.Equal(t, http.StatusSeeOther, status)

	status, _, page := send(t, laptop, http.MethodPost, s.url+"/settings/password", url.Values{"current_password": {"wrong-current-pass"}, "new_password": {"x"}}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "is wrong")
	assert.Contains(t, page, "must be at least 10 characters")
	assert.True(t, hasSession(t, s, phone), "a failed change signs nobody out")

	status, _, page = send(t, laptop, http.MethodPost, s.url+"/settings/password", url.Values{"current_password": {password}, "new_password": {"a-better-password"}}, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, "Password changed. Other browsers were signed out.")

	assert.True(t, hasSession(t, s, laptop), "this browser stays signed in")
	assert.False(t, hasSession(t, s, phone), "the other browser is signed out")
	assert.True(t, hasSession(t, s, bob), "other users are untouched")
}

// --- Isolation -----------------------------------------------------------

// twoUsers returns alice and bob signed in through OAuth, with one expense each.
func twoUsers(t *testing.T) (alice, bob *testServer, aliceExpense, bobExpense string) {
	t.Helper()
	alice = newTestServer(t)
	alice.login(t)
	bob = alice.as(t, bobEmail, bobPassword)

	var created map[string]any
	require.Equal(t, http.StatusCreated, alice.call(t, http.MethodPost, "/api/v1/expenses",
		`{"amount":"10","occurred_at":"2026-11-02T12:00:00+01:00","category":"food","merchant":"Alice Cafe","note":"alice only"}`, &created))
	aliceExpense = created["id"].(string)
	require.Equal(t, http.StatusCreated, bob.call(t, http.MethodPost, "/api/v1/expenses",
		`{"amount":"99","occurred_at":"2026-11-03T12:00:00+01:00","category":"travel","merchant":"Bob Air"}`, &created))
	bobExpense = created["id"].(string)
	return
}

func TestIsolation_Api(t *testing.T) {
	alice, bob, aliceExpense, bobExpense := twoUsers(t)

	var listed []map[string]any
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/expenses", "", &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, aliceExpense, listed[0]["id"])
	assert.NotContains(t, listed[0], "user_id", "owners aren't exposed")

	require.Equal(t, http.StatusOK, bob.call(t, http.MethodGet, "/api/v1/expenses?merchant=alice", "", &listed))
	assert.Empty(t, listed, "filters don't reach across users")

	// Bob can't read, change or delete alice's expense, and learns nothing:
	// it looks exactly like an id that doesn't exist.
	assert.Equal(t, http.StatusNotFound, bob.call(t, http.MethodGet, "/api/v1/expenses/"+aliceExpense, "", nil))
	assert.Equal(t, http.StatusNotFound, bob.call(t, http.MethodPatch, "/api/v1/expenses/"+aliceExpense, `{"amount":"0.01"}`, nil))
	assert.Equal(t, http.StatusNotFound, bob.call(t, http.MethodDelete, "/api/v1/expenses/"+aliceExpense, "", nil))
	assert.Equal(t, http.StatusNotFound, bob.call(t, http.MethodGet, "/api/v1/expenses/does-not-exist", "", nil))

	var got map[string]any
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/expenses/"+aliceExpense, "", &got))
	assert.Equal(t, "10.00", got["amount"], "alice's expense is untouched")

	// And the other way round.
	assert.Equal(t, http.StatusNotFound, alice.call(t, http.MethodDelete, "/api/v1/expenses/"+bobExpense, "", nil))

	// The owner can't be set or changed through the API.
	var fail struct {
		Error struct{ Fields map[string]string } `json:"error"`
	}
	assert.Equal(t, http.StatusUnprocessableEntity, bob.call(t, http.MethodPost, "/api/v1/expenses",
		`{"amount":"1","occurred_at":"2026-11-03T12:00:00+01:00","category":"other","user_id":"`+aliceExpense+`"}`, &fail))
	assert.Equal(t, "unknown field", fail.Error.Fields["user_id"])
	assert.Equal(t, http.StatusUnprocessableEntity, bob.call(t, http.MethodPatch, "/api/v1/expenses/"+bobExpense, `{"user_id":"x"}`, &fail))
}

func TestIsolation_Mcp(t *testing.T) {
	alice, bob, aliceExpense, _ := twoUsers(t)
	session := connectMcp(t, bob)

	text, isErr := callTool(t, session, "list_expenses", map[string]any{})
	require.False(t, isErr, text)
	var listed struct {
		Expenses []map[string]any `json:"expenses"`
		Count    int              `json:"count"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &listed))
	require.Equal(t, 1, listed.Count, "bob's agent sees bob's expenses only")
	assert.Equal(t, "Bob Air", listed.Expenses[0]["merchant"])
	assert.NotContains(t, text, "Alice Cafe")

	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"get_expense", map[string]any{"id": aliceExpense}},
		{"update_expense", map[string]any{"id": aliceExpense, "amount": "0.01"}},
		{"delete_expense", map[string]any{"id": aliceExpense}},
	} {
		text, isErr = callTool(t, session, call.tool, call.args)
		assert.True(t, isErr, call.tool)
		assert.Equal(t, "expense not found", text, call.tool)
	}

	// Created through MCP, owned by the agent's user.
	text, isErr = callTool(t, session, "create_expense", map[string]any{"amount": "5", "occurred_at": "2026-11-04T09:00:00+01:00", "category": "food"})
	require.False(t, isErr, text)
	var aliceList []map[string]any
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/expenses", "", &aliceList))
	assert.Len(t, aliceList, 1, "alice still has only her own")
}

func TestIsolation_UI(t *testing.T) {
	alice, bob, aliceExpense, _ := twoUsers(t)
	c := browser(t)
	signIn(t, bob, c)
	hx := map[string]string{"HX-Request": "true"}
	november := "from=2026-11-01&to=2026-11-30"

	_, _, page := send(t, c, http.MethodGet, alice.url+"/?"+november, nil, nil)
	assert.Contains(t, page, "Bob Air")
	assert.NotContains(t, page, "Alice Cafe")
	assert.Contains(t, page, "€99.00")
	assert.NotContains(t, page, "€109.00", "totals are bob's alone")

	status, _, _ := send(t, c, http.MethodGet, alice.url+"/ui/expenses/"+aliceExpense+"/edit", nil, hx)
	assert.Equal(t, http.StatusNotFound, status, "bob can't open alice's expense")
	status, _, _ = send(t, c, http.MethodPost, alice.url+"/ui/expenses/"+aliceExpense, url.Values{
		"tz_offset": {"60"}, "amount": {"0.01"}, "occurred_local": {"2026-11-02T12:00"}, "category": {"food"},
	}, hx)
	assert.Equal(t, http.StatusNotFound, status, "or save over it")
	send(t, c, http.MethodPost, alice.url+"/ui/expenses/"+aliceExpense+"/delete", url.Values{"from": {"2026-11-01"}, "to": {"2026-11-30"}}, hx)

	var got map[string]any
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/expenses/"+aliceExpense, "", &got), "alice's expense survives the delete attempt")
	assert.Equal(t, "10.00", got["amount"])

	_, _, csv := send(t, c, http.MethodGet, alice.url+"/ui/export?format=csv&from=&to=", nil, nil)
	assert.Contains(t, csv, "Bob Air")
	assert.NotContains(t, csv, "Alice Cafe", "exports are bob's alone")

	_, _, analysis := send(t, c, http.MethodGet, alice.url+"/analysis?period=all", nil, nil)
	assert.Contains(t, analysis, `<p class="kpi-value">€99.00</p>`)
	assert.NotContains(t, analysis, "Alice Cafe")
}

func TestIsolation_ConnectedApps(t *testing.T) {
	alice, bob, _, _ := twoUsers(t)
	aliceBrowser, bobBrowser := browser(t), browser(t)
	signIn(t, alice, aliceBrowser)
	signIn(t, bob, bobBrowser)

	_, _, apps := send(t, aliceBrowser, http.MethodGet, alice.url+"/apps", nil, nil)
	assert.Equal(t, 1, strings.Count(apps, `class="row" id="app-`), "alice sees her own app only")
	appId := apps[strings.Index(apps, `id="app-`)+len(`id="app-`):]
	appId = appId[:strings.Index(appId, `"`)]

	// Bob can't revoke alice's app.
	status, _, _ := send(t, bobBrowser, http.MethodPost, alice.url+"/ui/apps/"+appId+"/revoke", nil, nil)
	assert.Equal(t, http.StatusOK, status, "it's a no-op that reveals nothing")
	assert.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/expenses", "", nil), "alice's token still works")

	// Alice revokes it: her token dies, bob's keeps working.
	status, _, _ = send(t, aliceBrowser, http.MethodPost, alice.url+"/ui/apps/"+appId+"/revoke", nil, nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, http.StatusUnauthorized, alice.call(t, http.MethodGet, "/api/v1/expenses", "", nil))
	assert.Equal(t, http.StatusOK, bob.call(t, http.MethodGet, "/api/v1/expenses", "", nil))
}

// --- Sign-in -------------------------------------------------------------

func TestSignIn_LockoutIsPerEmail(t *testing.T) {
	s := newTestServer(t)
	s.addUser(t, bobEmail, bobPassword)

	for range 10 {
		status, _, _ := send(t, browser(t), http.MethodPost, s.url+"/login", url.Values{"email": {bobEmail}, "password": {"guessing-guessing"}}, nil)
		require.Equal(t, http.StatusUnauthorized, status)
	}
	status, _, page := send(t, browser(t), http.MethodPost, s.url+"/login", url.Values{"email": {bobEmail}, "password": {bobPassword}}, nil)
	assert.Equal(t, http.StatusTooManyRequests, status, "bob's email is locked, even with the right password")
	assert.Contains(t, page, "Too many failed sign-ins for this email")

	c := browser(t)
	signIn(t, s, c)
	assert.True(t, hasSession(t, s, c), "alice can still sign in")
}

func TestSignIn_SameAnswerForUnknownEmail(t *testing.T) {
	s := newTestServer(t)
	wrongPassword := url.Values{"email": {email}, "password": {"not-the-password"}}
	unknownEmail := url.Values{"email": {"nobody@example.com"}, "password": {"not-the-password"}}

	s1, _, p1 := send(t, browser(t), http.MethodPost, s.url+"/login", wrongPassword, nil)
	s2, _, p2 := send(t, browser(t), http.MethodPost, s.url+"/login", unknownEmail, nil)
	assert.Equal(t, s1, s2)
	assert.Contains(t, p1, "Wrong email or password.")
	assert.Contains(t, p2, "Wrong email or password.", "nothing tells whether the account exists")
}

func TestOAuth_SignInPicksTheUser(t *testing.T) {
	s := newTestServer(t)
	s.addUser(t, bobEmail, bobPassword)
	clientId := s.register(t)

	// Bob signs in on the OAuth page; the token is his, not alice's.
	bob := &testServer{url: s.url, db: s.db, h: s.h, email: bobEmail, password: bobPassword}
	status, tok, _ := bob.tokenRequest(t, codeGrant(clientId, bob.authorize(t, clientId)))
	require.Equal(t, http.StatusOK, status)
	bob.token = tok.AccessToken
	var me map[string]any
	require.Equal(t, http.StatusOK, bob.call(t, http.MethodGet, "/api/v1/me", "", &me))
	assert.Equal(t, bobEmail, me["email"])

	// Refreshing keeps it bob's.
	status, refreshed, _ := bob.tokenRequest(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientId}, "refresh_token": {tok.RefreshToken}})
	require.Equal(t, http.StatusOK, status)
	bob.token = refreshed.AccessToken
	require.Equal(t, http.StatusOK, bob.call(t, http.MethodGet, "/api/v1/me", "", &me))
	assert.Equal(t, bobEmail, me["email"])

	// A wrong email/password combination gets no code.
	form := authorizeParams(clientId)
	form.Set("email", bobEmail)
	form.Set("password", password) // alice's password
	resp, err := noRedirect.PostForm(s.url+"/oauth/authorize", form)
	require.NoError(t, err)
	decode(t, resp, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- Regression: the expense form as a browser sends it ----------------------

func TestUI_ExpenseFormAsBrowserSendsIt(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)
	hx := map[string]string{"HX-Request": "true"}

	// The form carries the list's filters next to the expense's own
	// category and merchant; neither may override the other.
	for _, filter := range []struct{ category, merchant string }{{"", ""}, {"groceries", "rew"}} {
		form := url.Values{
			"from": {"2026-11-01"}, "to": {"2026-11-30"},
			"filter_category": {filter.category}, "filter_merchant": {filter.merchant},
			"tz_offset": {"60"}, "amount": {"7.5"}, "occurred_local": {"2026-11-05T09:00"},
			"category": {"food"}, "merchant": {"Bakery"}, "note": {""},
		}
		status, _, saved := send(t, c, http.MethodPost, s.url+"/ui/expenses", form, hx)
		require.Equal(t, http.StatusOK, status)
		assert.Contains(t, saved, `<div id="editor" hx-swap-oob="true"></div>`, "saved, not sent back with errors")
		assert.Contains(t, saved, `name="category" value="`+filter.category+`"`, "the list keeps its filter")
	}

	s.login(t)
	var listed []map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses", "", &listed))
	require.Len(t, listed, 2)
	for _, e := range listed {
		assert.Equal(t, "food", e["category"], "the expense's own category, not the filter's")
		assert.Equal(t, "Bakery", e["merchant"])
	}
}

// --- Export and delete ---------------------------------------------------

// count returns the number of rows in table matching where.
func count(t *testing.T, s *testServer, table, where string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+where, args...).Scan(&n))
	return n
}

func TestExportData_Api(t *testing.T) {
	alice, bob, aliceExpense, bobExpense := twoUsers(t)
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodPatch, "/api/v1/expenses/"+aliceExpense, `{"note":"edited"}`, nil))

	var data struct {
		ExportedAt string           `json:"exported_at"`
		Account    map[string]any   `json:"account"`
		Expenses   []map[string]any `json:"expenses"`
		History    []map[string]any `json:"history"`
		Apps       []map[string]any `json:"connected_apps"`
	}
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/me/export", "", &data))
	assert.NotEmpty(t, data.ExportedAt)
	assert.Equal(t, email, data.Account["email"])
	assert.NotContains(t, data.Account, "password_hash")
	require.Len(t, data.Expenses, 1, "only alice's expenses")
	assert.Equal(t, aliceExpense, data.Expenses[0]["id"])
	require.Len(t, data.History, 2)
	assert.Equal(t, "create", data.History[0]["action"])
	assert.Equal(t, "update", data.History[1]["action"])
	assert.Nil(t, data.History[0]["before"])
	require.Len(t, data.Apps, 1)
	assert.Equal(t, "Test Client", data.Apps[0]["name"])

	raw := map[string]any{}
	require.Equal(t, http.StatusOK, bob.call(t, http.MethodGet, "/api/v1/me/export", "", &raw))
	body, _ := json.Marshal(raw)
	assert.Contains(t, string(body), bobExpense)
	assert.NotContains(t, string(body), aliceExpense, "bob's export has nothing of alice's")
	assert.NotContains(t, string(body), "$2a$", "no password hash")
	assert.NotContains(t, string(body), alice.token)
}

func TestDeleteAccount_Api(t *testing.T) {
	alice, bob, aliceExpense, bobExpense := twoUsers(t)
	var bobId string
	require.NoError(t, alice.db.QueryRow("SELECT id FROM users WHERE email = ?", bobEmail).Scan(&bobId))

	var fail struct {
		Error struct{ Fields map[string]string } `json:"error"`
	}
	assert.Equal(t, http.StatusUnprocessableEntity, bob.call(t, http.MethodDelete, "/api/v1/me", `{"password":"not-bobs-password"}`, &fail))
	assert.Equal(t, map[string]string{"password": "is wrong"}, fail.Error.Fields)
	fail.Error.Fields = nil
	assert.Equal(t, http.StatusUnprocessableEntity, bob.call(t, http.MethodDelete, "/api/v1/me", `{}`, &fail))
	assert.Equal(t, map[string]string{"password": "is required"}, fail.Error.Fields)
	assert.Equal(t, 1, count(t, alice, "users", "id = ?", bobId), "nothing deleted yet")

	assert.Equal(t, http.StatusNoContent, bob.call(t, http.MethodDelete, "/api/v1/me", `{"password":"`+bobPassword+`"}`, nil))

	for _, table := range []string{"users"} {
		assert.Zero(t, count(t, alice, table, "id = ?", bobId))
	}
	for _, table := range []string{"expenses", "logs", "sessions", "oauth_codes", "oauth_tokens"} {
		assert.Zero(t, count(t, alice, table, "user_id = ?", bobId), "bob's %s are gone", table)
	}
	assert.Equal(t, http.StatusUnauthorized, bob.call(t, http.MethodGet, "/api/v1/me", "", nil), "bob's token stops working")
	assert.Equal(t, http.StatusUnauthorized, bob.call(t, http.MethodGet, "/api/v1/expenses/"+bobExpense, "", nil))

	// Alice is untouched, and bob's email is free again.
	var got map[string]any
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/expenses/"+aliceExpense, "", &got))
	assert.Equal(t, 1, count(t, alice, "logs", "user_id IS NOT NULL"))
	resp, err := http.Post(alice.url+"/api/v1/users", "application/json", strings.NewReader(`{"email":"bob@example.com","password":"bob-has-a-long-password","first_name":"Bob","last_name":"B"}`))
	require.NoError(t, err)
	decode(t, resp, nil)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestDeleteAccount_KeepsSharedApps(t *testing.T) {
	s := newTestServer(t)
	clientId := s.register(t)
	status, tok, _ := s.tokenRequest(t, codeGrant(clientId, s.authorize(t, clientId)))
	require.Equal(t, http.StatusOK, status)
	s.token = tok.AccessToken

	// Bob signs in to the same client, and to one only he uses.
	s.addUser(t, bobEmail, bobPassword)
	bob := &testServer{url: s.url, db: s.db, h: s.h, email: bobEmail, password: bobPassword}
	status, tok, _ = bob.tokenRequest(t, codeGrant(clientId, bob.authorize(t, clientId)))
	require.Equal(t, http.StatusOK, status)
	bob.token = tok.AccessToken
	bobsOwn := bob.register(t)
	bob.authorize(t, bobsOwn) // leaves an unused code

	require.Equal(t, http.StatusNoContent, bob.call(t, http.MethodDelete, "/api/v1/me", `{"password":"`+bobPassword+`"}`, nil))
	assert.Equal(t, 1, count(t, s, "oauth_clients", "id = ?", clientId), "alice still uses the shared client")
	assert.Zero(t, count(t, s, "oauth_clients", "id = ?", bobsOwn), "the client only bob used is gone")
	assert.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/me", "", nil), "alice's token still works")
}

func TestDataSettings_UI(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)
	_, _, _ = send(t, c, http.MethodPost, s.url+"/ui/expenses", url.Values{
		"amount": {"12.50"}, "category": {"food"}, "merchant": {"Rewe"}, "occurred_local": {"2026-11-07T13:20"}, "tz_offset": {"60"},
	}, map[string]string{"HX-Request": "true"})

	_, _, page := send(t, c, http.MethodGet, s.url+"/settings", nil, nil)
	assert.Contains(t, page, `<a href="/settings/export" class="button outline" download>Export my data</a>`)
	assert.Contains(t, page, `data-dialog-open="delete-account">Delete account</button>`)
	assert.NotContains(t, page, "data-show-on-load", "the dialog starts closed")

	// Export downloads everything as JSON.
	req, err := http.NewRequest(http.MethodGet, s.url+"/settings/export", nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	var data map[string]any
	decode(t, resp, &data)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Regexp(t, `^attachment; filename="expense-tracker-data-\d{4}-\d{2}-\d{2}\.json"$`, resp.Header.Get("Content-Disposition"))
	assert.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	require.Len(t, data["expenses"], 1)
	assert.Equal(t, "Rewe", data["expenses"].([]any)[0].(map[string]any)["merchant"])

	// Signed out, neither works.
	status, _, _ := send(t, browser(t), http.MethodGet, s.url+"/settings/export", nil, nil)
	assert.Equal(t, http.StatusSeeOther, status)
	status, _, _ = send(t, browser(t), http.MethodPost, s.url+"/settings/delete", url.Values{"password": {password}}, nil)
	assert.Equal(t, http.StatusSeeOther, status)

	// Another site can't delete the account.
	status, _, _ = send(t, c, http.MethodPost, s.url+"/settings/delete", url.Values{"password": {password}}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusForbidden, status)

	// A wrong password reopens the dialog with the error.
	status, _, page = send(t, c, http.MethodPost, s.url+"/settings/delete", url.Values{"password": {"not-my-password"}}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "data-show-on-load")
	assert.Contains(t, page, "Password is wrong")
	assert.True(t, hasSession(t, s, c), "still signed in")

	other := browser(t)
	signIn(t, s, other)
	status, header, _ := send(t, c, http.MethodPost, s.url+"/settings/delete", url.Values{"password": {password}}, nil)
	require.Equal(t, http.StatusSeeOther, status)
	assert.Equal(t, "/login?deleted=1", header.Get("Location"))
	assert.Contains(t, header.Get("Set-Cookie"), "Max-Age=0")
	assert.False(t, hasSession(t, s, c))
	assert.False(t, hasSession(t, s, other), "every browser is signed out")
	_, _, login := send(t, c, http.MethodGet, s.url+"/login?deleted=1", nil, nil)
	assert.Contains(t, login, "Your account and all its data were deleted.")
	assert.Zero(t, count(t, s, "users", "1 = 1"))
	assert.Zero(t, count(t, s, "expenses", "1 = 1"))
	status, _, _ = send(t, browser(t), http.MethodPost, s.url+"/login", url.Values{"email": {email}, "password": {password}}, nil)
	assert.Equal(t, http.StatusUnauthorized, status, "the account can't sign in any more")
}

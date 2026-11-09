package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCategories_Settings(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	_, _, page := send(t, c, http.MethodGet, s.url+"/settings", nil, nil)
	assert.Contains(t, page, `<section class="card" id="categories">`)
	assert.Contains(t, page, `<span class="chip-name">Groceries</span>`, "every account starts with the defaults")

	// Add: normalized, shown, and the input gets the focus.
	status, _, page := send(t, c, http.MethodPost, s.url+"/settings/categories", url.Values{"name": {"  Kids'  Stuff "}}, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, "Added Kids&#39; stuff.")
	assert.Contains(t, page, `<span class="chip-name">Kids&#39; stuff</span>`)
	assert.Contains(t, page, `autofocus`)

	status, _, page = send(t, c, http.MethodPost, s.url+"/settings/categories", url.Values{"name": {"GROCERIES"}}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "Category already exists.")
	status, _, page = send(t, c, http.MethodPost, s.url+"/settings/categories", url.Values{"name": {"<script>"}}, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "Category may only contain")
	assert.NotContains(t, page, "<script>", "what was typed is escaped")

	// The new category works in the expense form; then it's in use.
	_, _, form := send(t, c, http.MethodGet, s.url+"/ui/expenses/new", nil, nil)
	assert.Contains(t, form, `<option value="kids&#39; stuff" >Kids&#39; stuff</option>`)
	status, _, _ = send(t, c, http.MethodPost, s.url+"/ui/expenses", url.Values{
		"amount": {"20"}, "category": {"kids' stuff"}, "occurred_local": {"2026-11-07T10:00"}, "tz_offset": {"60"}, "from": {"2026-11-01"}, "to": {"2026-11-30"},
	}, map[string]string{"HX-Request": "true"})
	require.Equal(t, http.StatusOK, status)

	_, _, page = send(t, c, http.MethodGet, s.url+"/settings", nil, nil)
	assert.Contains(t, page, `title="Used by 1 expense, so it can't be removed" disabled`, "the remove button is disabled while it's in use")
	status, _, page = send(t, c, http.MethodPost, s.url+"/settings/categories/delete", url.Values{"name": {"kids' stuff"}}, nil)
	assert.Equal(t, http.StatusConflict, status, "and the server refuses it anyway")
	assert.Contains(t, page, "can&#39;t be removed: 1 expense uses it")

	// An unused one goes.
	status, _, page = send(t, c, http.MethodPost, s.url+"/settings/categories/delete", url.Values{"name": {"education"}}, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, "Removed Education.")
	assert.NotContains(t, page, `<span class="chip-name">Education</span>`)
	_, _, form = send(t, c, http.MethodGet, s.url+"/ui/expenses/new", nil, nil)
	assert.NotContains(t, form, `value="education"`, "nor is it offered for new expenses")

	// Another site can't change them.
	status, _, _ = send(t, c, http.MethodPost, s.url+"/settings/categories", url.Values{"name": {"spam"}}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusForbidden, status)
}

func TestCategories_ApiAndIsolation(t *testing.T) {
	alice, bob, _, _ := twoUsers(t)

	var created map[string]any
	require.Equal(t, http.StatusCreated, alice.call(t, http.MethodPost, "/api/v1/categories", `{"name":"Pets"}`, &created))
	assert.Equal(t, "pets", created["name"])

	var list []map[string]any
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/categories", "", &list))
	assert.Equal(t, "pets", list[len(list)-1]["name"], "new ones go last")
	assert.EqualValues(t, 1, list[0]["expenses"], "alice's food expense counts")

	// Alice can use it; bob can't, and keeps his own list.
	assert.Equal(t, http.StatusCreated, alice.call(t, http.MethodPost, "/api/v1/expenses", `{"amount":"30","occurred_at":"2026-11-05T09:00:00+01:00","category":"Pets"}`, nil))
	var fail struct {
		Error struct{ Fields map[string]string } `json:"error"`
	}
	assert.Equal(t, http.StatusUnprocessableEntity, bob.call(t, http.MethodPost, "/api/v1/expenses", `{"amount":"30","occurred_at":"2026-11-05T09:00:00+01:00","category":"pets"}`, &fail))
	assert.Contains(t, fail.Error.Fields["category"], "must be one of your categories: food,")
	assert.Equal(t, http.StatusNotFound, bob.call(t, http.MethodDelete, "/api/v1/categories/pets", "", nil))

	// Filtering by it works; removing it while used doesn't.
	var filtered []map[string]any
	require.Equal(t, http.StatusOK, alice.call(t, http.MethodGet, "/api/v1/expenses?category=pets", "", &filtered))
	assert.Len(t, filtered, 1)
	var conflict struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	assert.Equal(t, http.StatusConflict, alice.call(t, http.MethodDelete, "/api/v1/categories/pets", "", &conflict))
	assert.Equal(t, "category_in_use", conflict.Error.Code)

	assert.Equal(t, http.StatusUnprocessableEntity, alice.call(t, http.MethodPost, "/api/v1/categories", `{"name":"x","color":"red"}`, nil))
	assert.Equal(t, http.StatusNoContent, alice.call(t, http.MethodDelete, "/api/v1/categories/gifts", "", nil))
	assert.Equal(t, http.StatusNotFound, alice.call(t, http.MethodDelete, "/api/v1/categories/gifts", "", nil))
}

func TestCategories_McpListsTheUsersOwn(t *testing.T) {
	s := newTestServer(t)
	s.login(t)
	require.Equal(t, http.StatusCreated, s.call(t, http.MethodPost, "/api/v1/categories", `{"name":"pets"}`, nil))
	require.Equal(t, http.StatusNoContent, s.call(t, http.MethodDelete, "/api/v1/categories/travel", "", nil))

	session := connectMcp(t, s)
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	for _, tool := range tools.Tools {
		if tool.Name == "create_expense" || tool.Name == "list_expenses" {
			schema, _ := json.Marshal(tool.InputSchema)
			assert.Contains(t, string(schema), `"pets"`, tool.Name)
			assert.NotContains(t, string(schema), `"travel"`, tool.Name)
		}
	}
	text, isErr := callTool(t, session, "create_expense", map[string]any{"amount": "9.99", "occurred_at": "2026-11-07T10:00:00+01:00", "category": "pets"})
	assert.False(t, isErr, text)
	assert.True(t, strings.Contains(text, `"category":"pets"`))
}

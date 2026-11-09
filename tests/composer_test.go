package tests

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sendText(t *testing.T, s *testServer, c *http.Client, text string) (int, string) {
	t.Helper()
	status, _, page := send(t, c, http.MethodPost, s.url+"/ui/expenses/text", url.Values{
		"text": {text}, "tz_offset": {"60"}, "from": {"2026-11-01"}, "to": {"2026-11-30"}, "filter_category": {""}, "filter_merchant": {""},
	}, map[string]string{"HX-Request": "true"})
	return status, page
}

func TestComposer_ShownOnlyWithAModel(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)
	_, _, page := send(t, c, http.MethodGet, s.url+"/", nil, nil)
	assert.NotContains(t, page, "data-composer", "no OpenRouter key, no composer")
	status, _ := sendText(t, s, c, "5 coffee")
	assert.Equal(t, http.StatusNotFound, status)

	withFakeOpenRouter(t, s, http.StatusOK, `{"is_expense":true}`)
	_, _, page = send(t, c, http.MethodGet, s.url+"/", nil, nil)
	assert.Contains(t, page, `<form class="composer" data-composer`)
	assert.Contains(t, page, `data-composer-camera`)
	assert.Contains(t, page, `maxlength="500"`)
	_, _, page = send(t, c, http.MethodGet, s.url+"/analysis", nil, nil)
	assert.NotContains(t, page, "data-composer", "only on the expense list")
}

func TestComposer_FillsOnlyWhatWasSaid(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	// Amount and merchant, no day, no category.
	withFakeOpenRouter(t, s, http.StatusOK, `{"is_expense":true,"amount":"12.5","currency":null,"date":null,"time":null,"merchant":"Rewe","category":null,"note":null}`)
	status, page := sendText(t, s, c, "12.50 at Rewe")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `<p class="from-text">`)
	assert.Contains(t, page, "“12.50 at Rewe”")
	assert.Contains(t, page, `name="amount" inputmode="decimal" value="12.50"`)
	assert.Contains(t, page, `name="merchant" value="Rewe"`)
	assert.Contains(t, page, `<option value="" selected disabled>Choose a category</option>`, "the category is left to the user")
	assert.NotContains(t, page, `value="food" selected`)
	assert.Contains(t, page, `name="occurred_local" step="1" value=""`, "no day said: the form fills in now")
	assert.Contains(t, page, `name="tz_offset" value="60"`)
	assert.NotContains(t, page, "scan-warnings", "nothing to warn about")

	// A day without a time keeps the current time of day.
	withFakeOpenRouter(t, s, http.StatusOK, `{"is_expense":true,"amount":"30","date":"2026-11-08","time":null,"merchant":null,"category":"transport","note":"to the airport"}`)
	_, page = sendText(t, s, c, "30 taxi to the airport yesterday")
	assert.Contains(t, page, `value="2026-11-08T`+time.Now().In(time.FixedZone("", 3600)).Format("15:")) // minutes may tick
	assert.Contains(t, page, `<option value="transport" selected>`)
	assert.Contains(t, page, "to the airport</textarea>")

	// Nothing saved until the user adds it.
	s.login(t)
	var listed []map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses", "", &listed))
	assert.Empty(t, listed)
}

func TestComposer_Problems(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)
	calls := withFakeOpenRouter(t, s, http.StatusOK, `{"is_expense":false}`)

	status, page := sendText(t, s, c, "hello there")
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "Couldn&#39;t find an expense in that.")
	assert.Contains(t, page, "“hello there”", "what was said is shown")

	status, page = sendText(t, s, c, "   ")
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "The message is required.")
	status, _ = sendText(t, s, c, strings.Repeat("a", 501))
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, int32(1), calls.Load(), "bad input never reaches the model")

	// Signed out, or from another site.
	status, _, _ = send(t, browser(t), http.MethodPost, s.url+"/ui/expenses/text", url.Values{"text": {"5 coffee"}}, nil)
	assert.Equal(t, http.StatusSeeOther, status)
	status, _, _ = send(t, c, http.MethodPost, s.url+"/ui/expenses/text", url.Values{"text": {"5 coffee"}}, map[string]string{"Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, int32(1), calls.Load())
}

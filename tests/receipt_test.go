package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keivanipchihagh/expense-tracker/internal/receipt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// receiptPng is enough of a PNG for content sniffing.
var receiptPng = append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), make([]byte, 64)...)

// withFakeOpenRouter turns on scanning against a fake OpenRouter that
// answers every request with content as the model's message. It returns
// how many requests it got.
func withFakeOpenRouter(t *testing.T, s *testServer, status int, content string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = io.WriteString(w, content)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	t.Cleanup(fake.Close)
	s.h.SetReceiptScanner(receipt.NewOpenRouter("test-key", "").WithBaseUrl(fake.URL))
	return &calls
}

// scan posts a receipt image the way the expense form's script does.
func scan(t *testing.T, s *testServer, c *http.Client, image []byte, headers map[string]string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range map[string]string{"from": "2026-11-01", "to": "2026-11-30", "filter_category": "groceries", "filter_merchant": "", "tz_offset": "60"} {
		require.NoError(t, mw.WriteField(k, v))
	}
	if image != nil {
		fw, err := mw.CreateFormFile("receipt", "receipt.jpg")
		require.NoError(t, err)
		_, err = fw.Write(image)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	req, err := http.NewRequest(http.MethodPost, s.url+"/ui/expenses/scan", &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(raw)
}

func TestScan_FillsTheFormWithoutSaving(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)
	calls := withFakeOpenRouter(t, s, http.StatusOK,
		`{"is_receipt":true,"amount":"10,91","currency":"USD","date":"2026-11-07","time":"18:42","merchant":"Rewe","category":"groceries","note":"Oat milk, bananas"}`)

	_, _, form := send(t, c, http.MethodGet, s.url+"/ui/expenses/new", nil, nil)
	assert.Contains(t, form, `data-scan-input`, "the new expense form offers scanning")
	assert.Contains(t, form, "Scan a receipt")

	status, page := scan(t, s, c, receiptPng, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, int32(1), calls.Load())
	assert.Contains(t, page, "Read from your receipt")
	assert.Contains(t, page, `name="amount" inputmode="decimal" value="10.91"`)
	assert.Contains(t, page, `value="2026-11-07T18:42:00"`)
	assert.Contains(t, page, `<option value="groceries" selected>`)
	assert.Contains(t, page, `name="merchant" value="Rewe"`)
	assert.Contains(t, page, "Oat milk, bananas</textarea>")
	assert.Contains(t, page, `name="tz_offset" value="60"`, "the browser's offset is kept")
	assert.Contains(t, page, `name="filter_category" value="groceries"`, "so is the list's filter")
	assert.Contains(t, page, "The receipt is in USD, but amounts are shown in EUR")
	assert.Contains(t, page, "Add expense", "the user still has to add it")

	var listed []map[string]any
	s.login(t)
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses", "", &listed))
	assert.Empty(t, listed, "nothing is saved until the user adds it")
}

func TestScan_Problems(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)

	// Off without a key: no scanner in the form, and the route says so.
	_, _, form := send(t, c, http.MethodGet, s.url+"/ui/expenses/new", nil, nil)
	assert.NotContains(t, form, "data-scan")
	status, _ := scan(t, s, c, receiptPng, nil)
	assert.Equal(t, http.StatusNotFound, status)

	calls := withFakeOpenRouter(t, s, http.StatusOK, `{"is_receipt":false,"category":"other"}`)
	status, page := scan(t, s, c, receiptPng, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "doesn&#39;t look like a receipt")
	assert.Contains(t, page, "Scan a receipt", "the form is shown again to retry")

	status, page = scan(t, s, c, []byte("<svg onload=alert(1)>"), nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "The photo must be a JPEG, PNG or WebP image.")
	status, page = scan(t, s, c, nil, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, page, "The photo is required.")
	assert.Equal(t, int32(1), calls.Load(), "bad uploads never reach the model")

	// Bigger than the route allows.
	status, page = scan(t, s, c, append(append([]byte{}, receiptPng...), make([]byte, 5<<20)...), nil)
	assert.Equal(t, http.StatusRequestEntityTooLarge, status)
	assert.Contains(t, page, "The photo is too large")

	// Errors from OpenRouter are explained, never with the key.
	withFakeOpenRouter(t, s, http.StatusNotFound, `{"error":{"message":"No allowed providers are available for the selected model."}}`)
	status, page = scan(t, s, c, receiptPng, nil)
	assert.Equal(t, http.StatusBadGateway, status)
	assert.Contains(t, page, "Your OpenRouter account doesn&#39;t allow this model&#39;s provider")
	assert.NotContains(t, page, "test-key")
}

func TestScan_NeedsSessionAndSameOrigin(t *testing.T) {
	s := newTestServer(t)
	calls := withFakeOpenRouter(t, s, http.StatusOK, `{"is_receipt":true}`)

	status, _ := scan(t, s, browser(t), receiptPng, nil)
	assert.Equal(t, http.StatusSeeOther, status, "signed out")

	c := browser(t)
	signIn(t, s, c)
	status, _ = scan(t, s, c, receiptPng, map[string]string{"Sec-Fetch-Site": "cross-site"})
	assert.Equal(t, http.StatusForbidden, status, "another site can't spend the user's scans")
	assert.Zero(t, calls.Load())
}

func TestScan_FormCanBeAddedAfterwards(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)
	signIn(t, s, c)
	withFakeOpenRouter(t, s, http.StatusOK, `{"is_receipt":true,"amount":"4.20","date":"2026-11-08","time":null,"merchant":"Bakery","category":"food","note":null}`)
	status, page := scan(t, s, c, receiptPng, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, `value="2026-11-08T12:00:00"`, "noon when the receipt has no time")

	// The user checks the form and adds it as it is.
	status, _, _ = send(t, c, http.MethodPost, s.url+"/ui/expenses", url.Values{
		"amount": {"4.20"}, "category": {"food"}, "merchant": {"Bakery"}, "occurred_local": {"2026-11-08T12:00:00"}, "tz_offset": {"60"},
		"from": {"2026-11-01"}, "to": {"2026-11-30"},
	}, map[string]string{"HX-Request": "true"})
	require.Equal(t, http.StatusOK, status)
	_, _, list := send(t, c, http.MethodGet, s.url+"/?from=2026-11-01&to=2026-11-30", nil, nil)
	assert.True(t, strings.Contains(list, "Bakery") && strings.Contains(list, "4.20"))
}

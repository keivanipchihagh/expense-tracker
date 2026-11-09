package receipt

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	today      = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	categories = []string{"food", "groceries", "other"}
	image      = []byte("\x89PNG\r\n\x1a\nfake")
)

// fakeOpenRouter answers chat completions with status and body, and keeps
// the last request.
func fakeOpenRouter(t *testing.T, status int, body string) (*OpenRouter, *map[string]any, *http.Header) {
	t.Helper()
	got := map[string]any{}
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/chat/completions", r.URL.Path)
		header = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(raw, &got))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewOpenRouter("test-key", "").WithBaseUrl(srv.URL + "/api/v1"), &got, &header
}

func completion(content string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	return string(b)
}

func TestScan_Success(t *testing.T) {
	o, sent, header := fakeOpenRouter(t, http.StatusOK, completion(
		`{"is_receipt":true,"amount":"10.91","currency":"EUR","date":"2026-11-07","time":"18:42","merchant":"Rewe","category":"groceries","note":null}`))

	r, err := o.Scan(context.Background(), image, "image/png", today, categories)
	require.NoError(t, err)
	assert.Equal(t, Reading{IsReceipt: true, Amount: "10.91", Currency: "EUR", Date: "2026-11-07", Time: "18:42", Merchant: "Rewe", Category: "groceries"}, r)

	assert.Equal(t, "Bearer test-key", header.Get("Authorization"))
	req := *sent
	assert.Equal(t, DefaultModel, req["model"])
	assert.EqualValues(t, 0, req["temperature"])
	format := req["response_format"].(map[string]any)
	assert.Equal(t, "json_schema", format["type"])
	schema := format["json_schema"].(map[string]any)
	assert.Equal(t, true, schema["strict"], "the model must follow the schema")
	props := schema["schema"].(map[string]any)["properties"].(map[string]any)
	assert.Equal(t, []any{"food", "groceries", "other"}, props["category"].(map[string]any)["enum"], "categories are limited to ours")
	assert.Equal(t, map[string]any{"require_parameters": true}, req["provider"], "only endpoints that honour the schema")

	messages := req["messages"].([]any)
	user := messages[1].(map[string]any)["content"].([]any)
	assert.Contains(t, user[0].(map[string]any)["text"], "Monday, 9 November 2026", "the model knows today's date")
	assert.Equal(t, "data:image/png;base64,iVBORw0KGgpmYWtl", user[1].(map[string]any)["image_url"].(map[string]any)["url"])
}

func TestScan_LenientParsing(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Reading
	}{
		{name: "code fence", content: "```json\n{\"is_receipt\":true,\"amount\":\"5.00\",\"category\":\"food\"}\n```", want: Reading{IsReceipt: true, Amount: "5.00", Category: "food"}},
		{name: "words around it", content: "Here you go: {\"amount\":\" 7.10 \",\"merchant\":\" Cafe \"} Done.", want: Reading{IsReceipt: true, Amount: "7.10", Merchant: "Cafe"}},
		{name: "not a receipt", content: `{"is_receipt":false,"amount":null,"category":"other"}`, want: Reading{IsReceipt: false, Category: "other"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, _, _ := fakeOpenRouter(t, http.StatusOK, completion(tt.content))
			r, err := o.Scan(context.Background(), image, "image/png", today, categories)
			require.NoError(t, err)
			assert.Equal(t, tt.want, r)
		})
	}
}

func TestScan_Errors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantMsg   string
		wantRetry bool
	}{
		{name: "bad key", status: 401, body: `{"error":{"message":"No auth credentials found"}}`, wantMsg: "OpenRouter rejected the API key"},
		{name: "no credits", status: 402, body: `{"error":{"message":"Insufficient credits"}}`, wantMsg: "out of credits"},
		{name: "provider not allowed", status: 404, body: `{"error":{"message":"No allowed providers are available for the selected model.","metadata":{"available_providers":["nvidia"],"requested_providers":["liquid","minimax"]}}}`, wantMsg: "doesn't allow NVIDIA, which runs this model. Add it under Allowed providers at openrouter.ai/settings/privacy."},
		{name: "provider not allowed, unnamed", status: 404, body: `{"error":{"message":"No allowed providers are available for the selected model."}}`, wantMsg: "doesn't allow this model's provider"},
		{name: "data policy", status: 404, body: `{"error":{"message":"No endpoints found matching your data policy"}}`, wantMsg: "Allow free endpoints at openrouter.ai/settings/privacy"},
		{name: "unknown model", status: 404, body: `{"error":{"message":"No endpoints found for x/y"}}`, wantMsg: "OPENROUTER_MODEL"},
		{name: "rate limited", status: 429, body: `{"error":{"message":"Rate limit exceeded: free-models-per-min"}}`, wantMsg: "busy", wantRetry: true},
		{name: "upstream down", status: 502, body: `bad gateway`, wantMsg: "unavailable", wantRetry: true},
		{name: "error inside a 200", status: 200, body: `{"error":{"code":502,"message":"Provider returned error"}}`, wantMsg: "failed to answer", wantRetry: true},
		{name: "no choices", status: 200, body: `{"choices":[]}`, wantMsg: "no answer", wantRetry: true},
		{name: "not JSON", status: 200, body: completion("I can't read this image."), wantMsg: "expected format", wantRetry: true},
		{name: "broken JSON", status: 200, body: completion(`{"amount": 12.5,}`), wantMsg: "expected format", wantRetry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, _, _ := fakeOpenRouter(t, tt.status, tt.body)
			_, err := o.Scan(context.Background(), image, "image/png", today, categories)
			var scanErr *Error
			require.True(t, errors.As(err, &scanErr), "got %v", err)
			assert.Contains(t, scanErr.Message, tt.wantMsg)
			assert.Equal(t, tt.wantRetry, scanErr.Retry)
			assert.NotContains(t, err.Error(), "test-key", "the key never shows up in errors")
		})
	}
}

func TestScan_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reading the body lets the server notice when the client gives up.
		_, _ = io.ReadAll(r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	o := NewOpenRouter("k", "some/model").WithBaseUrl(srv.URL)
	assert.Equal(t, "some/model", o.Model())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := o.Scan(ctx, image, "image/png", today, categories)
	var scanErr *Error
	require.ErrorAs(t, err, &scanErr)
	assert.Contains(t, scanErr.Message, "took too long")
	assert.True(t, scanErr.Retry)
}

func TestScan_Unreachable(t *testing.T) {
	o := NewOpenRouter("k", "").WithBaseUrl("http://127.0.0.1:1")
	_, err := o.Scan(context.Background(), image, "image/png", today, categories)
	var scanErr *Error
	require.ErrorAs(t, err, &scanErr)
	assert.True(t, strings.HasPrefix(scanErr.Message, "Couldn't reach OpenRouter"))
}

package tests

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApps_ListAndRevoke(t *testing.T) {
	s := newTestServer(t)
	s.login(t) // an OAuth client named "Test Client" signs in
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/api/v1/expenses", "", nil))

	c := browser(t)
	signIn(t, s, c)
	status, _, page := send(t, c, http.MethodGet, s.url+"/apps", nil, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, page, "Test Client")
	assert.Contains(t, page, "Redirects to localhost:9999")

	// Find the client id in the revoke button and revoke it.
	start := strings.Index(page, `hx-post="/ui/apps/`)
	require.Positive(t, start)
	path := page[start+len(`hx-post="`):]
	path = path[:strings.Index(path, `"`)]

	status, _, body := send(t, c, http.MethodPost, s.url+path, url.Values{}, map[string]string{"HX-Request": "true"})
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, body, "the empty response removes the row")

	assert.Equal(t, http.StatusUnauthorized, s.call(t, http.MethodGet, "/api/v1/expenses", "", nil),
		"the revoked app's access token stops working at once")

	_, _, page = send(t, c, http.MethodGet, s.url+"/apps", nil, nil)
	assert.Contains(t, page, "No connected apps.")
}

func TestApps_RequiresSession(t *testing.T) {
	s := newTestServer(t)
	c := browser(t)

	status, header, _ := send(t, c, http.MethodGet, s.url+"/apps", nil, nil)
	assert.Equal(t, http.StatusSeeOther, status)
	assert.Equal(t, "/login", header.Get("Location"))

	status, _, _ = send(t, c, http.MethodPost, s.url+"/ui/apps/anything/revoke", url.Values{}, map[string]string{"HX-Request": "true"})
	assert.Equal(t, http.StatusUnauthorized, status)
}

func TestErrorPages(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantJson   string
		wantHtml   bool
	}{
		{name: "unknown api path", method: http.MethodGet, path: "/api/v1/nope", wantStatus: http.StatusUnauthorized},
		{name: "unknown oauth path", method: http.MethodGet, path: "/oauth/nope", wantStatus: 404, wantJson: "not_found"},
		{name: "wrong method on health", method: http.MethodPost, path: "/health", wantStatus: 405, wantJson: "method_not_allowed"},
		{name: "wrong method on token", method: http.MethodGet, path: "/oauth/token", wantStatus: 405, wantJson: "method_not_allowed"},
		{name: "unknown page", method: http.MethodGet, path: "/nope", wantStatus: 404, wantHtml: true},
		{name: "unknown static file", method: http.MethodGet, path: "/static/nope.js", wantStatus: 404, wantHtml: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, s.url+tt.path, nil)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			switch {
			case tt.wantJson != "":
				assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
				var body struct {
					Error struct{ Code string } `json:"error"`
				}
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
				assert.Equal(t, tt.wantJson, body.Error.Code)
			case tt.wantHtml:
				assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
				assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "default-src 'self'")
				page, _ := io.ReadAll(resp.Body)
				assert.Contains(t, string(page), `<p class="error-code">404</p>`)
				assert.Contains(t, string(page), "Go to expenses")
			}
		})
	}

	s.login(t)
	var body map[string]any
	assert.Equal(t, http.StatusNotFound, s.call(t, http.MethodGet, "/api/v1/nope", "", &body), "with a token, unknown API paths are a JSON 404")
	assert.Equal(t, map[string]any{"error": map[string]any{"code": "not_found", "message": "not found"}}, body)
	assert.Equal(t, http.StatusMethodNotAllowed, s.call(t, http.MethodPut, "/api/v1/expenses", "", &body))
	assert.Equal(t, "method_not_allowed", body["error"].(map[string]any)["code"])
}

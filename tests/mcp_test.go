package tests

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bearer adds an access token to every request, as an MCP client does
// after its OAuth login.
type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// connectMcp logs in and opens an MCP session against /mcp.
func connectMcp(t *testing.T, s *testServer) *mcp.ClientSession {
	t.Helper()
	s.login(t)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   s.url + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: s.token, next: http.DefaultTransport}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool calls a tool and returns its text and whether it was an error.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text, res.IsError
}

func TestMcp_RequiresToken(t *testing.T) {
	s := newTestServer(t)

	resp, err := http.Post(s.url+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, `Bearer resource_metadata="`+s.url+`/.well-known/oauth-protected-resource/mcp"`, resp.Header.Get("WWW-Authenticate"),
		"MCP clients are pointed at the metadata for the /mcp resource")

	var metadata map[string]any
	require.Equal(t, http.StatusOK, s.call(t, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", "", &metadata))
	assert.Equal(t, s.url+"/mcp", metadata["resource"])
	assert.Equal(t, []any{s.url}, metadata["authorization_servers"])
}

func TestMcp_Tools(t *testing.T) {
	s := newTestServer(t)
	session := connectMcp(t, s)

	assert.Contains(t, session.InitializeResult().Instructions, "occurred_at")

	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := []string{}
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	assert.ElementsMatch(t, []string{"create_expense", "list_expenses", "get_expense", "update_expense", "delete_expense"}, names)

	for _, tool := range tools.Tools {
		if tool.Name == "create_expense" {
			schema, _ := json.Marshal(tool.InputSchema)
			assert.NotContains(t, string(schema), "currency", "expenses have no currency")
			assert.Contains(t, string(schema), `"required":["amount","occurred_at","category"]`)
		}
		if tool.Name == "delete_expense" {
			require.NotNil(t, tool.Annotations.DestructiveHint)
			assert.True(t, *tool.Annotations.DestructiveHint)
		}
	}
}

func TestMcp_ExpenseLifecycle(t *testing.T) {
	s := newTestServer(t)
	session := connectMcp(t, s)

	// Create
	text, isErr := callTool(t, session, "create_expense", map[string]any{
		"amount": "12.5", "occurred_at": "2026-11-07T13:20:00+01:00",
		"category": "food", "merchant": "Rewe", "note": "lunch with the team",
	})
	require.False(t, isErr, text)
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	id := created["id"].(string)
	assert.Equal(t, "12.50", created["amount"])

	// List by date
	text, isErr = callTool(t, session, "list_expenses", map[string]any{"date_from": "2026-11-07", "date_to": "2026-11-07"})
	require.False(t, isErr, text)
	var listed struct {
		Expenses []map[string]any `json:"expenses"`
		Count    int              `json:"count"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &listed))
	require.Equal(t, 1, listed.Count)
	assert.Equal(t, id, listed.Expenses[0]["id"])

	// List by category and merchant (part of the name, any case)
	text, isErr = callTool(t, session, "list_expenses", map[string]any{"category": "food", "merchant": "REW"})
	require.False(t, isErr, text)
	require.NoError(t, json.Unmarshal([]byte(text), &listed))
	assert.Equal(t, 1, listed.Count)

	text, isErr = callTool(t, session, "list_expenses", map[string]any{"category": "groceries", "merchant": "rewe"})
	require.False(t, isErr, text)
	require.NoError(t, json.Unmarshal([]byte(text), &listed))
	assert.Equal(t, 0, listed.Count, "filters combine")

	// Update: change the amount and clear the merchant with null
	text, isErr = callTool(t, session, "update_expense", map[string]any{"id": id, "amount": "20", "merchant": nil})
	require.False(t, isErr, text)
	var updated map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &updated))
	assert.Equal(t, "20.00", updated["amount"])
	assert.Nil(t, updated["merchant"])
	assert.Equal(t, "lunch with the team", updated["note"], "fields left out are unchanged")

	// Get
	text, isErr = callTool(t, session, "get_expense", map[string]any{"id": id})
	require.False(t, isErr, text)
	assert.Contains(t, text, `"amount":"20.00"`)

	// Delete
	text, isErr = callTool(t, session, "delete_expense", map[string]any{"id": id})
	require.False(t, isErr, text)
	assert.JSONEq(t, `{"deleted":true,"id":"`+id+`"}`, text)

	text, isErr = callTool(t, session, "get_expense", map[string]any{"id": id})
	assert.True(t, isErr)
	assert.Equal(t, "expense not found", text)

	assert.Equal(t, []string{"create", "update", "delete"}, s.logActions(t), "MCP writes are logged like any other")
}

func TestMcp_ValidationErrorsAreReadable(t *testing.T) {
	s := newTestServer(t)
	session := connectMcp(t, s)

	text, isErr := callTool(t, session, "create_expense", map[string]any{
		"amount": "1.234", "currency": "GBP", "occurred_at": "2026-11-07T13:20:00", "category": "food", "tip": "2",
	})
	assert.True(t, isErr)
	assert.Equal(t, "validation failed:\n"+
		"- amount: must have at most 2 decimal places\n"+
		"- currency: unknown field\n"+
		"- occurred_at: must be an RFC 3339 timestamp with a UTC offset, e.g. 2026-11-07T13:20:00+01:00\n"+
		"- tip: unknown field", text, "every problem is listed so the agent can fix its call in one go")

	text, isErr = callTool(t, session, "list_expenses", map[string]any{"date_from": "yesterday", "category": "pets", "merchant": 5, "limit": 3})
	assert.True(t, isErr)
	assert.Contains(t, text, "date_from: must be a date in YYYY-MM-DD format")
	assert.Contains(t, text, "merchant: must be a string")
	assert.Contains(t, text, "limit: unknown field")

	text, isErr = callTool(t, session, "list_expenses", map[string]any{"category": "pets"})
	assert.True(t, isErr)
	assert.Contains(t, text, "category: must be one of")

	text, isErr = callTool(t, session, "update_expense", map[string]any{"amount": "5"})
	assert.True(t, isErr)
	assert.Equal(t, "id is required", text)

	text, isErr = callTool(t, session, "update_expense", map[string]any{"id": "missing", "amount": "5"})
	assert.True(t, isErr)
	assert.Equal(t, "expense not found", text)

	assert.Empty(t, s.logActions(t), "nothing is written")
}

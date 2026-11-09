package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpInstructions is sent to clients when they connect; agents use it to
// decide how to fill in the tool arguments.
const mcpInstructions = `Expense log for one person. Use these tools to record, correct, look up and delete their expenses.

- amount: decimal string with at most 2 decimals, e.g. "12.50". Always positive. Amounts have no currency: the user picks one display currency in the web UI. If the user names a different currency, ask before logging.
- occurred_at: RFC 3339 timestamp WITH the user's UTC offset, e.g. "2026-11-07T13:20:00+01:00". If the user doesn't say when, use the current time in their time zone.
- category: pick the closest of the allowed values; use "other" if nothing fits.
- merchant: the store or vendor, if known.
- note: only extra words the user said about the expense. Leave it out otherwise; don't invent one.

To fix a mistake, use update_expense with only the fields that change. To find an expense's id, call list_expenses for its date. To answer questions like "how much at Rewe this month?", filter list_expenses by merchant or category.`

// NewMcpHandler returns the MCP endpoint (Streamable HTTP, stateless). It
// exposes the same operations as the REST API and goes through the same
// handler, so validation and change logs are identical.
func (s *Server) NewMcpHandler() http.Handler {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "expense-tracker", Version: s.version},
		&mcp.ServerOptions{Instructions: mcpInstructions},
	)

	destructive := true
	server.AddTool(&mcp.Tool{
		Name:        "create_expense",
		Description: "Record a new expense. Returns the stored expense, including its id.",
		InputSchema: objectSchema(expenseProperties(), "amount", "occurred_at", "category"),
		Annotations: &mcp.ToolAnnotations{Title: "Create expense"},
	}, s.mcpCreateExpense)

	server.AddTool(&mcp.Tool{
		Name:        "list_expenses",
		Description: "List expenses oldest first, optionally filtered by local date range, category and merchant. All filters are optional and combine.",
		InputSchema: objectSchema(map[string]any{
			"date_from": map[string]any{"type": "string", "format": "date", "description": "First local date, YYYY-MM-DD (inclusive)."},
			"date_to":   map[string]any{"type": "string", "format": "date", "description": "Last local date, YYYY-MM-DD (inclusive)."},
			"category":  map[string]any{"type": "string", "enum": toStrings(models.Categories), "description": "Only expenses in this category."},
			"merchant":  map[string]any{"type": "string", "maxLength": models.MaxMerchantLength, "description": `Only expenses whose merchant contains this text, ignoring case, e.g. "rewe".`},
		}),
		Annotations: &mcp.ToolAnnotations{Title: "List expenses", ReadOnlyHint: true},
	}, s.mcpListExpenses)

	server.AddTool(&mcp.Tool{
		Name:        "get_expense",
		Description: "Get one expense by id.",
		InputSchema: objectSchema(map[string]any{"id": idProperty()}, "id"),
		Annotations: &mcp.ToolAnnotations{Title: "Get expense", ReadOnlyHint: true},
	}, s.mcpGetExpense)

	updateProps := expenseProperties()
	updateProps["id"] = idProperty()
	for _, field := range []string{"merchant", "note"} {
		p := updateProps[field].(map[string]any)
		p["type"] = []string{"string", "null"}
		p["description"] = p["description"].(string) + " Send null to clear it."
	}
	server.AddTool(&mcp.Tool{
		Name:        "update_expense",
		Description: "Correct an expense. Send only the fields that change; fields left out stay as they are.",
		InputSchema: objectSchema(updateProps, "id"),
		Annotations: &mcp.ToolAnnotations{Title: "Update expense", IdempotentHint: true},
	}, s.mcpUpdateExpense)

	server.AddTool(&mcp.Tool{
		Name:        "delete_expense",
		Description: "Permanently delete an expense. Confirm with the user first unless they clearly asked for it.",
		InputSchema: objectSchema(map[string]any{"id": idProperty()}, "id"),
		Annotations: &mcp.ToolAnnotations{Title: "Delete expense", DestructiveHint: &destructive, IdempotentHint: true},
	}, s.mcpDeleteExpense)

	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
}

func (s *Server) mcpCreateExpense(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := toolArguments(req)
	if err != nil {
		return toolError(err.Error()), nil
	}
	expense, err := s.handler.CreateExpense(ctx, args)
	if err != nil {
		return handlerToolError(ctx, err), nil
	}
	return toolResult(expense)
}

func (s *Server) mcpListExpenses(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, err := toolArguments(req)
	if err != nil {
		return toolError(err.Error()), nil
	}

	fieldErrors := map[string]string{}
	filter := models.ExpenseFilter{
		DateFrom: dateArgument(args, "date_from", fieldErrors),
		DateTo:   dateArgument(args, "date_to", fieldErrors),
		Category: models.Category(stringArgument(args, "category", fieldErrors)),
		Merchant: stringArgument(args, "merchant", fieldErrors),
	}
	for key := range args {
		switch key {
		case "date_from", "date_to", "category", "merchant":
		default:
			fieldErrors[key] = "unknown field"
		}
	}
	if len(fieldErrors) > 0 {
		return handlerToolError(ctx, pkg.NewValidationError(fieldErrors)), nil
	}

	expenses, err := s.handler.ListExpenses(ctx, filter)
	if err != nil {
		return handlerToolError(ctx, err), nil
	}
	return toolResult(map[string]any{"expenses": expenses, "count": len(expenses)})
}

func (s *Server) mcpGetExpense(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, _, err := idArgument(req)
	if err != nil {
		return toolError(err.Error()), nil
	}
	expense, err := s.handler.GetExpense(ctx, id)
	if err != nil {
		return handlerToolError(ctx, err), nil
	}
	return toolResult(expense)
}

func (s *Server) mcpUpdateExpense(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, patch, err := idArgument(req)
	if err != nil {
		return toolError(err.Error()), nil
	}
	expense, err := s.handler.UpdateExpense(ctx, id, patch)
	if err != nil {
		return handlerToolError(ctx, err), nil
	}
	return toolResult(expense)
}

func (s *Server) mcpDeleteExpense(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, rest, err := idArgument(req)
	if err != nil {
		return toolError(err.Error()), nil
	}
	if len(rest) > 0 {
		return toolError("delete_expense takes only id"), nil
	}
	if err := s.handler.DeleteExpense(ctx, id); err != nil {
		return handlerToolError(ctx, err), nil
	}
	return toolResult(map[string]any{"deleted": true, "id": id})
}

// toolArguments decodes the arguments like the REST API decodes a body:
// numbers stay json.Number and a null value is kept as a nil entry.
func toolArguments(req *mcp.CallToolRequest) (map[string]any, error) {
	args := map[string]any{}
	raw := req.Params.Arguments
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return args, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil {
		return nil, errors.New("arguments must be a JSON object")
	}
	return args, nil
}

// idArgument splits the required "id" from the other arguments.
func idArgument(req *mcp.CallToolRequest) (string, map[string]any, error) {
	args, err := toolArguments(req)
	if err != nil {
		return "", nil, err
	}
	id, ok := args["id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return "", nil, errors.New("id is required")
	}
	delete(args, "id")
	return id, args, nil
}

func dateArgument(args map[string]any, key string, fieldErrors map[string]string) time.Time {
	v, ok := args[key]
	if !ok || v == nil || v == "" {
		return time.Time{}
	}
	s, _ := v.(string)
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		fieldErrors[key] = "must be a date in YYYY-MM-DD format"
		return time.Time{}
	}
	return t
}

// stringArgument reads an optional string argument; missing or null is "".
func stringArgument(args map[string]any, key string, fieldErrors map[string]string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	s, isString := v.(string)
	if !isString {
		fieldErrors[key] = "must be a string"
	}
	return s
}

// toolResult returns v as structured content and as JSON text for clients
// that only read text.
func toolResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(b)}},
		StructuredContent: json.RawMessage(b),
	}, nil
}

// toolError is a failed tool call the agent can read and act on.
func toolError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}

// handlerToolError turns a handler error into a tool error. Validation
// errors list every field and why, so the agent can correct its call.
func handlerToolError(ctx context.Context, err error) *mcp.CallToolResult {
	var appErr *pkg.Error
	if !errors.As(err, &appErr) {
		slog.ErrorContext(ctx, "mcp tool failed", "error", err)
		return toolError("internal server error")
	}
	if len(appErr.Fields) == 0 {
		return toolError(appErr.Message)
	}

	keys := make([]string, 0, len(appErr.Fields))
	for k := range appErr.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = fmt.Sprintf("- %s: %s", k, appErr.Fields[k])
	}
	return toolError(appErr.Message + ":\n" + strings.Join(lines, "\n"))
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func idProperty() map[string]any {
	return map[string]any{"type": "string", "description": "The expense id, as returned by create_expense or list_expenses."}
}

// expenseProperties describes the writable expense fields.
func expenseProperties() map[string]any {
	return map[string]any{
		"amount": map[string]any{
			"type":        "string",
			"pattern":     `^\d+(\.\d{1,2})?$`,
			"description": `Positive decimal amount with at most 2 decimals, e.g. "12.50".`,
		},
		"occurred_at": map[string]any{
			"type":        "string",
			"format":      "date-time",
			"description": `When the expense happened, RFC 3339 with the user's UTC offset, e.g. "2026-11-07T13:20:00+01:00".`,
		},
		"category": map[string]any{
			"type":        "string",
			"enum":        toStrings(models.Categories),
			"description": `Kind of spending. "food" is eating out; "groceries" is food shopping.`,
		},
		"merchant": map[string]any{
			"type":        "string",
			"maxLength":   models.MaxMerchantLength,
			"description": "Store or vendor, e.g. \"Rewe\". Optional.",
		},
		"note": map[string]any{
			"type":        "string",
			"maxLength":   models.MaxNoteLength,
			"description": "Extra words the user said about the expense. Only set it when the user adds something; never invent one.",
		},
	}
}

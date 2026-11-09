// Package receipt reads an expense off a photo of a receipt with a vision
// model on OpenRouter. The model's answer is a draft: the caller checks it
// and the user confirms it before anything is saved.
package receipt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultModel is a free vision model with strict structured output.
const DefaultModel = "nvidia/nemotron-3.5-super-vl:free"

const defaultBaseUrl = "https://openrouter.ai/api/v1"

// Reading is what the model read off the image. Fields it couldn't read are
// empty. Nothing here is validated yet.
type Reading struct {
	IsReceipt bool   `json:"is_receipt"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	Date      string `json:"date"`
	Time      string `json:"time"`
	Merchant  string `json:"merchant"`
	Category  string `json:"category"`
	Note      string `json:"note"`
}

// Scanner reads a receipt image.
type Scanner interface {
	// Scan sends the image (JPEG, PNG or WebP bytes of the given MIME type)
	// to the model. today helps it resolve dates like "07.11.26". Errors
	// are *Error with a message fit to show the user.
	Scan(ctx context.Context, image []byte, mimeType string, today time.Time, categories []string) (Reading, error)
}

// Error is a failure to read the receipt, with a message for the user.
type Error struct {
	Message string
	// Retry is true when trying again later may work (rate limit, timeout).
	Retry bool
	err   error
}

func (e *Error) Error() string {
	if e.err != nil {
		return e.Message + ": " + e.err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.err }

// OpenRouter is a Scanner backed by OpenRouter's chat completions API.
type OpenRouter struct {
	apiKey  string
	model   string
	baseUrl string
	client  *http.Client
}

// NewOpenRouter returns a scanner using apiKey and model (DefaultModel if
// empty).
func NewOpenRouter(apiKey, model string) *OpenRouter {
	if model == "" {
		model = DefaultModel
	}
	return &OpenRouter{apiKey: apiKey, model: model, baseUrl: defaultBaseUrl, client: &http.Client{}}
}

// WithBaseUrl points the scanner at another API root, for tests.
func (o *OpenRouter) WithBaseUrl(u string) *OpenRouter {
	o.baseUrl = strings.TrimSuffix(u, "/")
	return o
}

// Model is the model the scanner asks.
func (o *OpenRouter) Model() string { return o.model }

const systemPrompt = `You read photos of receipts, invoices and bills and extract the one expense they record.
Rules:
- Use only what is printed. Use null for anything missing or unreadable; never guess an amount.
- amount is the grand total actually paid (after discounts, including tax and tip), as digits with a dot and two decimals, e.g. 1234.50. Convert decimal commas.
- currency is the ISO 4217 code of the total. Work it out from the symbol together with the address or country when that makes it clear (e.g. $ at a US address is USD, € is EUR); null if it can't be told.
- date is YYYY-MM-DD and time is HH:MM (24-hour). Day-first formats like 07.11.2026 are the usual in Europe.
- merchant is the name people use for the store or business, e.g. "Rewe" rather than "REWE Markt GmbH".
- category: food is eating out (restaurants, cafes, takeaway, delivery); groceries is supermarkets and food shops; pick "other" if nothing fits.
- note says briefly what was bought, at most 60 characters, in the receipt's language or English.
- is_receipt is false when the image is not a receipt, invoice or bill; then use null everywhere else and category "other".`

func schema(categories []string) map[string]any {
	nullable := func(desc string) map[string]any {
		return map[string]any{"type": []string{"string", "null"}, "description": desc}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"is_receipt", "amount", "currency", "date", "time", "merchant", "category", "note"},
		"properties": map[string]any{
			"is_receipt": map[string]any{"type": "boolean", "description": "false if the image is not a receipt, invoice or bill"},
			"amount":     nullable("grand total paid, e.g. 12.50"),
			"currency":   nullable("ISO 4217 code of the total, e.g. EUR"),
			"date":       nullable("purchase date, YYYY-MM-DD"),
			"time":       nullable("purchase time, HH:MM"),
			"merchant":   nullable("store or business name"),
			"category":   map[string]any{"type": "string", "enum": categories},
			"note":       nullable("what was bought, at most 60 characters"),
		},
	}
}

func (o *OpenRouter) Scan(ctx context.Context, image []byte, mimeType string, today time.Time, categories []string) (Reading, error) {
	body, err := json.Marshal(map[string]any{
		"model":       o.model,
		"temperature": 0,
		"max_tokens":  4000,
		"messages": []any{
			map[string]any{"role": "system", "content": systemPrompt},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "Today is " + today.Format("Monday, 2 January 2006") + ". Extract the expense from this image."},
				map[string]any{"type": "image_url", "image_url": map[string]string{
					"url": "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(image),
				}},
			}},
		},
		"response_format": map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "expense", "strict": true, "schema": schema(categories)},
		},
		// Only use endpoints that honour the schema, and leave out any
		// reasoning text from the answer.
		"provider":  map[string]any{"require_parameters": true},
		"reasoning": map[string]any{"exclude": true},
	})
	if err != nil {
		return Reading{}, &Error{Message: "Couldn't prepare the request.", err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseUrl+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Reading{}, &Error{Message: "Couldn't prepare the request.", err: err}
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Title", "expense-tracker")

	resp, err := o.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Reading{}, &Error{Message: "The model took too long to answer. Try again.", Retry: true, err: err}
		}
		return Reading{}, &Error{Message: "Couldn't reach OpenRouter.", Retry: true, err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Reading{}, &Error{Message: "Couldn't read the model's answer.", Retry: true, err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return Reading{}, statusError(resp.StatusCode, raw)
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &completion); err != nil {
		return Reading{}, &Error{Message: "The model's answer couldn't be read.", Retry: true, err: err}
	}
	// OpenRouter can report an upstream failure inside a 200 response.
	if completion.Error != nil {
		return Reading{}, &Error{Message: "The model failed to answer. Try again.", Retry: true, err: errors.New(completion.Error.Message)}
	}
	if len(completion.Choices) == 0 {
		return Reading{}, &Error{Message: "The model returned no answer. Try again.", Retry: true}
	}
	return parseReading(completion.Choices[0].Message.Content)
}

// parseReading decodes the model's JSON. Null fields decode as "". Some
// models wrap the object in a Markdown code fence or add words around it,
// so only the outermost {...} is read.
func parseReading(content string) (Reading, error) {
	start, end := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return Reading{}, &Error{Message: "The model's answer wasn't in the expected format. Try again.", Retry: true, err: fmt.Errorf("no JSON object in %q", truncate(content, 200))}
	}
	var r struct {
		IsReceipt *bool   `json:"is_receipt"`
		Amount    *string `json:"amount"`
		Currency  *string `json:"currency"`
		Date      *string `json:"date"`
		Time      *string `json:"time"`
		Merchant  *string `json:"merchant"`
		Category  *string `json:"category"`
		Note      *string `json:"note"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &r); err != nil {
		return Reading{}, &Error{Message: "The model's answer wasn't in the expected format. Try again.", Retry: true, err: err}
	}
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	return Reading{
		// Missing means the model didn't say it isn't one.
		IsReceipt: r.IsReceipt == nil || *r.IsReceipt,
		Amount:    s(r.Amount),
		Currency:  s(r.Currency),
		Date:      s(r.Date),
		Time:      s(r.Time),
		Merchant:  s(r.Merchant),
		Category:  s(r.Category),
		Note:      s(r.Note),
	}, nil
}

// statusError explains an OpenRouter error response. The API key is never
// part of it.
func statusError(status int, body []byte) error {
	var parsed struct {
		Error struct {
			Message  string `json:"message"`
			Metadata struct {
				Available []string `json:"available_providers"`
			} `json:"metadata"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	detail := parsed.Error.Message
	err := fmt.Errorf("openrouter returned %d: %s", status, truncate(detail, 300))
	lower := strings.ToLower(detail)
	switch {
	case status == http.StatusUnauthorized:
		return &Error{Message: "OpenRouter rejected the API key. Check OPENROUTER_API_KEY.", err: err}
	case status == http.StatusPaymentRequired:
		return &Error{Message: "OpenRouter says the account is out of credits.", err: err}
	case status == http.StatusNotFound && strings.Contains(lower, "allowed provider"):
		provider := "this model's provider"
		if names := parsed.Error.Metadata.Available; len(names) > 0 {
			provider = providerNames(names)
		}
		return &Error{Message: "Your OpenRouter account doesn't allow " + provider + ", which runs this model. Add it under Allowed providers at openrouter.ai/settings/privacy.", err: err}
	case status == http.StatusNotFound && (strings.Contains(lower, "data policy") || strings.Contains(lower, "privacy")):
		return &Error{Message: "Your OpenRouter privacy settings block this free model. Allow free endpoints at openrouter.ai/settings/privacy.", err: err}
	case status == http.StatusNotFound:
		return &Error{Message: "OpenRouter has no endpoint for this model right now. Check OPENROUTER_MODEL.", err: err}
	case status == http.StatusRequestEntityTooLarge:
		return &Error{Message: "The photo is too large for the model.", err: err}
	case status == http.StatusTooManyRequests:
		return &Error{Message: "The free model is busy. Try again in a minute.", Retry: true, err: err}
	case status >= 500:
		return &Error{Message: "The model is unavailable right now. Try again.", Retry: true, err: err}
	}
	return &Error{Message: "OpenRouter refused the request.", err: err}
}

// providerNames turns OpenRouter's provider slugs into names to show,
// e.g. ["nvidia"] into "NVIDIA".
func providerNames(slugs []string) string {
	known := map[string]string{"nvidia": "NVIDIA", "google-ai-studio": "Google AI Studio", "atlas-cloud": "AtlasCloud", "minimax": "MiniMax", "liquid": "Liquid"}
	names := make([]string, len(slugs))
	for i, s := range slugs {
		if n, ok := known[strings.ToLower(s)]; ok {
			names[i] = n
		} else {
			names[i] = s
		}
	}
	return strings.Join(names, " or ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/keivanipchihagh/expense-tracker/internal/receipt"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/shopspring/decimal"
)

const (
	// MaxReceiptBytes caps an uploaded receipt image. The UI shrinks photos
	// well below this before sending them.
	MaxReceiptBytes = 4 << 20
	// maxScans is how many receipts one user may scan per scanWindow; the
	// free model's quota is shared by everyone on the server.
	maxScans   = 30
	scanWindow = time.Hour
	// maxReceiptNote is the longest note taken from a receipt.
	maxReceiptNote = 200
	// MaxExpenseText is the longest message ReadText takes.
	MaxExpenseText = 500
)

var (
	// ErrScanDisabled is returned when no OpenRouter key is configured.
	ErrScanDisabled = &pkg.Error{Status: http.StatusNotFound, Code: "scan_disabled", Message: "Receipt scanning isn't set up on this server."}
	// ErrTooManyScans is returned when a user hits the scan rate limit.
	ErrTooManyScans = &pkg.Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "You've scanned a lot of receipts in the last hour. Try again later."}
)

// receiptTypes are the image types sent to the model, by sniffed type.
var receiptTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// ReceiptDraft is an expense read off a receipt, checked and ready to fill
// the expense form. Fields that couldn't be read or didn't pass the checks
// are empty, with a warning saying so. Nothing is saved.
type ReceiptDraft struct {
	Amount   string // e.g. "12.50"
	Date     string // YYYY-MM-DD
	Time     string // HH:MM
	Merchant string
	Category models.Category
	Note     string
	// Currency is the receipt's ISO code, if printed.
	Currency string
	Warnings []string
}

// SetReceiptScanner turns on receipt scanning.
func (h *Handler) SetReceiptScanner(s receipt.Scanner) { h.scanner = s }

// ScanEnabled reports whether receipts can be scanned.
func (h *Handler) ScanEnabled() bool { return h.scanner != nil }

// ScanReceipt reads an expense off a receipt image for the signed-in user.
// loc is the user's time zone, for "today". The result is only a draft for
// the user to check: every field the model returns is validated here, and
// anything doubtful is dropped or flagged.
func (h *Handler) ScanReceipt(ctx context.Context, image []byte, loc *time.Location) (ReceiptDraft, error) {
	if h.scanner == nil {
		return ReceiptDraft{}, ErrScanDisabled
	}
	mimeType, err := CheckReceiptImage(image)
	if err != nil {
		return ReceiptDraft{}, err
	}
	return h.readExpense(ctx, sourceReceipt, loc, func(today time.Time, categories []string) (receipt.Reading, error) {
		return h.scanner.Scan(ctx, image, mimeType, today, categories)
	})
}

// ReadText reads an expense out of what the signed-in user typed or said,
// e.g. "12.50 lunch at Rewe yesterday". Only what the text says is filled
// in; loc is the user's time zone, for "yesterday".
func (h *Handler) ReadText(ctx context.Context, text string, loc *time.Location) (ReceiptDraft, error) {
	if h.scanner == nil {
		return ReceiptDraft{}, ErrScanDisabled
	}
	text = strings.Join(strings.Fields(text), " ")
	switch {
	case text == "":
		return ReceiptDraft{}, pkg.NewValidationError(map[string]string{"text": "is required"})
	case utf8.RuneCountInString(text) > MaxExpenseText:
		return ReceiptDraft{}, pkg.NewValidationError(map[string]string{"text": fmt.Sprintf("must be at most %d characters", MaxExpenseText)})
	}
	return h.readExpense(ctx, sourceText, loc, func(today time.Time, categories []string) (receipt.Reading, error) {
		return h.scanner.ReadText(ctx, text, today, categories)
	})
}

// draftSource is where a draft was read from; it changes the warnings.
type draftSource int

const (
	sourceReceipt draftSource = iota
	sourceText
)

// readExpense rate limits, asks the model through ask, and checks the
// answer.
func (h *Handler) readExpense(ctx context.Context, source draftSource, loc *time.Location, ask func(today time.Time, categories []string) (receipt.Reading, error)) (ReceiptDraft, error) {
	uid, err := userId(ctx)
	if err != nil {
		return ReceiptDraft{}, err
	}
	now := h.now()
	if h.scans.full(uid, now) {
		return ReceiptDraft{}, ErrTooManyScans
	}
	h.scans.record(uid, now)
	if loc == nil {
		loc = time.UTC
	}
	today := now.In(loc)

	currency, err := h.DisplayCurrency(ctx)
	if err != nil {
		return ReceiptDraft{}, err
	}
	categories, err := h.CategoryNames(ctx)
	if err != nil {
		return ReceiptDraft{}, err
	}
	started := time.Now()
	reading, err := ask(today, toStrings(categories))
	if err != nil {
		if scanErr, ok := errors.AsType[*receipt.Error](err); ok {
			h.logger.WarnContext(ctx, "reading an expense failed", "user_id", uid, "error", err)
			status := http.StatusBadGateway
			if scanErr.Retry {
				status = http.StatusServiceUnavailable
			}
			return ReceiptDraft{}, &pkg.Error{Status: status, Code: "scan_failed", Message: scanErr.Message}
		}
		h.logger.ErrorContext(ctx, "reading an expense failed", "user_id", uid, "error", err)
		return ReceiptDraft{}, pkg.InternalError
	}
	h.logger.InfoContext(ctx, "expense read", "user_id", uid, "from_text", source == sourceText, "duration_ms", time.Since(started).Milliseconds(), "is_expense", reading.IsExpense)
	if !reading.IsExpense {
		if source == sourceText {
			return ReceiptDraft{}, &pkg.Error{Status: http.StatusUnprocessableEntity, Code: "no_expense", Message: "Couldn't find an expense in that. Try something like \"12.50 lunch at Rewe\"."}
		}
		return ReceiptDraft{}, &pkg.Error{Status: http.StatusUnprocessableEntity, Code: "not_a_receipt", Message: "That doesn't look like a receipt. Try a clearer photo of the whole receipt."}
	}
	return draftFrom(reading, today, currency, categories, source), nil
}

// CheckReceiptImage returns the image's type, sniffed from its bytes (not
// the file name or the browser's claim), or a validation error.
func CheckReceiptImage(image []byte) (string, error) {
	switch {
	case len(image) == 0:
		return "", pkg.NewValidationError(map[string]string{"receipt": "is required"})
	case len(image) > MaxReceiptBytes:
		return "", pkg.NewValidationError(map[string]string{"receipt": "must be at most 4 MB"})
	}
	mimeType := http.DetectContentType(image)
	if !receiptTypes[mimeType] {
		return "", pkg.NewValidationError(map[string]string{"receipt": "must be a JPEG, PNG or WebP image"})
	}
	return mimeType, nil
}

// AttachReceipt keeps a receipt photo with the signed-in user's expense,
// replacing any earlier one.
func (h *Handler) AttachReceipt(ctx context.Context, expenseId string, image []byte) error {
	uid, err := userId(ctx)
	if err != nil {
		return err
	}
	mimeType, err := CheckReceiptImage(image)
	if err != nil {
		return err
	}
	err = h.expenseRepo.SaveReceipt(ctx, models.Receipt{ExpenseId: expenseId, UserId: uid, MimeType: mimeType, Data: image, CreatedAt: h.now()})
	switch {
	case errors.Is(err, repositories.ErrExpenseNotFound):
		return pkg.ErrExpenseNotFound
	case err != nil:
		h.logger.ErrorContext(ctx, "save receipt failed", "error", err)
		return pkg.InternalError
	}
	h.logger.InfoContext(ctx, "receipt attached", "expense_id", expenseId, "bytes", len(image))
	return nil
}

// Receipt returns the receipt of the signed-in user's expense.
func (h *Handler) Receipt(ctx context.Context, expenseId string) (models.Receipt, error) {
	uid, err := userId(ctx)
	if err != nil {
		return models.Receipt{}, err
	}
	rc, err := h.expenseRepo.GetReceipt(ctx, uid, expenseId)
	switch {
	case errors.Is(err, repositories.ErrReceiptNotFound):
		return models.Receipt{}, pkg.ErrReceiptNotFound
	case err != nil:
		h.logger.ErrorContext(ctx, "get receipt failed", "error", err)
		return models.Receipt{}, pkg.InternalError
	}
	return rc, nil
}

// RemoveReceipt deletes the receipt of the signed-in user's expense, if any.
func (h *Handler) RemoveReceipt(ctx context.Context, expenseId string) error {
	uid, err := userId(ctx)
	if err != nil {
		return err
	}
	if err := h.expenseRepo.DeleteReceipt(ctx, uid, expenseId); err != nil {
		h.logger.ErrorContext(ctx, "delete receipt failed", "error", err)
		return pkg.InternalError
	}
	return nil
}

// ReceiptIds returns the ids of the signed-in user's expenses with a receipt.
func (h *Handler) ReceiptIds(ctx context.Context) (map[string]bool, error) {
	uid, err := userId(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := h.expenseRepo.ReceiptIds(ctx, uid)
	if err != nil {
		h.logger.ErrorContext(ctx, "list receipts failed", "error", err)
		return nil, pkg.InternalError
	}
	return ids, nil
}

// draftFrom validates what the model read. categories are the user's; the
// draft's category is one of them, or empty when the model didn't pick one
// for sure, so the user chooses it.
func draftFrom(r receipt.Reading, now time.Time, display models.Currency, categories []models.Category, source draftSource) ReceiptDraft {
	d := ReceiptDraft{}
	warn := func(s string) { d.Warnings = append(d.Warnings, s) }

	if amount, ok := parseReceiptAmount(r.Amount); ok {
		d.Amount = amount
	} else if source == sourceText {
		warn("No amount in what you said. Enter it yourself.")
	} else {
		warn("Couldn't read the total. Enter the amount yourself.")
	}

	if c := strings.ToUpper(strings.TrimSpace(r.Currency)); len(c) == 3 && isLetters(c) {
		d.Currency = c
		if c != string(display) {
			if source == sourceText {
				warn("You mentioned " + c + ", but amounts are shown in " + string(display) + " and aren't converted. Check the amount.")
			} else {
				warn("The receipt is in " + c + ", but amounts are shown in " + string(display) + " and aren't converted. Check the amount.")
			}
		}
	}

	if r.Date != "" {
		if date, err := time.Parse(time.DateOnly, r.Date); err != nil {
			warn("Couldn't read the date. It's set to now.")
		} else if date.After(now.AddDate(0, 0, 1)) || date.Before(now.AddDate(-5, 0, 0)) {
			// From the future, or from long ago: a misreading.
			warn("The date (" + r.Date + ") looks wrong, so it wasn't used.")
		} else {
			d.Date = r.Date
			if t, err := time.Parse("15:04", r.Time); err == nil {
				d.Time = t.Format("15:04")
			}
		}
	} else if source == sourceReceipt {
		// A message without a day means now; a receipt always has one.
		warn("Couldn't read the date. It's set to now.")
	}

	if c, problem := NormalizeCategory(r.Category); problem == "" && models.HasCategory(categories, c) {
		d.Category = c
	}
	d.Merchant = cleanText(r.Merchant, models.MaxMerchantLength)
	d.Note = cleanText(r.Note, maxReceiptNote)
	return d
}

// parseReceiptAmount accepts a positive amount like "12.50", "12,50" or
// "1,234.50" and returns it with two decimals.
func parseReceiptAmount(s string) (string, bool) {
	s = strings.TrimSpace(s)
	// Drop thousands separators: a comma followed by three digits and a dot
	// later ("1,234.50"); otherwise a lone comma is a decimal comma.
	if strings.Contains(s, ",") && strings.Contains(s, ".") {
		s = strings.ReplaceAll(s, ",", "")
	} else if strings.Count(s, ",") == 1 {
		s = strings.Replace(s, ",", ".", 1)
	}
	v, err := decimal.NewFromString(s)
	if err != nil || !v.IsPositive() || v.GreaterThan(decimal.NewFromInt(1_000_000_000)) {
		return "", false
	}
	return v.Round(2).StringFixed(2), true
}

// cleanText trims s, drops control characters and cuts it to max runes.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > max {
		s = strings.TrimSpace(string([]rune(s)[:max]))
	}
	return s
}

func isLetters(s string) bool {
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func toStrings[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

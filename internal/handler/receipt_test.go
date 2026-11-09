package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/keivanipchihagh/expense-tracker/internal/receipt"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// pngBytes is enough of a PNG for content sniffing.
var pngBytes = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

type fakeScanner struct {
	reading  receipt.Reading
	err      error
	calls    int
	mimeType string
	text     string
	today    time.Time
	cats     []string
}

func (f *fakeScanner) ReadText(_ context.Context, text string, today time.Time, cats []string) (receipt.Reading, error) {
	f.calls++
	f.text, f.today, f.cats = text, today, cats
	return f.reading, f.err
}

func (f *fakeScanner) Scan(_ context.Context, _ []byte, mimeType string, today time.Time, cats []string) (receipt.Reading, error) {
	f.calls++
	f.mimeType, f.today, f.cats = mimeType, today, cats
	return f.reading, f.err
}

func TestScanReceipt(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
	scanner := &fakeScanner{reading: receipt.Reading{
		IsExpense: true, Amount: "10,91", Currency: "eur", Date: "2026-11-07", Time: "18:42",
		Merchant: " Rewe ", Category: "Groceries", Note: "Oat milk,\nbananas",
	}}
	ts.handler.SetReceiptScanner(scanner)
	assert.True(t, ts.handler.ScanEnabled())

	draft, err := ts.handler.ScanReceipt(userCtx, pngBytes, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, ReceiptDraft{
		Amount: "10.91", Date: "2026-11-07", Time: "18:42", Merchant: "Rewe",
		Category: models.CategoryGroceries, Note: "Oat milk, bananas", Currency: "EUR",
	}, draft)
	assert.Equal(t, "image/png", scanner.mimeType, "the type comes from the bytes")
	assert.Equal(t, testNow, scanner.today)
	assert.Contains(t, scanner.cats, "groceries")
}

func TestScanReceipt_Refusals(t *testing.T) {
	t.Run("signed out", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.handler.SetReceiptScanner(&fakeScanner{})
		_, err := ts.handler.ScanReceipt(context.Background(), pngBytes, time.UTC)
		assert.Same(t, pkg.ErrNotSignedIn, err)
	})
	t.Run("scanning off", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		assert.False(t, ts.handler.ScanEnabled())
		_, err := ts.handler.ScanReceipt(userCtx, pngBytes, time.UTC)
		assert.Same(t, ErrScanDisabled, err)
	})
	for name, tt := range map[string]struct {
		image []byte
		want  string
	}{
		"empty":          {nil, "is required"},
		"too big":        {append(append([]byte{}, pngBytes...), make([]byte, MaxReceiptBytes)...), "must be at most 4 MB"},
		"not an image":   {[]byte("%PDF-1.7 a document"), "must be a JPEG, PNG or WebP image"},
		"html as image":  {[]byte("<html><script>alert(1)</script>"), "must be a JPEG, PNG or WebP image"},
		"gif (no model)": {[]byte("GIF89a......"), "must be a JPEG, PNG or WebP image"},
	} {
		t.Run(name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			scanner := &fakeScanner{}
			ts.handler.SetReceiptScanner(scanner)
			_, err := ts.handler.ScanReceipt(userCtx, tt.image, time.UTC)
			assert.Equal(t, map[string]string{"receipt": tt.want}, validationFields(t, err))
			assert.Zero(t, scanner.calls, "nothing is sent to the model")
		})
	}
}

func TestScanReceipt_RateLimited(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil).Times(maxScans)
	scanner := &fakeScanner{reading: receipt.Reading{IsExpense: true}}
	ts.handler.SetReceiptScanner(scanner)
	for range maxScans {
		_, err := ts.handler.ScanReceipt(userCtx, pngBytes, time.UTC)
		require.NoError(t, err)
	}
	_, err := ts.handler.ScanReceipt(userCtx, pngBytes, time.UTC)
	assert.Same(t, ErrTooManyScans, err)
	assert.Equal(t, maxScans, scanner.calls)

	// Per user: someone else can still scan.
	other := pkg.WithUserId(context.Background(), "user-2")
	ts.userRepo.EXPECT().Get(gomock.Any(), "user-2").Return(models.User{Id: "user-2", Currency: models.CurrencyEUR}, nil)
	_, err = ts.handler.ScanReceipt(other, pngBytes, time.UTC)
	assert.NoError(t, err)
}

func TestScanReceipt_ModelErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		reading    receipt.Reading
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{name: "retryable", err: &receipt.Error{Message: "The free model is busy. Try again in a minute.", Retry: true}, wantStatus: http.StatusServiceUnavailable, wantCode: "scan_failed", wantMsg: "The free model is busy. Try again in a minute."},
		{name: "configuration", err: &receipt.Error{Message: "OpenRouter rejected the API key."}, wantStatus: http.StatusBadGateway, wantCode: "scan_failed", wantMsg: "OpenRouter rejected the API key."},
		{name: "unexpected", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantCode: "internal", wantMsg: "internal server error"},
		{name: "not a receipt", reading: receipt.Reading{IsExpense: false}, wantStatus: http.StatusUnprocessableEntity, wantCode: "not_a_receipt", wantMsg: "That doesn't look like a receipt."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
			ts.handler.SetReceiptScanner(&fakeScanner{reading: tt.reading, err: tt.err})
			_, err := ts.handler.ScanReceipt(userCtx, pngBytes, time.UTC)
			var appErr *pkg.Error
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, tt.wantStatus, appErr.Status)
			assert.Equal(t, tt.wantCode, appErr.Code)
			assert.Contains(t, appErr.Message, tt.wantMsg)
		})
	}
}

func TestDraftFrom(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		reading  receipt.Reading
		check    func(t *testing.T, d ReceiptDraft)
		warnings []string
	}{
		{
			name:    "nothing readable",
			reading: receipt.Reading{IsExpense: true},
			check: func(t *testing.T, d ReceiptDraft) {
				assert.Equal(t, ReceiptDraft{Warnings: d.Warnings}, d, "no category unless the model is sure")
			},
			warnings: []string{"Couldn't read the total. Enter the amount yourself.", "Couldn't read the date. It's set to now."},
		},
		{
			name:    "thousands separator",
			reading: receipt.Reading{Amount: "1,234.5", Date: "2026-11-01"},
			check:   func(t *testing.T, d ReceiptDraft) { assert.Equal(t, "1234.50", d.Amount) },
		},
		{
			name:    "rounds to cents",
			reading: receipt.Reading{Amount: "3.456", Date: "2026-11-01"},
			check:   func(t *testing.T, d ReceiptDraft) { assert.Equal(t, "3.46", d.Amount) },
		},
		{
			name:     "negative, zero and junk amounts are dropped",
			reading:  receipt.Reading{Amount: "-4.00", Date: "2026-11-01"},
			check:    func(t *testing.T, d ReceiptDraft) { assert.Empty(t, d.Amount) },
			warnings: []string{"Couldn't read the total. Enter the amount yourself."},
		},
		{
			name:     "absurd amount",
			reading:  receipt.Reading{Amount: "99999999999", Date: "2026-11-01"},
			check:    func(t *testing.T, d ReceiptDraft) { assert.Empty(t, d.Amount) },
			warnings: []string{"Couldn't read the total. Enter the amount yourself."},
		},
		{
			name:     "other currency",
			reading:  receipt.Reading{Amount: "5.00", Currency: "USD", Date: "2026-11-01"},
			check:    func(t *testing.T, d ReceiptDraft) { assert.Equal(t, "USD", d.Currency) },
			warnings: []string{"The receipt is in USD, but amounts are shown in EUR and aren't converted. Check the amount."},
		},
		{
			name:    "a currency symbol isn't a code",
			reading: receipt.Reading{Amount: "5.00", Currency: "€", Date: "2026-11-01"},
			check:   func(t *testing.T, d ReceiptDraft) { assert.Empty(t, d.Currency) },
		},
		{
			name:     "future date",
			reading:  receipt.Reading{Amount: "5.00", Date: "2027-01-15", Time: "10:00"},
			check:    func(t *testing.T, d ReceiptDraft) { assert.Empty(t, d.Date); assert.Empty(t, d.Time) },
			warnings: []string{"The date (2027-01-15) looks wrong, so it wasn't used."},
		},
		{
			name:     "ancient date",
			reading:  receipt.Reading{Amount: "5.00", Date: "1999-01-15"},
			check:    func(t *testing.T, d ReceiptDraft) { assert.Empty(t, d.Date) },
			warnings: []string{"The date (1999-01-15) looks wrong, so it wasn't used."},
		},
		{
			name:     "date in another format",
			reading:  receipt.Reading{Amount: "5.00", Date: "07.11.2026"},
			check:    func(t *testing.T, d ReceiptDraft) { assert.Empty(t, d.Date) },
			warnings: []string{"Couldn't read the date. It's set to now."},
		},
		{
			name:    "bad time is dropped, date kept",
			reading: receipt.Reading{Amount: "5.00", Date: "2026-11-08", Time: "25:99"},
			check:   func(t *testing.T, d ReceiptDraft) { assert.Equal(t, "2026-11-08", d.Date); assert.Empty(t, d.Time) },
		},
		{
			name:    "unknown category",
			reading: receipt.Reading{Amount: "5.00", Date: "2026-11-08", Category: "pets"},
			check:   func(t *testing.T, d ReceiptDraft) { assert.Empty(t, d.Category, "left for the user") },
		},
		{
			name:    "text is cleaned and cut",
			reading: receipt.Reading{Amount: "5.00", Date: "2026-11-08", Merchant: "  Café\x00 <b>Zur\tPost</b>  ", Note: strings.Repeat("é", 300)},
			check: func(t *testing.T, d ReceiptDraft) {
				assert.Equal(t, "Café <b>Zur Post</b>", d.Merchant, "markup is kept as text; the template escapes it")
				assert.Equal(t, 200, len([]rune(d.Note)))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := draftFrom(tt.reading, now, models.CurrencyEUR, models.DefaultCategories, sourceReceipt)
			tt.check(t, d)
			assert.Equal(t, tt.warnings, d.Warnings)
		})
	}
}

func TestReadText(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
	scanner := &fakeScanner{reading: receipt.Reading{IsExpense: true, Amount: "12.5", Date: "2026-11-07", Merchant: "Rewe", Category: "food"}}
	ts.handler.SetReceiptScanner(scanner)
	berlin := time.FixedZone("", 60*60)

	draft, err := ts.handler.ReadText(userCtx, "  12.5   lunch at\nRewe yesterday ", berlin)
	require.NoError(t, err)
	assert.Equal(t, ReceiptDraft{Amount: "12.50", Date: "2026-11-07", Merchant: "Rewe", Category: models.CategoryFood}, draft, "no date warning: a message without a time is normal")
	assert.Equal(t, "12.5 lunch at Rewe yesterday", scanner.text, "whitespace is tidied")
	assert.Equal(t, testNow.In(berlin), scanner.today, "today is the user's")
}

func TestReadText_Refusals(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		_, err := setupOAuthSuite(t).handler.ReadText(userCtx, "5 coffee", time.UTC)
		assert.Same(t, ErrScanDisabled, err)
	})
	for name, tt := range map[string]struct{ text, want string }{
		"empty":    {"   ", "is required"},
		"too long": {strings.Repeat("a", MaxExpenseText+1), "must be at most 500 characters"},
	} {
		t.Run(name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			scanner := &fakeScanner{}
			ts.handler.SetReceiptScanner(scanner)
			_, err := ts.handler.ReadText(userCtx, tt.text, time.UTC)
			assert.Equal(t, map[string]string{"text": tt.want}, validationFields(t, err))
			assert.Zero(t, scanner.calls)
		})
	}
	t.Run("no expense in it", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		ts.handler.SetReceiptScanner(&fakeScanner{reading: receipt.Reading{IsExpense: false}})
		_, err := ts.handler.ReadText(userCtx, "hello there", time.UTC)
		var appErr *pkg.Error
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, "no_expense", appErr.Code)
		assert.Contains(t, appErr.Message, "Couldn't find an expense in that")
	})
}

func TestDraftFrom_Text(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	d := draftFrom(receipt.Reading{IsExpense: true, Merchant: "Bakery", Currency: "USD"}, now, models.CurrencyEUR, models.DefaultCategories, sourceText)
	assert.Equal(t, []string{
		"No amount in what you said. Enter it yourself.",
		"You mentioned USD, but amounts are shown in EUR and aren't converted. Check the amount.",
	}, d.Warnings, "no date warning for a message")
	assert.Empty(t, d.Date)
	assert.Empty(t, d.Category)
}

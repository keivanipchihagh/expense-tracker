package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	pkgHttp "github.com/keivanipchihagh/expense-tracker/pkg/http"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

const (
	scanPath = "/ui/expenses/scan"
	textPath = "/ui/expenses/text"
)

// RouteLimits are the routes that need more than the server-wide limits:
// a receipt scan uploads a photo and waits on a free model that can be
// slow.
func RouteLimits() []pkgHttp.RouteLimits {
	upload := int64(handler.MaxReceiptBytes + 64<<10)
	return []pkgHttp.RouteLimits{
		{Path: scanPath, Timeout: 90 * time.Second, MaxBodyBytes: upload},
		{Path: textPath, Timeout: 90 * time.Second},
		// Adding an expense can carry the scanned receipt along.
		{Path: "/ui/expenses", MaxBodyBytes: upload},
	}
}

// ScanReceiptSubmit handles POST /ui/expenses/scan: a multipart form with
// the photo as "receipt" plus the new expense form's hidden fields. It
// answers with the new expense form, filled with what was read, for the
// user to check and add. Nothing is saved here.
func (s *Server) ScanReceiptSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.handler.ScanEnabled() {
		http.NotFound(w, r)
		return
	}
	data := formData{
		Values:      formValues{Category: string(models.CategoryFood)},
		Symbol:      s.currencySymbol(r),
		ScanEnabled: s.handler.ScanEnabled(),
	}
	render := func(status int) { s.renderForm(w, r, status, data) }

	if err := r.ParseMultipartForm(handler.MaxReceiptBytes); err != nil {
		data.Range = filterFromExpenseForm(r.PostForm, time.Now())
		data.ScanError = "The photo couldn't be uploaded."
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			data.ScanError = "The photo is too large. Try a smaller one."
		}
		render(http.StatusRequestEntityTooLarge)
		return
	}
	defer r.MultipartForm.RemoveAll()
	data.Range = filterFromExpenseForm(r.PostForm, time.Now())
	data.Values.TzOffset = r.PostForm.Get("tz_offset")

	var image []byte
	if file, _, err := r.FormFile("receipt"); err == nil {
		image, err = io.ReadAll(io.LimitReader(file, handler.MaxReceiptBytes+1))
		file.Close()
		if err != nil {
			data.ScanError = "The photo couldn't be read."
			render(http.StatusBadRequest)
			return
		}
	}

	draft, err := s.handler.ScanReceipt(r.Context(), image, zoneFromOffset(data.Values.TzOffset))
	if err != nil {
		appErr, ok := errors.AsType[*pkg.Error](err)
		switch {
		case !ok:
			data.ScanError = "Something went wrong. Try again."
			render(http.StatusInternalServerError)
		case appErr.Fields["receipt"] != "":
			data.ScanError = "The photo " + appErr.Fields["receipt"] + "."
			render(appErr.Status)
		case appErr.Status == http.StatusUnauthorized:
			s.renderUiError(w, r, err)
		default:
			data.ScanError = appErr.Message
			render(appErr.Status)
		}
		return
	}

	data.Scanned = true
	fillFromDraft(&data, draft, "12:00")
	render(http.StatusOK)
}

// TextSubmit handles POST /ui/expenses/text: "text" (typed or dictated,
// e.g. "12.50 lunch at Rewe yesterday") plus the list's hidden fields. It
// answers with the new expense form, filled with only what the text says,
// for the user to finish and add. Nothing is saved here.
func (s *Server) TextSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.handler.ScanEnabled() {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(r.PostForm.Get("text"))
	data := formData{
		Range:       filterFromExpenseForm(r.PostForm, time.Now()),
		Values:      formValues{TzOffset: r.PostForm.Get("tz_offset")},
		Symbol:      s.currencySymbol(r),
		ScanEnabled: true,
		FromText:    text,
	}
	loc := zoneFromOffset(data.Values.TzOffset)
	draft, err := s.handler.ReadText(r.Context(), text, loc)
	if err != nil {
		appErr, ok := errors.AsType[*pkg.Error](err)
		switch {
		case !ok:
			data.ScanError = "Something went wrong. Try again."
			s.renderForm(w, r, http.StatusInternalServerError, data)
		case appErr.Fields["text"] != "":
			data.ScanError = "The message " + appErr.Fields["text"] + "."
			s.renderForm(w, r, appErr.Status, data)
		case appErr.Status == http.StatusUnauthorized:
			s.renderUiError(w, r, err)
		default:
			data.ScanError = appErr.Message
			s.renderForm(w, r, appErr.Status, data)
		}
		return
	}
	// A day without a time ("yesterday") keeps the time it is now.
	fillFromDraft(&data, draft, time.Now().In(loc).Format("15:04"))
	s.renderForm(w, r, http.StatusOK, data)
}

// fillFromDraft puts what was read into the form. defaultTime is used when
// the draft has a day but no time.
func fillFromDraft(data *formData, draft handler.ReceiptDraft, defaultTime string) {
	data.FromDraft = true
	data.ScanWarnings = draft.Warnings
	data.Values.Amount = draft.Amount
	data.Values.Category = string(draft.Category)
	data.Values.Merchant = draft.Merchant
	data.Values.Note = draft.Note
	if draft.Date != "" {
		t := draft.Time
		if t == "" {
			t = defaultTime
		}
		data.Values.OccurredLocal = draft.Date + "T" + t + ":00"
	}
}

// zoneFromOffset turns the browser's UTC offset in minutes (east of UTC)
// into a time zone; anything unreadable is UTC.
func zoneFromOffset(minutes string) *time.Location {
	m, err := strconv.Atoi(minutes)
	if err != nil || m < -14*60 || m > 14*60 {
		return time.UTC
	}
	return time.FixedZone("", m*60)
}

// ReceiptImage handles GET /ui/expenses/{id}/receipt: the photo kept with
// the expense.
func (s *Server) ReceiptImage(w http.ResponseWriter, r *http.Request) {
	rc, err := s.handler.Receipt(r.Context(), r.PathValue("id"))
	if err != nil {
		if appErr, ok := errors.AsType[*pkg.Error](err); ok && appErr.Status == http.StatusNotFound {
			http.NotFound(w, r)
			return
		}
		s.renderUiError(w, r, err)
		return
	}
	writeReceipt(w, rc)
}

// ExpenseReceipt handles GET /api/v1/expenses/{id}/receipt.
func (s *Server) ExpenseReceipt(w http.ResponseWriter, r *http.Request) {
	rc, err := s.handler.Receipt(r.Context(), r.PathValue("id"))
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeReceipt(w, rc)
}

// writeReceipt sends the image with the type sniffed when it was stored.
// It's private to the user but never changes, so the browser may keep it.
func writeReceipt(w http.ResponseWriter, rc models.Receipt) {
	w.Header().Set("Content-Type", rc.MimeType)
	w.Header().Set("Content-Disposition", `inline; filename="receipt`+receiptExt(rc.MimeType)+`"`)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write(rc.Data)
}

func receiptExt(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	}
	return ".jpg"
}

package api

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	pkgHttp "github.com/keivanipchihagh/expense-tracker/pkg/http"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

const scanPath = "/ui/expenses/scan"

// RouteLimits are the routes that need more than the server-wide limits:
// a receipt scan uploads a photo and waits on a free model that can be
// slow.
func RouteLimits() []pkgHttp.RouteLimits {
	upload := int64(handler.MaxReceiptBytes + 64<<10)
	return []pkgHttp.RouteLimits{
		{Path: scanPath, Timeout: 90 * time.Second, MaxBodyBytes: upload},
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

	draft, err := s.handler.ScanReceipt(r.Context(), image)
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
	data.ScanWarnings = draft.Warnings
	data.Values.Amount = draft.Amount
	data.Values.Category = string(draft.Category)
	data.Values.Merchant = draft.Merchant
	data.Values.Note = draft.Note
	if draft.Date != "" {
		t := draft.Time
		if t == "" {
			t = "12:00"
		}
		data.Values.OccurredLocal = draft.Date + "T" + t + ":00"
	}
	render(http.StatusOK)
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

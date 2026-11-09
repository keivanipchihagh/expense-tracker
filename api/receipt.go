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
	return []pkgHttp.RouteLimits{{Path: scanPath, Timeout: 90 * time.Second, MaxBodyBytes: handler.MaxReceiptBytes + 64<<10}}
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
	render := func(status int) { s.renderPartial(w, status, "form", data) }

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

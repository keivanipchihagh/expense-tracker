package api

import (
	"encoding/csv"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// exportColumns are the CSV header, in column order.
var exportColumns = []string{"id", "occurred_at", "amount", "category", "merchant", "note", "created_at", "updated_at"}

// ExportExpenses handles GET /ui/export?format=csv|json: a download of the
// expenses the list shows for the same from/to/category/merchant.
func (s *Server) ExportExpenses(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	format := q.Get("format")
	if format != "csv" && format != "json" {
		http.Error(w, `format must be "csv" or "json"`, http.StatusBadRequest)
		return
	}
	filter := filterFromQuery(q, time.Now())
	from, _ := time.Parse(time.DateOnly, filter.From)
	to, _ := time.Parse(time.DateOnly, filter.To)
	expenses, err := s.handler.ListExpenses(r.Context(), models.ExpenseFilter{
		DateFrom: from,
		DateTo:   to,
		Category: models.Category(filter.Category),
		Merchant: filter.Merchant,
	})
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}

	name := exportFilename(filter.dateRange, format)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if format == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(expenses); err != nil {
			slog.ErrorContext(r.Context(), "export json failed", "error", err)
		}
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	if err := writeExpensesCsv(w, expenses); err != nil {
		slog.ErrorContext(r.Context(), "export csv failed", "error", err)
	}
}

// writeExpensesCsv writes a header row and one row per expense. Times keep
// their offsets (RFC 3339); missing merchant or note are empty cells.
func writeExpensesCsv(w http.ResponseWriter, expenses []models.Expense) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(exportColumns); err != nil {
		return err
	}
	for _, e := range expenses {
		row := []string{
			e.Id,
			e.OccurredAt.Format(time.RFC3339),
			e.Amount,
			string(e.Category),
			safeCell(deref(e.Merchant)),
			safeCell(deref(e.Note)),
			e.CreatedAt.Format(time.RFC3339),
			e.UpdatedAt.Format(time.RFC3339),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// safeCell stops spreadsheets from running free text as a formula: a cell
// that starts with = + - @ (or a tab or carriage return) gets a leading
// apostrophe, which Excel and Sheets treat as "this is text".
func safeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// exportFilename names the download after its dates:
// expenses_2026-11-01_2026-11-30.csv, or expenses_all.json for all time.
func exportFilename(rng dateRange, format string) string {
	if rng.From == "" && rng.To == "" {
		return "expenses_all." + format
	}
	from, to := rng.From, rng.To
	if from == "" {
		from = "start"
	}
	if to == "" {
		to = "today"
	}
	return "expenses_" + from + "_" + to + "." + format
}

// ExportQuery is the export link's query for this list: the same dates and
// filters, plus the format.
func (f listFilter) ExportQuery(format string) template.URL {
	v, _ := url.ParseQuery(f.query(f.dateRange))
	v.Set("format", format)
	return template.URL(v.Encode())
}

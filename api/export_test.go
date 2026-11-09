package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeCell(t *testing.T) {
	for in, want := range map[string]string{
		"":                "",
		"Rewe":            "Rewe",
		"=HYPERLINK(1)":   "'=HYPERLINK(1)",
		"+1 555":          "'+1 555",
		"-5 discount":     "'-5 discount",
		"@SUM(A1)":        "'@SUM(A1)",
		"\tcmd":           "'\tcmd",
		"\rcmd":           "'\rcmd",
		"split = fair":    "split = fair",
		"Café, 2 coffees": "Café, 2 coffees",
	} {
		assert.Equal(t, want, safeCell(in), "%q", in)
	}
}

func TestExportFilename(t *testing.T) {
	assert.Equal(t, "expenses_2026-11-01_2026-11-30.csv", exportFilename(dateRange{"2026-11-01", "2026-11-30"}, "csv"))
	assert.Equal(t, "expenses_all.json", exportFilename(dateRange{}, "json"))
	assert.Equal(t, "expenses_start_2026-11-30.csv", exportFilename(dateRange{To: "2026-11-30"}, "csv"))
	assert.Equal(t, "expenses_2026-11-01_today.csv", exportFilename(dateRange{From: "2026-11-01"}, "csv"))
}

func TestListFilter_ExportQuery(t *testing.T) {
	f := listFilter{dateRange: dateRange{"2026-11-01", "2026-11-30"}, Category: "groceries", Merchant: "rewe city"}
	assert.Equal(t, "category=groceries&format=csv&from=2026-11-01&merchant=rewe+city&to=2026-11-30", string(f.ExportQuery("csv")))

	all := listFilter{}
	assert.Equal(t, "format=json&from=&to=", string(all.ExportQuery("json")), "all time keeps its empty dates")
}

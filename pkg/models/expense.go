package models

import (
	"fmt"
	"slices"
	"time"
)

// Currency is an ISO 4217 code. Expenses don't have one: amounts are plain
// numbers, and the currency is only how the UI displays them.
type Currency string

const (
	CurrencyEUR Currency = "EUR"
	CurrencyUSD Currency = "USD"
	CurrencyGBP Currency = "GBP"
	CurrencyCHF Currency = "CHF"
	CurrencyCAD Currency = "CAD"
	CurrencyAUD Currency = "AUD"
	CurrencyJPY Currency = "JPY"
	CurrencyCNY Currency = "CNY"
	CurrencyINR Currency = "INR"
	CurrencyTRY Currency = "TRY"
	CurrencySEK Currency = "SEK"
)

// Currencies lists the display currencies, in the order the UI offers them.
var Currencies = []Currency{
	CurrencyEUR, CurrencyUSD, CurrencyGBP, CurrencyCHF, CurrencyCAD, CurrencyAUD,
	CurrencyJPY, CurrencyCNY, CurrencyINR, CurrencyTRY, CurrencySEK,
}

// DefaultCurrency is the display currency until the user picks one.
const DefaultCurrency = CurrencyEUR

// currencySymbols maps each display currency to its symbol.
var currencySymbols = map[Currency]string{
	CurrencyEUR: "€",
	CurrencyUSD: "$",
	CurrencyGBP: "£",
	CurrencyCAD: "CA$",
	CurrencyAUD: "A$",
	CurrencyJPY: "¥",
	CurrencyCNY: "CN¥",
	CurrencyINR: "₹",
	CurrencyTRY: "₺",
	// Letter symbols get a no-break space so amounts read "CHF 12.50"
	// without the symbol wrapping onto its own line.
	CurrencyCHF: "CHF\u00a0",
	CurrencySEK: "kr\u00a0",
}

// Symbol returns the currency's symbol, e.g. "€", or the code itself for a
// currency without one.
func (c Currency) Symbol() string {
	if s, ok := currencySymbols[c]; ok {
		return s
	}
	return string(c)
}

// Category is what kind of spending an expense is. Each user has their own
// list (see CategoryUsage); names are stored lowercase.
type Category string

const (
	CategoryFood          Category = "food" // eating out
	CategoryGroceries     Category = "groceries"
	CategoryTransport     Category = "transport"
	CategoryHousing       Category = "housing"
	CategoryUtilities     Category = "utilities"
	CategoryHealth        Category = "health"
	CategoryEntertainment Category = "entertainment"
	CategoryShopping      Category = "shopping"
	CategoryTravel        Category = "travel"
	CategorySubscriptions Category = "subscriptions"
	CategoryEducation     Category = "education"
	CategoryGifts         Category = "gifts"
	CategoryOther         Category = "other"
)

// DefaultCategories are the categories every account starts with.
var DefaultCategories = []Category{
	CategoryFood, CategoryGroceries, CategoryTransport, CategoryHousing,
	CategoryUtilities, CategoryHealth, CategoryEntertainment, CategoryShopping,
	CategoryTravel, CategorySubscriptions, CategoryEducation, CategoryGifts,
	CategoryOther,
}

// Maximum lengths, in characters, of the free-text fields.
const (
	MaxMerchantLength = 200
	MaxNoteLength     = 2000
)

// Expense is one logged expense. Optional fields are nil when unset.
type Expense struct {
	Id string `json:"id"`
	// UserId is the owner. It's never serialized: an API caller only ever
	// sees their own expenses.
	UserId string `json:"-"`
	// Amount is an exact decimal string with two decimal places, e.g. "12.50".
	Amount string `json:"amount"`
	// OccurredAt keeps the UTC offset it was recorded with.
	OccurredAt time.Time `json:"occurred_at"`
	Category   Category  `json:"category"`
	// Merchant is the store or vendor, e.g. "Rewe".
	Merchant *string `json:"merchant"`
	// Note is free text from the user's own message, set only when they add one.
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (e *Expense) String() string {
	return fmt.Sprintf("Expense{Id: %s, Amount: %s, Category: %s, OccurredAt: %s}",
		e.Id, e.Amount, e.Category, e.OccurredAt.Format(time.RFC3339))
}

// IsValid reports whether c is a display currency.
func (c Currency) IsValid() bool { return slices.Contains(Currencies, c) }

// Limits on a user's categories.
const (
	MaxCategoryLength = 30
	MaxCategories     = 50
)

// CategoryUsage is one of a user's categories and how many of their
// expenses use it.
type CategoryUsage struct {
	Name     Category `json:"name"`
	Expenses int      `json:"expenses"`
}

// HasCategory reports whether c is in list.
func HasCategory(list []Category, c Category) bool { return slices.Contains(list, c) }

// ExpenseFilter selects expenses to list. Zero fields don't filter.
type ExpenseFilter struct {
	// DateFrom and DateTo bound the local date (inclusive); zero is open.
	DateFrom time.Time
	DateTo   time.Time
	// Category matches exactly.
	Category Category
	// Merchant matches any part of the merchant name, ignoring case
	// (ASCII letters only, as SQLite's LIKE does).
	Merchant string
}

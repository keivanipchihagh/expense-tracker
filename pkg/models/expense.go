package models

import (
	"fmt"
	"slices"
	"time"
)

// Currency is an ISO 4217 currency code.
type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
)

// Currencies lists the accepted currencies.
var Currencies = []Currency{CurrencyUSD, CurrencyEUR}

// Category is what kind of spending an expense is.
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

// Categories lists the accepted categories.
var Categories = []Category{
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
	// Amount is an exact decimal string with the currency's decimal places, e.g. "12.50".
	Amount   string   `json:"amount"`
	Currency Currency `json:"currency"`
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
	return fmt.Sprintf("Expense{Id: %s, Amount: %s %s, Category: %s, OccurredAt: %s}",
		e.Id, e.Amount, e.Currency, e.Category, e.OccurredAt.Format(time.RFC3339))
}

// IsValid reports whether c is an accepted currency.
func (c Currency) IsValid() bool { return slices.Contains(Currencies, c) }

// IsValid reports whether c is an accepted category.
func (c Category) IsValid() bool { return slices.Contains(Categories, c) }

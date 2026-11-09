package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpense_JSON(t *testing.T) {
	merchant := "Rewe"
	e := Expense{
		Id:         "e1",
		Amount:     "12.50",
		OccurredAt: time.Date(2026, 11, 7, 13, 20, 0, 0, time.FixedZone("", 3600)),
		Category:   CategoryFood,
		Merchant:   &merchant,
		CreatedAt:  time.Date(2026, 11, 7, 12, 21, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 11, 7, 12, 21, 0, 0, time.UTC),
	}

	b, err := json.Marshal(e)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"id": "e1",
		"amount": "12.50",
		"occurred_at": "2026-11-07T13:20:00+01:00",
		"category": "food",
		"merchant": "Rewe",
		"note": null,
		"created_at": "2026-11-07T12:21:00Z",
		"updated_at": "2026-11-07T12:21:00Z"
	}`, string(b), "occurred_at keeps its offset; unset optional fields are null; there is no currency")
}

func TestCurrency_Symbol(t *testing.T) {
	assert.Equal(t, "$", CurrencyUSD.Symbol())
	assert.Equal(t, "€", CurrencyEUR.Symbol())
	assert.Equal(t, "£", CurrencyGBP.Symbol())
	assert.Equal(t, "CHF", Currency("CHF").Symbol(), "falls back to the code")

	for _, c := range Currencies {
		assert.NotEqual(t, string(c), c.Symbol(), "every accepted currency has a symbol: %s", c)
	}
}

func TestIsValid(t *testing.T) {
	assert.True(t, CurrencyEUR.IsValid())
	assert.True(t, CurrencyGBP.IsValid())
	assert.True(t, DefaultCurrency.IsValid())
	assert.False(t, Currency("CHF").IsValid())
	assert.False(t, Currency("eur").IsValid())
	assert.False(t, Currency("").IsValid())

	assert.True(t, CategoryGroceries.IsValid())
	assert.False(t, Category("rent").IsValid())
}

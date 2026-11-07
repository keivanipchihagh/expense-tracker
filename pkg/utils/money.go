package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"golang.org/x/text/currency"
)

// maxAmount is the exclusive upper bound for an amount.
var maxAmount = decimal.New(1, 15)

var errAmountFormat = errors.New(`must be a decimal number or string, e.g. "12.50"`)

// ParseAmount validates an amount for the given ISO 4217 currency and
// returns it with exactly the currency's number of decimal places, e.g.
// 12.5 EUR → "12.50", 1200 JPY → "1200". v must be a string or json.Number;
// floats are rejected so amounts stay exact.
func ParseAmount(v any, currencyCode string) (string, error) {
	var raw string
	switch a := v.(type) {
	case string:
		raw = a
	case json.Number:
		raw = a.String()
	default:
		return "", errAmountFormat
	}

	d, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return "", errAmountFormat
	}
	if !d.IsPositive() {
		return "", errors.New("must be greater than zero")
	}
	if d.GreaterThanOrEqual(maxAmount) {
		return "", errors.New("is too large")
	}

	scale, err := CurrencyScale(currencyCode)
	if err != nil {
		return "", err
	}
	if !d.Equal(d.Truncate(scale)) {
		return "", fmt.Errorf("must have at most %d decimal places for %s", scale, currencyCode)
	}
	return d.StringFixed(scale), nil
}

// CurrencyScale returns the number of decimal places ISO 4217 defines for
// the currency, e.g. 2 for EUR and 0 for JPY.
func CurrencyScale(currencyCode string) (int32, error) {
	unit, err := currency.ParseISO(currencyCode)
	if err != nil {
		return 0, fmt.Errorf("unknown currency %q", currencyCode)
	}
	scale, _ := currency.Standard.Rounding(unit)
	return int32(scale), nil
}

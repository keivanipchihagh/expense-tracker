package utils

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/shopspring/decimal"
)

// AmountDecimals is the number of decimal places amounts are stored with.
const AmountDecimals = 2

// maxAmount is the exclusive upper bound for an amount.
var maxAmount = decimal.New(1, 15)

var errAmountFormat = errors.New(`must be a decimal number or string, e.g. "12.50"`)

// ParseAmount validates an amount and returns it with exactly two decimal
// places, e.g. 12.5 → "12.50". v must be a string or json.Number; floats are
// rejected so amounts stay exact.
func ParseAmount(v any) (string, error) {
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
	if !d.Equal(d.Truncate(AmountDecimals)) {
		return "", errors.New("must have at most 2 decimal places")
	}
	return d.StringFixed(AmountDecimals), nil
}

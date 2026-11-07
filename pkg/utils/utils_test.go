package utils

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOffsetTime(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOffset int // seconds east of UTC
		wantErr    bool
	}{
		{name: "positive offset", input: "2026-11-07T13:20:00+01:00", wantOffset: 3600},
		{name: "negative offset", input: "2026-11-07T13:20:00-05:00", wantOffset: -5 * 3600},
		{name: "utc", input: "2026-11-07T12:20:00Z", wantOffset: 0},
		{name: "fractional seconds", input: "2026-11-07T13:20:00.123+01:00", wantOffset: 3600},
		{name: "no offset", input: "2026-11-07T13:20:00", wantErr: true},
		{name: "date only", input: "2026-11-07", wantErr: true},
		{name: "space separator", input: "2026-11-07 13:20:00+01:00", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOffsetTime(tt.input)
			if tt.wantErr {
				assert.ErrorIs(t, err, errOffsetTime)
				return
			}
			require.NoError(t, err)
			_, offset := got.Zone()
			assert.Equal(t, tt.wantOffset, offset)
		})
	}

	t.Run("keeps local wall clock", func(t *testing.T) {
		got, err := ParseOffsetTime("2026-11-07T23:30:00-05:00")
		require.NoError(t, err)
		assert.Equal(t, "2026-11-07T23:30:00-05:00", got.Format(time.RFC3339))
	})
}

func TestParseAmount(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		currency string
		want     string
		wantErr  string
	}{
		{name: "string with cents", input: "12.50", currency: "EUR", want: "12.50"},
		{name: "json number", input: json.Number("12.5"), currency: "EUR", want: "12.50"},
		{name: "integer pads decimals", input: "12", currency: "USD", want: "12.00"},
		{name: "trailing zeros beyond scale are fine", input: "12.500", currency: "EUR", want: "12.50"},
		{name: "zero-decimal currency", input: "1200", currency: "JPY", want: "1200"},
		{name: "three-decimal currency", input: "1.234", currency: "KWD", want: "1.234"},
		{name: "surrounding spaces", input: " 3.10 ", currency: "EUR", want: "3.10"},
		{name: "too many decimals", input: "12.345", currency: "EUR", wantErr: "must have at most 2 decimal places for EUR"},
		{name: "decimals on zero-decimal currency", input: "12.5", currency: "JPY", wantErr: "must have at most 0 decimal places for JPY"},
		{name: "zero", input: "0", currency: "EUR", wantErr: "must be greater than zero"},
		{name: "negative", input: "-5", currency: "EUR", wantErr: "must be greater than zero"},
		{name: "too large", input: "1000000000000000", currency: "EUR", wantErr: "is too large"},
		{name: "not a number", input: "twelve", currency: "EUR", wantErr: errAmountFormat.Error()},
		{name: "float is rejected", input: 12.5, currency: "EUR", wantErr: errAmountFormat.Error()},
		{name: "bool is rejected", input: true, currency: "EUR", wantErr: errAmountFormat.Error()},
		{name: "unknown currency", input: "1", currency: "XYZ", wantErr: `unknown currency "XYZ"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAmount(tt.input, tt.currency)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCurrencyScale(t *testing.T) {
	for code, want := range map[string]int32{"USD": 2, "EUR": 2, "JPY": 0, "KWD": 3} {
		got, err := CurrencyScale(code)
		require.NoError(t, err, code)
		assert.Equal(t, want, got, code)
	}
}

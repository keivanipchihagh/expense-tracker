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
		name    string
		input   any
		want    string
		wantErr string
	}{
		{name: "string with cents", input: "12.50", want: "12.50"},
		{name: "json number", input: json.Number("12.5"), want: "12.50"},
		{name: "integer pads decimals", input: "12", want: "12.00"},
		{name: "trailing zeros beyond two decimals are fine", input: "12.500", want: "12.50"},
		{name: "smallest amount", input: "0.01", want: "0.01"},
		{name: "largest amount", input: "999999999999999.99", want: "999999999999999.99"},
		{name: "surrounding spaces", input: " 3.10 ", want: "3.10"},
		{name: "too many decimals", input: "12.345", wantErr: "must have at most 2 decimal places"},
		{name: "zero", input: "0", wantErr: "must be greater than zero"},
		{name: "negative", input: "-5", wantErr: "must be greater than zero"},
		{name: "too large", input: "1000000000000000", wantErr: "is too large"},
		{name: "not a number", input: "twelve", wantErr: errAmountFormat.Error()},
		{name: "empty", input: "", wantErr: errAmountFormat.Error()},
		{name: "float is rejected", input: 12.5, wantErr: errAmountFormat.Error()},
		{name: "bool is rejected", input: true, wantErr: errAmountFormat.Error()},
		{name: "nil is rejected", input: nil, wantErr: errAmountFormat.Error()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAmount(tt.input)
			if tt.wantErr != "" {
				assert.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

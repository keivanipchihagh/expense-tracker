package handler

import (
	"context"
	"testing"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestDisplayCurrency(t *testing.T) {
	tests := []struct {
		name   string
		stored string
		err    error
		want   models.Currency
	}{
		{name: "saved choice", stored: "USD", want: models.CurrencyUSD},
		{name: "never saved uses the default", err: repositories.ErrSettingNotFound, want: models.DefaultCurrency},
		{name: "no longer offered uses the default", stored: "JPY", want: models.DefaultCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupHandlerTestSuite(t)
			ts.settingRepo.EXPECT().Get(gomock.Any(), "currency").Return(tt.stored, tt.err)

			got, err := ts.handler.DisplayCurrency(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("database error", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		ts.settingRepo.EXPECT().Get(gomock.Any(), "currency").Return("", errDatabase)

		_, err := ts.handler.DisplayCurrency(context.Background())
		assert.Same(t, pkg.InternalError, err)
	})
}

func TestSetDisplayCurrency(t *testing.T) {
	t.Run("saves an offered currency", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		ts.settingRepo.EXPECT().Set(gomock.Any(), "currency", "GBP").Return(nil)

		require.NoError(t, ts.handler.SetDisplayCurrency(context.Background(), models.CurrencyGBP))
	})

	for _, c := range []models.Currency{"", "eur", "JPY"} {
		t.Run("rejects "+string(c), func(t *testing.T) {
			ts := setupHandlerTestSuite(t)

			err := ts.handler.SetDisplayCurrency(context.Background(), c)
			assert.Equal(t, map[string]string{"currency": "must be one of EUR, USD, GBP"}, validationFields(t, err))
		})
	}

	t.Run("database error", func(t *testing.T) {
		ts := setupHandlerTestSuite(t)
		ts.settingRepo.EXPECT().Set(gomock.Any(), "currency", "EUR").Return(errDatabase)

		assert.Same(t, pkg.InternalError, ts.handler.SetDisplayCurrency(context.Background(), models.CurrencyEUR))
	})
}

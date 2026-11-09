package handler

import (
	"context"
	"errors"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

// settingCurrency is the key of the display currency setting.
const settingCurrency = "currency"

// DisplayCurrency returns the currency the UI shows amounts in: the saved
// choice, or models.DefaultCurrency if there is none (or it's no longer
// offered).
func (h *Handler) DisplayCurrency(ctx context.Context) (models.Currency, error) {
	value, err := h.settingRepo.Get(ctx, settingCurrency)
	if errors.Is(err, repositories.ErrSettingNotFound) {
		return models.DefaultCurrency, nil
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "get display currency failed", "error", err)
		return "", pkg.InternalError
	}
	if c := models.Currency(value); c.IsValid() {
		return c, nil
	}
	return models.DefaultCurrency, nil
}

// SetDisplayCurrency saves the currency the UI shows amounts in. It only
// changes how amounts are displayed; nothing is converted.
func (h *Handler) SetDisplayCurrency(ctx context.Context, c models.Currency) error {
	if !c.IsValid() {
		return pkg.NewValidationError(map[string]string{"currency": "must be one of " + joinValues(models.Currencies)})
	}
	if err := h.settingRepo.Set(ctx, settingCurrency, string(c)); err != nil {
		h.logger.ErrorContext(ctx, "set display currency failed", "error", err)
		return pkg.InternalError
	}
	h.logger.InfoContext(ctx, "display currency changed", "currency", string(c))
	return nil
}

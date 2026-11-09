package internal

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/keivanipchihagh/expense-tracker/internal/config"
	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

// adoptLegacyData gives expenses and logs from before accounts existed an
// owner, once: the account with OWNER_EMAIL and AUTH_PASSWORD (created if
// needed). The old display currency moves onto that account and the
// settings table is dropped. It does nothing when
// every expense already has an owner.
func adoptLegacyData(ctx context.Context, db *sqlite.Client, h *handler.Handler, cfg config.Config) error {
	unowned, err := repositories.CountUnowned(ctx, db)
	if err != nil {
		return err
	}
	currency, hasCurrency, err := repositories.LegacyCurrency(ctx, db)
	if err != nil {
		return err
	}
	if unowned == 0 {
		return repositories.DropLegacySettings(ctx, db)
	}
	if cfg.OwnerEmail == "" || cfg.AuthPassword == "" {
		return fmt.Errorf("%d expenses from before accounts have no owner: set OWNER_EMAIL and AUTH_PASSWORD (the owner's password) once and restart", unowned)
	}

	owner, err := h.EnsureOwner(ctx, handler.SignUpInput{Email: cfg.OwnerEmail, Password: cfg.AuthPassword})
	if err != nil {
		return fmt.Errorf("create owner account: %w", err)
	}
	if hasCurrency && models.Currency(currency).IsValid() {
		if err := h.SetDisplayCurrency(pkg.WithUserId(ctx, owner.Id), models.Currency(currency)); err != nil {
			return fmt.Errorf("move display currency: %w", err)
		}
	}
	if err := repositories.AssignUnowned(ctx, db, owner.Id); err != nil {
		return err
	}
	if err := repositories.DropLegacySettings(ctx, db); err != nil {
		return err
	}
	slog.InfoContext(ctx, "existing data assigned to owner", "expenses", unowned, "user_id", owner.Id)
	return nil
}

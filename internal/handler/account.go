package handler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"golang.org/x/crypto/bcrypt"
)

// DataExport is everything stored about a user, for "export my data". The
// password hash and the hashes of session and OAuth secrets are left out.
type DataExport struct {
	ExportedAt    time.Time        `json:"exported_at"`
	Account       models.User      `json:"account"`
	Expenses      []models.Expense `json:"expenses"`
	History       []HistoryEntry   `json:"history"`
	ConnectedApps []ExportedApp    `json:"connected_apps"`
}

// HistoryEntry is one change to an expense: before is null for a create
// and after is null for a delete.
type HistoryEntry struct {
	ExpenseId string          `json:"expense_id"`
	Action    string          `json:"action"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	At        time.Time       `json:"at"`
}

// ExportedApp is an app with access to the account.
type ExportedApp struct {
	Name         string    `json:"name"`
	ConnectedAt  time.Time `json:"connected_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// ExportData returns everything stored about the signed-in user.
func (h *Handler) ExportData(ctx context.Context) (DataExport, error) {
	user, err := h.CurrentUser(ctx)
	if err != nil {
		return DataExport{}, err
	}
	now := h.now()
	expenses, err := h.expenseRepo.List(ctx, user.Id, models.ExpenseFilter{})
	if err != nil {
		h.logger.ErrorContext(ctx, "export expenses failed", "error", err)
		return DataExport{}, pkg.InternalError
	}
	logs, err := h.expenseRepo.History(ctx, user.Id)
	if err != nil {
		h.logger.ErrorContext(ctx, "export history failed", "error", err)
		return DataExport{}, pkg.InternalError
	}
	apps, err := h.oauthRepo.ListConnectedApps(ctx, user.Id, now)
	if err != nil {
		h.logger.ErrorContext(ctx, "export connected apps failed", "error", err)
		return DataExport{}, pkg.InternalError
	}

	out := DataExport{
		ExportedAt:    now.UTC(),
		Account:       user,
		Expenses:      expenses,
		History:       make([]HistoryEntry, len(logs)),
		ConnectedApps: make([]ExportedApp, len(apps)),
	}
	for i, l := range logs {
		out.History[i] = HistoryEntry{ExpenseId: l.ExpenseId, Action: l.Action.String(), Before: l.Before, After: l.After, At: l.CreatedAt}
	}
	for i, a := range apps {
		out.ConnectedApps[i] = ExportedApp{Name: a.Client.Name, ConnectedAt: a.Client.CreatedAt, LastActiveAt: a.LastActiveAt}
	}
	h.logger.InfoContext(ctx, "data exported", "user_id", user.Id)
	return out, nil
}

// DeleteAccount permanently deletes the signed-in user and everything that
// is theirs, after checking their password. Their sessions and app tokens
// stop working at once.
func (h *Handler) DeleteAccount(ctx context.Context, password string) error {
	user, err := h.CurrentUser(ctx)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return pkg.NewValidationError(map[string]string{"password": "is wrong"})
	}
	if err := h.userRepo.Delete(ctx, user.Id); err != nil {
		return h.userRepoError(ctx, "delete account failed", err)
	}
	h.logger.InfoContext(ctx, "account deleted", "user_id", user.Id)
	return nil
}

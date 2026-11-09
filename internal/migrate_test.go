package internal

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/keivanipchihagh/expense-tracker/internal/config"
	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

const ownerPassword = "the-owners-old-auth-password"

// legacyDatabase builds a database the way it looked before accounts:
// expenses and logs without owners, a session, and a settings table.
func legacyDatabase(t *testing.T) *sqlite.Client {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.NewClient(ctx, filepath.Join(t.TempDir(), "expense.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE expenses (id TEXT PRIMARY KEY, amount TEXT NOT NULL, occurred_at DATETIME NOT NULL, category TEXT NOT NULL,
			merchant TEXT, note TEXT, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`INSERT INTO expenses VALUES
			('e1', '12.50', '2026-11-07T13:20:00+01:00', 'food', 'Rewe', NULL, '2026-11-07T12:21:00Z', '2026-11-07T12:21:00Z'),
			('e2', '1250.00', '2026-11-01T09:00:00+01:00', 'housing', 'Landlord', NULL, '2026-11-01T08:00:00Z', '2026-11-01T08:00:00Z')`,
		`CREATE TABLE logs (id INTEGER PRIMARY KEY AUTOINCREMENT, expense_id TEXT NOT NULL, action TEXT NOT NULL,
			"before" TEXT, "after" TEXT, created_at DATETIME NOT NULL)`,
		`INSERT INTO logs (expense_id, action, "after", created_at) VALUES ('e1', 'create', '{}', '2026-11-07T12:21:00Z')`,
		`CREATE TABLE sessions (token_hash TEXT PRIMARY KEY, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL)`,
		`INSERT INTO sessions VALUES ('old', 9999999999, 1)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO settings VALUES ('currency', 'GBP')`,
	} {
		_, err := db.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}
	return db
}

type stack struct {
	db       *sqlite.Client
	h        *handler.Handler
	expenses repositories.ExpenseRepository
	users    repositories.UserRepository
}

// open runs the startup steps: repositories (with their migrations) and the handler.
func open(t *testing.T, db *sqlite.Client) stack {
	t.Helper()
	ctx := context.Background()
	logs, err := repositories.NewLogRepository(ctx, db)
	require.NoError(t, err)
	expenses, err := repositories.NewExpenseRepository(ctx, db, logs)
	require.NoError(t, err)
	oauth, err := repositories.NewOAuthRepository(ctx, db)
	require.NoError(t, err)
	sessions, err := repositories.NewSessionRepository(ctx, db)
	require.NoError(t, err)
	users, err := repositories.NewUserRepository(ctx, db)
	require.NoError(t, err)
	categories, err := repositories.NewCategoryRepository(ctx, db)
	require.NoError(t, err)
	h := handler.NewHandler(expenses, oauth, sessions, users, categories, "http://localhost:8000", false)
	h.SetBcryptCost(bcrypt.MinCost)
	return stack{db: db, h: h, expenses: expenses, users: users}
}

func ownerConfig() config.Config {
	return config.Config{OwnerEmail: "Keivan@Example.com", AuthPassword: ownerPassword}
}

func TestAdoptLegacyData(t *testing.T) {
	ctx := context.Background()
	s := open(t, legacyDatabase(t))

	require.NoError(t, adoptLegacyData(ctx, s.db, s.h, ownerConfig()))

	owner, err := s.users.GetByEmail(ctx, "keivan@example.com")
	require.NoError(t, err, "the owner account exists, even with sign-up off")
	assert.Equal(t, "keivan@example.com", owner.Email, "the email is lowercased")
	assert.Equal(t, models.CurrencyGBP, owner.Currency, "the old display currency moved onto the owner")
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(owner.PasswordHash), []byte(ownerPassword)), "the old password signs in")

	list, err := s.expenses.List(ctx, owner.Id, models.ExpenseFilter{})
	require.NoError(t, err)
	require.Len(t, list, 2, "every expense is the owner's")
	assert.Equal(t, "1250.00", list[0].Amount)

	var unownedLogs, settingsTables int
	require.NoError(t, s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM logs WHERE user_id IS NULL").Scan(&unownedLogs))
	assert.Zero(t, unownedLogs, "so is their history")
	require.NoError(t, s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name = 'settings'").Scan(&settingsTables))
	assert.Zero(t, settingsTables, "the settings table is gone")

	// The owner can sign in with email and the old password.
	user, err := s.h.Authenticate(ctx, "keivan@example.com", ownerPassword)
	require.NoError(t, err)
	assert.Equal(t, owner.Id, user.Id)

	// Restarting does nothing more, even without the variables.
	s2 := open(t, s.db)
	require.NoError(t, adoptLegacyData(ctx, s2.db, s2.h, config.Config{}))
	list, err = s2.expenses.List(ctx, owner.Id, models.ExpenseFilter{})
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestAdoptLegacyData_NeedsTheOwnerSettings(t *testing.T) {
	s := open(t, legacyDatabase(t))
	for _, cfg := range []config.Config{
		{},
		{OwnerEmail: "keivan@example.com"},
		{AuthPassword: ownerPassword},
	} {
		err := adoptLegacyData(context.Background(), s.db, s.h, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "2 expenses from before accounts have no owner")
		assert.Contains(t, err.Error(), "OWNER_EMAIL and AUTH_PASSWORD")
	}
	unowned, err := repositories.CountUnowned(context.Background(), s.db)
	require.NoError(t, err)
	assert.Equal(t, 2, unowned, "nothing was assigned")
}

func TestAdoptLegacyData_ReusesAnExistingAccount(t *testing.T) {
	ctx := context.Background()
	s := open(t, legacyDatabase(t))
	existing, err := s.h.EnsureOwner(ctx, handler.SignUpInput{Email: "keivan@example.com", Password: "a-different-password"})
	require.NoError(t, err)

	require.NoError(t, adoptLegacyData(ctx, s.db, s.h, ownerConfig()))

	list, err := s.expenses.List(ctx, existing.Id, models.ExpenseFilter{})
	require.NoError(t, err)
	assert.Len(t, list, 2, "the data goes to the account with that email")
	_, err = s.h.Authenticate(ctx, "keivan@example.com", "a-different-password")
	assert.NoError(t, err, "its password isn't replaced")
}

func TestAdoptLegacyData_RejectsAnInvalidOwner(t *testing.T) {
	s := open(t, legacyDatabase(t))
	cfg := ownerConfig()
	cfg.OwnerEmail = "not-an-email"
	assert.Error(t, adoptLegacyData(context.Background(), s.db, s.h, cfg))
}

func TestAdoptLegacyData_FreshDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.NewClient(ctx, filepath.Join(t.TempDir(), "expense.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	s := open(t, db)

	require.NoError(t, adoptLegacyData(ctx, s.db, s.h, config.Config{}), "nothing to adopt, nothing required")
	_, err = s.h.CurrentUser(pkg.WithUserId(ctx, "anyone"))
	assert.Error(t, err, "no accounts were created")
}

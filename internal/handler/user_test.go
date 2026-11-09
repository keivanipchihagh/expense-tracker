package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"
)

func validSignUp() SignUpInput {
	return SignUpInput{Email: " Alice@Example.COM ", Password: testPassword, FirstName: " Alice ", LastName: "Liddell"}
}

func TestSignUp_Success(t *testing.T) {
	ts := setupOAuthSuite(t)
	var stored models.User
	ts.userRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u models.User) error { stored = u; return nil })

	user, err := ts.handler.SignUp(context.Background(), validSignUp())
	require.NoError(t, err)
	assert.Equal(t, stored, user)
	assert.NotEmpty(t, user.Id)
	assert.Equal(t, "alice@example.com", user.Email, "emails are trimmed and lowercase")
	assert.Equal(t, "Alice", user.FirstName, "names are trimmed but keep their case")
	assert.Equal(t, "Liddell", user.LastName)
	assert.Equal(t, models.DefaultCurrency, user.Currency)
	assert.Equal(t, testNow, user.CreatedAt)
	assert.NotContains(t, user.PasswordHash, testPassword, "the password itself is never stored")
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(testPassword)), "a bcrypt hash of it is")
}

func TestSignUp_Validation(t *testing.T) {
	tests := []struct {
		name   string
		modify func(in *SignUpInput)
		want   map[string]string
	}{
		{name: "everything missing", modify: func(in *SignUpInput) { *in = SignUpInput{} }, want: map[string]string{
			"email": "is required", "password": "must be at least 10 characters",
			"first_name": "is required", "last_name": "is required",
		}},
		{name: "blank names", modify: func(in *SignUpInput) { in.FirstName, in.LastName = "  ", "" }, want: map[string]string{"first_name": "is required", "last_name": "is required"}},
		{name: "name too long", modify: func(in *SignUpInput) { in.LastName = strings.Repeat("é", models.MaxNameLength+1) }, want: map[string]string{"last_name": "must be at most 50 characters"}},
		{name: "name with control characters", modify: func(in *SignUpInput) { in.FirstName = "Al\x00ice" }, want: map[string]string{"first_name": "must not contain control characters"}},
		{name: "not an email", modify: func(in *SignUpInput) { in.Email = "alice" }, want: map[string]string{"email": "must be an email address, e.g. you@example.com"}},
		{name: "email with a display name", modify: func(in *SignUpInput) { in.Email = "Alice <alice@example.com>" }, want: map[string]string{"email": "must be an email address, e.g. you@example.com"}},
		{name: "email without a domain dot", modify: func(in *SignUpInput) { in.Email = "alice@localhost" }, want: map[string]string{"email": "must be an email address, e.g. you@example.com"}},
		{name: "email too long", modify: func(in *SignUpInput) { in.Email = strings.Repeat("a", 250) + "@example.com" }, want: map[string]string{"email": "must be at most 254 characters"}},
		{name: "password too short", modify: func(in *SignUpInput) { in.Password = "short" }, want: map[string]string{"password": "must be at least 10 characters"}},
		{name: "password over bcrypt's limit", modify: func(in *SignUpInput) { in.Password = strings.Repeat("x", 73) }, want: map[string]string{"password": "must be at most 72 bytes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := setupOAuthSuite(t)
			in := validSignUp()
			tt.modify(&in)
			_, err := ts.handler.SignUp(context.Background(), in)
			assert.Equal(t, tt.want, validationFields(t, err))
		})
	}
}

func TestSignUp_Taken(t *testing.T) {
	for repoErr, field := range map[error]string{repositories.ErrEmailTaken: "email"} {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(repoErr)
		_, err := ts.handler.SignUp(context.Background(), validSignUp())
		assert.Equal(t, map[string]string{field: "is taken"}, validationFields(t, err))
	}
}

func TestSignUp_Disabled(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.handler.signupEnabled = false
	_, err := ts.handler.SignUp(context.Background(), validSignUp())
	assert.Same(t, pkg.ErrSignupDisabled, err)
	assert.False(t, ts.handler.SignupEnabled())
}

func TestSignUp_RateLimited(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.userRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil).Times(maxSignups)
	for i := range maxSignups {
		in := validSignUp()
		in.Email = fmt.Sprintf("user%d@example.com", i)
		_, err := ts.handler.SignUp(context.Background(), in)
		require.NoError(t, err)
	}
	_, err := ts.handler.SignUp(context.Background(), validSignUp())
	assert.Same(t, pkg.ErrTooManySignups, err)
}

func TestEnsureOwner(t *testing.T) {
	t.Run("finds the existing account", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.knowsUser()
		user, err := ts.handler.EnsureOwner(context.Background(), SignUpInput{Email: "ALICE@example.com", Password: "ignored"})
		require.NoError(t, err)
		assert.Equal(t, testUserId, user.Id, "existing accounts are reused, not overwritten")
	})
	t.Run("creates it even with sign-up off", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.handler.signupEnabled = false
		ts.userRepo.EXPECT().GetByEmail(gomock.Any(), "owner@example.com").Return(models.User{}, repositories.ErrUserNotFound)
		ts.userRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
		user, err := ts.handler.EnsureOwner(context.Background(), SignUpInput{Email: "owner@example.com", Password: testPassword})
		require.NoError(t, err)
		assert.Equal(t, "owner@example.com", user.Email)
	})
	t.Run("validates like sign-up", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).Return(models.User{}, repositories.ErrUserNotFound)
		_, err := ts.handler.EnsureOwner(context.Background(), SignUpInput{Email: "owner@example.com", Password: "short"})
		assert.Contains(t, validationFields(t, err), "password")
	})
}

func TestCurrentUser(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
	user, err := ts.handler.CurrentUser(userCtx)
	require.NoError(t, err)
	assert.Equal(t, testEmail, user.Email)

	_, err = ts.handler.CurrentUser(context.Background())
	assert.Same(t, pkg.ErrNotSignedIn, err)

	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(models.User{}, repositories.ErrUserNotFound)
	_, err = ts.handler.CurrentUser(userCtx)
	assert.Same(t, pkg.ErrNotSignedIn, err, "a deleted account is signed out")
}

func TestUpdateUser(t *testing.T) {
	t.Run("changes the fields sent", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		var stored models.User
		ts.userRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u models.User) error { stored = u; return nil })

		user, err := ts.handler.UpdateUser(userCtx, map[string]any{"first_name": " Alice ", "last_name": "Kingsleigh", "currency": "GBP"})
		require.NoError(t, err)
		assert.Equal(t, stored, user)
		assert.Equal(t, "Alice", user.FirstName)
		assert.Equal(t, "Kingsleigh", user.LastName)
		assert.Equal(t, models.CurrencyGBP, user.Currency)
		assert.Equal(t, testEmail, user.Email, "untouched")
		assert.Equal(t, string(testHash), user.PasswordHash, "the password is untouched")
		assert.Equal(t, testNow, user.UpdatedAt)
	})
	t.Run("no change writes nothing", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		_, err := ts.handler.UpdateUser(userCtx, map[string]any{"currency": "EUR"})
		assert.NoError(t, err)
	})
	t.Run("the email can't be changed", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		_, err := ts.handler.UpdateUser(userCtx, map[string]any{"username": "mallory", "email": "mallory@example.com"})
		assert.Equal(t, map[string]string{"username": "unknown field", "email": "cannot be changed"}, validationFields(t, err))
	})
	t.Run("rejects bad values and other fields", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		_, err := ts.handler.UpdateUser(userCtx, map[string]any{
			"first_name": " ", "last_name": 5, "currency": "XYZ", "password_hash": "x", "id": "user-2",
		})
		assert.Equal(t, map[string]string{
			"first_name":    "is required",
			"last_name":     "must be a string",
			"currency":      "must be one of EUR, USD, GBP, CHF, CAD, AUD, JPY, CNY, INR, TRY, SEK",
			"password_hash": "unknown field",
			"id":            "unknown field",
		}, validationFields(t, err), "names can't be cleared; the id and password can't be set this way")
	})
	t.Run("needs a user", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		_, err := ts.handler.UpdateUser(context.Background(), map[string]any{"first_name": "x"})
		assert.Same(t, pkg.ErrNotSignedIn, err)
	})
}

func TestEnsureOwner_NeedsNoName(t *testing.T) {
	ts := setupOAuthSuite(t)
	ts.userRepo.EXPECT().GetByEmail(gomock.Any(), gomock.Any()).Return(models.User{}, repositories.ErrUserNotFound)
	ts.userRepo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	user, err := ts.handler.EnsureOwner(context.Background(), SignUpInput{Email: "owner@example.com", Password: testPassword})
	require.NoError(t, err, "the migration's owner has no name until they set one")
	assert.Empty(t, user.FirstName)
}

func TestUserNames(t *testing.T) {
	both := models.User{Email: "kip@example.com", FirstName: "keivan", LastName: "Ipchihagh"}
	assert.Equal(t, "keivan Ipchihagh", both.DisplayName())
	assert.Equal(t, "KI", both.Initials())
	none := models.User{Email: "kip@example.com"}
	assert.Equal(t, "kip@example.com", none.DisplayName(), "falls back to the email")
	assert.Equal(t, "K", none.Initials())
	unicode := models.User{Email: "x@example.com", FirstName: "émile", LastName: "Ölund"}
	assert.Equal(t, "ÉÖ", unicode.Initials())
}

func TestChangePassword(t *testing.T) {
	const next = "a-whole-new-password"
	t.Run("success signs out other sessions", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		var stored models.User
		ts.userRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u models.User) error { stored = u; return nil })
		ts.sessionRepo.EXPECT().DeleteOthers(gomock.Any(), testUserId, hashSecret("this-session")).Return(nil)

		require.NoError(t, ts.handler.ChangePassword(userCtx, testPassword, next, "this-session"))
		assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(next)))
		assert.Error(t, bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte(testPassword)), "the old one stops working")
	})
	t.Run("wrong current password and weak new one", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		err := ts.handler.ChangePassword(userCtx, "not-it", "short", "")
		assert.Equal(t, map[string]string{"current_password": "is wrong", "new_password": "must be at least 10 characters"}, validationFields(t, err))
	})
	t.Run("a failed sign-out doesn't undo the change", func(t *testing.T) {
		ts := setupOAuthSuite(t)
		ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(testUser(), nil)
		ts.userRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		ts.sessionRepo.EXPECT().DeleteOthers(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("db down"))
		assert.NoError(t, ts.handler.ChangePassword(userCtx, testPassword, next, ""))
	})
}

func TestDisplayCurrency(t *testing.T) {
	ts := setupOAuthSuite(t)
	gbp := testUser()
	gbp.Currency = models.CurrencyGBP
	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(gbp, nil)
	c, err := ts.handler.DisplayCurrency(userCtx)
	require.NoError(t, err)
	assert.Equal(t, models.CurrencyGBP, c)

	odd := testUser()
	odd.Currency = "XYZ"
	ts.userRepo.EXPECT().Get(gomock.Any(), testUserId).Return(odd, nil)
	c, err = ts.handler.DisplayCurrency(userCtx)
	require.NoError(t, err)
	assert.Equal(t, models.DefaultCurrency, c, "a currency no longer offered falls back")
}

func TestExpenses_NeedASignedInUser(t *testing.T) {
	ts := setupOAuthSuite(t)
	ctx := context.Background()
	_, err := ts.handler.CreateExpense(ctx, map[string]any{})
	assert.Same(t, pkg.ErrNotSignedIn, err)
	_, err = ts.handler.ListExpenses(ctx, models.ExpenseFilter{})
	assert.Same(t, pkg.ErrNotSignedIn, err)
	_, err = ts.handler.GetExpense(ctx, "id")
	assert.Same(t, pkg.ErrNotSignedIn, err)
	_, err = ts.handler.UpdateExpense(ctx, "id", map[string]any{})
	assert.Same(t, pkg.ErrNotSignedIn, err)
	assert.Same(t, pkg.ErrNotSignedIn, ts.handler.DeleteExpense(ctx, "id"))
}

func TestCreateExpense_OwnedBySignedInUser(t *testing.T) {
	ts := setupOAuthSuite(t)
	var stored models.Expense
	ts.expenseRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, e models.Expense) error { stored = e; return nil })
	_, err := ts.handler.CreateExpense(userCtx, map[string]any{"amount": "1", "occurred_at": "2026-11-07T13:20:00+01:00", "category": "food"})
	require.NoError(t, err)
	assert.Equal(t, testUserId, stored.UserId)

	_, err = ts.handler.CreateExpense(userCtx, map[string]any{"amount": "1", "occurred_at": "2026-11-07T13:20:00+01:00", "category": "food", "user_id": "user-2"})
	assert.Equal(t, map[string]string{"user_id": "unknown field"}, validationFields(t, err), "the owner can't be chosen")
}

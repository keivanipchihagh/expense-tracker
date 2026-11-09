package handler

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
	"golang.org/x/crypto/bcrypt"
)

// dummyHash is compared against when an email has no account, so a sign-in
// takes as long whether or not the account exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("no account has this password"), bcrypt.DefaultCost)

// SignUpInput is what a new account needs.
type SignUpInput struct {
	Email    string
	Password string
	// FirstName and LastName are required (see SignUp).
	FirstName string
	LastName  string
}

// SignUp creates an account, unless sign-up is disabled or rate limited.
func (h *Handler) SignUp(ctx context.Context, in SignUpInput) (models.User, error) {
	if !h.signupEnabled {
		return models.User{}, pkg.ErrSignupDisabled
	}
	if h.signups.full(h.now()) {
		h.logger.WarnContext(ctx, "sign-up refused: rate limit reached")
		return models.User{}, pkg.ErrTooManySignups
	}
	user, err := h.createUser(ctx, in, true)
	if err != nil {
		return models.User{}, err
	}
	h.signups.record(h.now())
	h.logger.InfoContext(ctx, "user signed up", "user_id", user.Id)
	return user, nil
}

// EnsureOwner returns the account with the given email, creating it if
// needed. It ignores the sign-up switch and doesn't require a name: it's for
// the startup migration that gives data from before accounts an owner.
func (h *Handler) EnsureOwner(ctx context.Context, in SignUpInput) (models.User, error) {
	existing, err := h.userRepo.GetByEmail(ctx, normalizeEmail(in.Email))
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, repositories.ErrUserNotFound) {
		h.logger.ErrorContext(ctx, "look up owner failed", "error", err)
		return models.User{}, pkg.InternalError
	}
	user, err := h.createUser(ctx, in, false)
	if err != nil {
		return models.User{}, err
	}
	h.logger.InfoContext(ctx, "owner account created", "user_id", user.Id)
	return user, nil
}

func (h *Handler) createUser(ctx context.Context, in SignUpInput, requireNames bool) (models.User, error) {
	errs := map[string]string{}
	email := validateEmail(in.Email, errs)
	firstName := validateName("first_name", in.FirstName, requireNames, errs)
	lastName := validateName("last_name", in.LastName, requireNames, errs)
	validatePassword("password", in.Password, errs)
	if len(errs) > 0 {
		return models.User{}, pkg.NewValidationError(errs)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), h.bcryptCost)
	if err != nil {
		h.logger.ErrorContext(ctx, "hash password failed", "error", err)
		return models.User{}, pkg.InternalError
	}
	now := h.now().UTC()
	user := models.User{
		Id:           uuid.Must(uuid.NewV7()).String(),
		Email:        email,
		FirstName:    firstName,
		LastName:     lastName,
		PasswordHash: string(hash),
		Currency:     models.DefaultCurrency,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := h.userRepo.Create(ctx, user); err != nil {
		return models.User{}, h.userRepoError(ctx, "create user failed", err)
	}
	h.seedCategories(ctx, user.Id)
	return user, nil
}

// Authenticate checks an email and password. Wrong passwords count per
// email: after maxFailedLogins in failedLoginWindow that email is refused
// with ErrTooManyLogins until the window passes. A wrong email and a wrong
// password both return ErrWrongPassword, after the same amount of work.
func (h *Handler) Authenticate(ctx context.Context, email, password string) (models.User, error) {
	email = normalizeEmail(email)
	now := h.now()
	if h.logins.full(email, now) {
		return models.User{}, ErrTooManyLogins
	}

	user, err := h.userRepo.GetByEmail(ctx, email)
	hash := []byte(user.PasswordHash)
	switch {
	case errors.Is(err, repositories.ErrUserNotFound):
		hash = dummyHash
	case err != nil:
		h.logger.ErrorContext(ctx, "look up user failed", "error", err)
		return models.User{}, pkg.InternalError
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || err != nil {
		h.logins.record(email, now)
		h.logger.WarnContext(ctx, "sign-in failed: wrong email or password")
		return models.User{}, ErrWrongPassword
	}
	return user, nil
}

// CurrentUser returns the signed-in user.
func (h *Handler) CurrentUser(ctx context.Context) (models.User, error) {
	uid, err := userId(ctx)
	if err != nil {
		return models.User{}, err
	}
	user, err := h.userRepo.Get(ctx, uid)
	if errors.Is(err, repositories.ErrUserNotFound) {
		return models.User{}, pkg.ErrNotSignedIn
	}
	if err != nil {
		h.logger.ErrorContext(ctx, "get user failed", "error", err)
		return models.User{}, pkg.InternalError
	}
	return user, nil
}

// UpdateUser changes the signed-in user's first name, last name or
// currency. Keys missing from patch stay as they are. The email can't be
// changed; other keys are rejected.
func (h *Handler) UpdateUser(ctx context.Context, patch map[string]any) (models.User, error) {
	user, err := h.CurrentUser(ctx)
	if err != nil {
		return models.User{}, err
	}

	errs := map[string]string{}
	updated := user
	for key, v := range patch {
		switch key {
		case "email":
			errs[key] = "cannot be changed"
			continue
		case "first_name", "last_name", "currency":
		default:
			errs[key] = "unknown field"
			continue
		}
		s, isString := v.(string)
		if !isString {
			errs[key] = "must be a string"
			continue
		}
		switch key {
		case "first_name":
			updated.FirstName = validateName(key, s, true, errs)
		case "last_name":
			updated.LastName = validateName(key, s, true, errs)
		case "currency":
			if c := models.Currency(s); c.IsValid() {
				updated.Currency = c
			} else {
				errs["currency"] = "must be one of " + joinValues(models.Currencies)
			}
		}
	}
	if len(errs) > 0 {
		return models.User{}, pkg.NewValidationError(errs)
	}
	if updated == user {
		return user, nil
	}

	updated.UpdatedAt = h.now().UTC()
	if err := h.userRepo.Update(ctx, updated); err != nil {
		return models.User{}, h.userRepoError(ctx, "update user failed", err)
	}
	h.logger.InfoContext(ctx, "user updated", "user_id", user.Id)
	return updated, nil
}

// ChangePassword sets a new password after checking the current one, and
// signs out the user's other browsers. keepSession is the current UI
// session token to keep ("" when the change comes through the API).
func (h *Handler) ChangePassword(ctx context.Context, current, next, keepSession string) error {
	user, err := h.CurrentUser(ctx)
	if err != nil {
		return err
	}
	errs := map[string]string{}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(current)) != nil {
		errs["current_password"] = "is wrong"
	}
	validatePassword("new_password", next, errs)
	if len(errs) > 0 {
		return pkg.NewValidationError(errs)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(next), h.bcryptCost)
	if err != nil {
		h.logger.ErrorContext(ctx, "hash password failed", "error", err)
		return pkg.InternalError
	}
	user.PasswordHash = string(hash)
	user.UpdatedAt = h.now().UTC()
	if err := h.userRepo.Update(ctx, user); err != nil {
		return h.userRepoError(ctx, "change password failed", err)
	}
	if err := h.sessionRepo.DeleteOthers(ctx, user.Id, hashSecret(keepSession)); err != nil {
		h.logger.WarnContext(ctx, "sign out other sessions failed", "error", err)
	}
	h.logger.InfoContext(ctx, "password changed", "user_id", user.Id)
	return nil
}

// DisplayCurrency returns the currency the signed-in user's amounts are
// shown in. Nothing is converted.
func (h *Handler) DisplayCurrency(ctx context.Context) (models.Currency, error) {
	user, err := h.CurrentUser(ctx)
	if err != nil {
		return "", err
	}
	if !user.Currency.IsValid() {
		return models.DefaultCurrency, nil
	}
	return user.Currency, nil
}

// SetDisplayCurrency saves the signed-in user's display currency.
func (h *Handler) SetDisplayCurrency(ctx context.Context, c models.Currency) error {
	_, err := h.UpdateUser(ctx, map[string]any{"currency": string(c)})
	return err
}

func (h *Handler) userRepoError(ctx context.Context, msg string, err error) error {
	switch {
	case errors.Is(err, repositories.ErrEmailTaken):
		return pkg.NewValidationError(map[string]string{"email": "is taken"})
	case errors.Is(err, repositories.ErrUserNotFound):
		return pkg.ErrNotSignedIn
	}
	h.logger.ErrorContext(ctx, msg, "error", err)
	return pkg.InternalError
}

// validateEmail lowercases and checks an email address, recording problems.
func validateEmail(s string, errs map[string]string) string {
	s = normalizeEmail(s)
	addr, err := mail.ParseAddress(s)
	switch {
	case s == "":
		errs["email"] = "is required"
	case len(s) > models.MaxEmailLength:
		errs["email"] = fmt.Sprintf("must be at most %d characters", models.MaxEmailLength)
	case err != nil || addr.Address != s || !strings.Contains(s[strings.LastIndex(s, "@")+1:], "."):
		errs["email"] = "must be an email address, e.g. you@example.com"
	}
	return s
}

// validateName trims a first or last name and checks it's present (when
// required), not too long and free of control characters, recording problems.
func validateName(field, s string, required bool, errs map[string]string) string {
	s = strings.TrimSpace(s)
	switch {
	case required && s == "":
		errs[field] = "is required"
	case utf8.RuneCountInString(s) > models.MaxNameLength:
		errs[field] = fmt.Sprintf("must be at most %d characters", models.MaxNameLength)
	case strings.IndexFunc(s, unicode.IsControl) >= 0:
		errs[field] = "must not contain control characters"
	}
	return s
}

func validatePassword(field, s string, errs map[string]string) {
	switch {
	case utf8.RuneCountInString(s) < models.MinPasswordLength:
		errs[field] = fmt.Sprintf("must be at least %d characters", models.MinPasswordLength)
	case len(s) > models.MaxPasswordBytes:
		errs[field] = fmt.Sprintf("must be at most %d bytes", models.MaxPasswordBytes)
	}
}

func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

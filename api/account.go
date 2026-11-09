package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg"
	"github.com/keivanipchihagh/expense-tracker/pkg/models"
)

// accountView is what the settings page shows and edits. The email is
// shown but can't be changed.
type accountView struct {
	Email, FirstName, LastName, Currency string
}

// currencyOption is one entry of the display currency select box. Label
// is the code, followed by the symbol when it differs, e.g. "EUR (€)".
type currencyOption struct {
	Code     string
	Label    string
	Selected bool
}

// CurrencyOptions feeds the display currency select box.
func (a accountView) CurrencyOptions() []currencyOption {
	out := make([]currencyOption, len(models.Currencies))
	for i, c := range models.Currencies {
		label := string(c)
		if sym := strings.TrimSpace(c.Symbol()); sym != label {
			label += " (" + sym + ")"
		}
		out[i] = currencyOption{Code: string(c), Label: label, Selected: string(c) == a.Currency}
	}
	return out
}

// SignupPage handles GET /signup.
func (s *Server) SignupPage(w http.ResponseWriter, r *http.Request) {
	if s.signedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderPage(w, http.StatusOK, "signup.html", pageData{Title: "Create an account", SignupEnabled: s.handler.SignupEnabled()})
}

// SignupSubmit handles POST /signup: create the account and sign in.
func (s *Server) SignupSubmit(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	form := map[string]string{
		"email":      r.PostForm.Get("email"),
		"first_name": r.PostForm.Get("first_name"), "last_name": r.PostForm.Get("last_name"),
	}
	page := func(status int, data pageData) {
		data.Title, data.Form, data.SignupEnabled = "Create an account", form, s.handler.SignupEnabled()
		s.renderPage(w, status, "signup.html", data)
	}

	user, err := s.handler.SignUp(r.Context(), handler.SignUpInput{
		Email: form["email"], Password: r.PostForm.Get("password"),
		FirstName: form["first_name"], LastName: form["last_name"],
	})
	if appErr, ok := errors.AsType[*pkg.Error](err); ok {
		if len(appErr.Fields) > 0 {
			page(http.StatusUnprocessableEntity, pageData{Errors: appErr.Fields})
		} else {
			page(appErr.Status, pageData{Error: appErr.Message})
		}
		return
	}
	if err != nil {
		page(http.StatusInternalServerError, pageData{Error: "Something went wrong. Try again."})
		return
	}

	token, expiresAt, err := s.handler.StartSession(r.Context(), user)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, s.sessionCookie(token, expiresAt))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// SettingsPage handles GET /settings.
func (s *Server) SettingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderAccount(w, r, http.StatusOK, pageData{})
}

// AccountRedirect sends the old /account address to /settings.
func (s *Server) AccountRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/settings", http.StatusMovedPermanently)
}

// SettingsSubmit handles POST /settings: first name, last name and
// currency. The email can't be changed, so it's never read.
func (s *Server) SettingsSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	patch := map[string]any{}
	for _, key := range []string{"first_name", "last_name", "currency"} {
		if r.PostForm.Has(key) {
			patch[key] = r.PostForm.Get(key)
		}
	}
	_, err := s.handler.UpdateUser(r.Context(), patch)
	if appErr, ok := errors.AsType[*pkg.Error](err); ok && len(appErr.Fields) > 0 {
		s.renderAccount(w, r, http.StatusUnprocessableEntity, pageData{
			Errors: appErr.Fields,
			Form:   map[string]string{"first_name": r.PostForm.Get("first_name"), "last_name": r.PostForm.Get("last_name")},
		})
		return
	}
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderAccount(w, r, http.StatusOK, pageData{Notice: "Saved."})
}

// PasswordSubmit handles POST /settings/password. Other browsers are signed
// out; this one stays signed in.
func (s *Server) PasswordSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var keep string
	if c, err := r.Cookie(s.cookieName()); err == nil {
		keep = c.Value
	}
	err := s.handler.ChangePassword(r.Context(), r.PostForm.Get("current_password"), r.PostForm.Get("new_password"), keep)
	if appErr, ok := errors.AsType[*pkg.Error](err); ok && len(appErr.Fields) > 0 {
		s.renderAccount(w, r, http.StatusUnprocessableEntity, pageData{Errors: appErr.Fields})
		return
	}
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	s.renderAccount(w, r, http.StatusOK, pageData{Notice: "Password changed. Other browsers were signed out."})
}

// ExportDataDownload handles GET /settings/export: a JSON download of
// everything stored about the account.
func (s *Server) ExportDataDownload(w http.ResponseWriter, r *http.Request) {
	data, err := s.handler.ExportData(r.Context())
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	name := "expense-tracker-data-" + data.ExportedAt.Format(time.DateOnly) + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		slog.ErrorContext(r.Context(), "export data failed", "error", err)
	}
}

// DeleteAccountSubmit handles POST /settings/delete: check the password,
// delete the account and everything in it, and sign out.
func (s *Server) DeleteAccountSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	err := s.handler.DeleteAccount(r.Context(), r.PostForm.Get("password"))
	if appErr, ok := errors.AsType[*pkg.Error](err); ok && len(appErr.Fields) > 0 {
		// Reopen the dialog with the error.
		s.renderAccount(w, r, http.StatusUnprocessableEntity, pageData{Errors: map[string]string{"delete_password": appErr.Fields["password"]}})
		return
	}
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	expired := s.sessionCookie("", time.Unix(0, 0))
	expired.MaxAge = -1
	http.SetCookie(w, expired)
	http.Redirect(w, r, "/login?deleted=1", http.StatusSeeOther)
}

func (s *Server) renderAccount(w http.ResponseWriter, r *http.Request, status int, data pageData) {
	user, err := s.handler.CurrentUser(r.Context())
	if err != nil {
		s.renderUiError(w, r, err)
		return
	}
	view := accountView{Email: user.Email, FirstName: user.FirstName, LastName: user.LastName, Currency: string(user.Currency)}
	// After a rejected change, show what was typed rather than what's saved.
	if v, ok := data.Form["first_name"]; ok {
		view.FirstName = v
	}
	if v, ok := data.Form["last_name"]; ok {
		view.LastName = v
	}
	data.Title, data.Nav, data.Account = "Settings", "settings", &view
	s.renderSignedIn(w, r, status, "settings.html", data)
}

package api

import (
	"net/http"

	"github.com/keivanipchihagh/expense-tracker/internal/handler"
)

// SignUp handles POST /api/v1/users: create an account. It's public; the
// new account then signs in through OAuth like any client.
func (s *Server) SignUp(w http.ResponseWriter, r *http.Request) {
	input, err := decodeJsonObject(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	in, fieldErrors := signUpInput(input)
	if len(fieldErrors) > 0 {
		writeError(w, http.StatusUnprocessableEntity, codeValidationFailed, "validation failed", fieldErrors)
		return
	}
	user, err := s.handler.SignUp(r.Context(), in)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusCreated, user)
}

// GetMe handles GET /api/v1/me: the account the token belongs to.
func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	user, err := s.handler.CurrentUser(r.Context())
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusOK, user)
}

// UpdateMe handles PATCH /api/v1/me: change first name, last name or
// currency. The email can't be changed.
func (s *Server) UpdateMe(w http.ResponseWriter, r *http.Request) {
	patch, err := decodeJsonObject(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	user, err := s.handler.UpdateUser(r.Context(), patch)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusOK, user)
}

// ChangeMyPassword handles PUT /api/v1/me/password with
// {"current_password", "new_password"}. It signs out the user's browsers.
func (s *Server) ChangeMyPassword(w http.ResponseWriter, r *http.Request) {
	input, err := decodeJsonObject(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	fieldErrors := map[string]string{}
	current := stringField(input, "current_password", fieldErrors)
	next := stringField(input, "new_password", fieldErrors)
	for key := range input {
		if key != "current_password" && key != "new_password" {
			fieldErrors[key] = "unknown field"
		}
	}
	if len(fieldErrors) > 0 {
		writeError(w, http.StatusUnprocessableEntity, codeValidationFailed, "validation failed", fieldErrors)
		return
	}
	if err := s.handler.ChangePassword(r.Context(), current, next, ""); err != nil {
		writeHandlerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ExportMe handles GET /api/v1/me/export: everything stored about the
// account, as JSON.
func (s *Server) ExportMe(w http.ResponseWriter, r *http.Request) {
	data, err := s.handler.ExportData(r.Context())
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	writeJson(w, http.StatusOK, data)
}

// DeleteMe handles DELETE /api/v1/me with {"password"}: permanently delete
// the account and everything in it.
func (s *Server) DeleteMe(w http.ResponseWriter, r *http.Request) {
	input, err := decodeJsonObject(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	fieldErrors := map[string]string{}
	password := stringField(input, "password", fieldErrors)
	for key := range input {
		if key != "password" {
			fieldErrors[key] = "unknown field"
		}
	}
	if len(fieldErrors) > 0 {
		writeError(w, http.StatusUnprocessableEntity, codeValidationFailed, "validation failed", fieldErrors)
		return
	}
	if err := s.handler.DeleteAccount(r.Context(), password); err != nil {
		writeHandlerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// signUpInput reads {"email", "password", "first_name", "last_name"}, all
// required strings.
func signUpInput(input map[string]any) (handler.SignUpInput, map[string]string) {
	fieldErrors := map[string]string{}
	in := handler.SignUpInput{
		Email:     stringField(input, "email", fieldErrors),
		Password:  stringField(input, "password", fieldErrors),
		FirstName: stringField(input, "first_name", fieldErrors),
		LastName:  stringField(input, "last_name", fieldErrors),
	}
	for key := range input {
		switch key {
		case "email", "password", "first_name", "last_name":
		default:
			fieldErrors[key] = "unknown field"
		}
	}
	return in, fieldErrors
}

// stringField reads a required string field.
func stringField(input map[string]any, key string, fieldErrors map[string]string) string {
	v, ok := input[key]
	if !ok || v == nil {
		fieldErrors[key] = "is required"
		return ""
	}
	s, isString := v.(string)
	if !isString {
		fieldErrors[key] = "must be a string"
	}
	return s
}

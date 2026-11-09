package pkg

import (
	"fmt"
	"net/http"
)

// Error is a domain error carrying the HTTP status and code the API sends.
type Error struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e *Error) Error() string {
	if len(e.Fields) == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Fields)
}

var (
	InternalError      = &Error{Status: http.StatusInternalServerError, Code: "internal", Message: "internal server error"}
	ErrExpenseNotFound = &Error{Status: http.StatusNotFound, Code: "not_found", Message: "expense not found"}
	ErrAppNotFound     = &Error{Status: http.StatusNotFound, Code: "not_found", Message: "app not found"}
	// ErrNotSignedIn means a request reached a handler without a user; the
	// auth middleware should have stopped it.
	ErrNotSignedIn = &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "not signed in"}
	// ErrSignupDisabled is returned while SIGNUP_ENABLED is false.
	ErrSignupDisabled = &Error{Status: http.StatusForbidden, Code: "signup_disabled", Message: "sign-up is disabled on this server"}
	// ErrTooManySignups is returned when the sign-up rate limit is reached.
	ErrTooManySignups = &Error{Status: http.StatusTooManyRequests, Code: "rate_limited", Message: "too many sign-ups, try again later"}
)

// OAuthError is an OAuth 2.0 protocol error. The API sends it as
// {"error": Code, "error_description": Description} (RFC 6749 section 5.2).
type OAuthError struct {
	Status      int
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	return e.Code + ": " + e.Description
}

// NewOAuthError returns an OAuth error with the given status, code and description.
func NewOAuthError(status int, code, description string) *OAuthError {
	return &OAuthError{Status: status, Code: code, Description: description}
}

// NewValidationError returns a 422 error mapping each invalid field to the
// reason it was rejected.
func NewValidationError(fields map[string]string) *Error {
	return &Error{
		Status:  http.StatusUnprocessableEntity,
		Code:    "validation_failed",
		Message: "validation failed",
		Fields:  fields,
	}
}

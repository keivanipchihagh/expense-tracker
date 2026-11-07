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
)

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

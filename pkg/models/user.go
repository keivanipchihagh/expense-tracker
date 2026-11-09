package models

import (
	"strings"
	"time"
	"unicode"
)

// Limits on account fields.
const (
	MaxEmailLength    = 254
	MaxNameLength     = 50
	MinPasswordLength = 10
	// MaxPasswordBytes is bcrypt's limit; longer passwords would be cut.
	MaxPasswordBytes = 72
)

// User is an account. Each user sees and changes only their own expenses.
type User struct {
	Id string `json:"id"`
	// Email is unique, lowercase, and used to sign in.
	Email string `json:"email"`
	// FirstName and LastName are required at sign-up; an account migrated
	// from before accounts may have neither.
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	// PasswordHash is the bcrypt hash of the password; never serialized.
	PasswordHash string `json:"-"`
	// Currency is the display currency: amounts have none of their own.
	Currency  Currency  `json:"currency"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DisplayName is the user's full name, or their email without one.
func (u User) DisplayName() string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		return u.Email
	}
	return name
}

// Initials are up to two letters for the profile icon: from the first and
// last name, or the email's first letter.
func (u User) Initials() string {
	var out []rune
	for _, part := range []string{u.FirstName, u.LastName} {
		if r := []rune(strings.TrimSpace(part)); len(r) > 0 {
			out = append(out, unicode.ToUpper(r[0]))
		}
	}
	if len(out) == 0 {
		if r := []rune(u.Email); len(r) > 0 {
			out = append(out, unicode.ToUpper(r[0]))
		}
	}
	return string(out)
}

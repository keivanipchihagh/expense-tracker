package models

import "time"

// Session is a signed-in UI session. Only the hash of its cookie value is stored.
type Session struct {
	TokenHash string
	UserId    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

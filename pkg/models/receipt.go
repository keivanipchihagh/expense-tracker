package models

import "time"

// Receipt is the photo kept with an expense, as uploaded after shrinking.
type Receipt struct {
	ExpenseId string
	UserId    string
	// MimeType is sniffed from the bytes: image/jpeg, image/png or image/webp.
	MimeType  string
	Data      []byte
	CreatedAt time.Time
}

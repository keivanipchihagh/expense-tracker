package models

import (
	"fmt"
	"time"
)

// Expense is one logged expense. Core fields are typed; the user-defined
// fields from fields.yaml are in Fields, keyed by field name.
type Expense struct {
	Id string `json:"id"`
	// Amount is an exact decimal string, e.g. "12.50".
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	// OccurredAt keeps the UTC offset it was recorded with.
	OccurredAt time.Time      `json:"occurred_at"`
	RawInput   *string        `json:"raw_input"`
	Fields     map[string]any `json:"-"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

func (e *Expense) String() string {
	return fmt.Sprintf("Expense{Id: %s, Amount: %s %s, OccurredAt: %s}",
		e.Id, e.Amount, e.Currency, e.OccurredAt.Format(time.RFC3339))
}

package models

import (
	"encoding/json"
	"fmt"
	"time"
)

// LogAction is the kind of change a Log records.
type LogAction int

const (
	LogActionUnspecified LogAction = iota
	LogActionCreate
	LogActionUpdate
	LogActionDelete
)

func (a LogAction) String() string {
	switch a {
	case LogActionCreate:
		return "create"
	case LogActionUpdate:
		return "update"
	case LogActionDelete:
		return "delete"
	default:
		return "unspecified"
	}
}

// ParseLogAction is the inverse of String; unknown text is
// LogActionUnspecified.
func ParseLogAction(s string) LogAction {
	switch s {
	case "create":
		return LogActionCreate
	case "update":
		return LogActionUpdate
	case "delete":
		return LogActionDelete
	default:
		return LogActionUnspecified
	}
}

// Log records one change to an expense as JSON snapshots of the row.
// Before is nil for a create and After is nil for a delete.
type Log struct {
	Id        int64  `json:"id"`
	ExpenseId string `json:"expense_id"`
	// UserId is the expense's owner, kept after the expense is deleted.
	UserId    string          `json:"user_id"`
	Action    LogAction       `json:"action"`
	Before    json.RawMessage `json:"before"`
	After     json.RawMessage `json:"after"`
	CreatedAt time.Time       `json:"created_at"`
}

// NewLog builds a Log for a change to expenseId. It panics when the
// snapshots don't match the action: create has only After, delete has only
// Before, update has both.
func NewLog(expenseId, userId string, action LogAction, before, after json.RawMessage) *Log {
	if expenseId == "" || userId == "" {
		panic("log expense id and user id must not be empty")
	}
	switch action {
	case LogActionCreate:
		if before != nil || after == nil {
			panic("create log must have only an after snapshot")
		}
	case LogActionUpdate:
		if before == nil || after == nil {
			panic("update log must have before and after snapshots")
		}
	case LogActionDelete:
		if before == nil || after != nil {
			panic("delete log must have only a before snapshot")
		}
	default:
		panic(fmt.Sprintf("invalid log action %d", action))
	}

	return &Log{
		ExpenseId: expenseId,
		UserId:    userId,
		Action:    action,
		Before:    before,
		After:     after,
		CreatedAt: time.Now().UTC(),
	}
}

func (l *Log) String() string {
	return fmt.Sprintf("Log{Id: %d, ExpenseId: %s, Action: %s, CreatedAt: %s}",
		l.Id, l.ExpenseId, l.Action, l.CreatedAt.Format(time.RFC3339))
}

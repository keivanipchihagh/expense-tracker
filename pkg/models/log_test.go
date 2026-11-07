package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLog(t *testing.T) {
	snapshot := json.RawMessage(`{"id":"e1"}`)

	tests := []struct {
		name      string
		expenseId string
		action    LogAction
		before    json.RawMessage
		after     json.RawMessage
		wantPanic bool
	}{
		{name: "create with after", expenseId: "e1", action: LogActionCreate, after: snapshot},
		{name: "update with both", expenseId: "e1", action: LogActionUpdate, before: snapshot, after: snapshot},
		{name: "delete with before", expenseId: "e1", action: LogActionDelete, before: snapshot},
		{name: "empty expense id", expenseId: "", action: LogActionCreate, after: snapshot, wantPanic: true},
		{name: "create with before", expenseId: "e1", action: LogActionCreate, before: snapshot, after: snapshot, wantPanic: true},
		{name: "create without after", expenseId: "e1", action: LogActionCreate, wantPanic: true},
		{name: "update without before", expenseId: "e1", action: LogActionUpdate, after: snapshot, wantPanic: true},
		{name: "update without after", expenseId: "e1", action: LogActionUpdate, before: snapshot, wantPanic: true},
		{name: "delete with after", expenseId: "e1", action: LogActionDelete, before: snapshot, after: snapshot, wantPanic: true},
		{name: "unspecified action", expenseId: "e1", action: LogActionUnspecified, after: snapshot, wantPanic: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			build := func() { NewLog(tt.expenseId, tt.action, tt.before, tt.after) }
			if tt.wantPanic {
				assert.Panics(t, build)
				return
			}

			l := NewLog(tt.expenseId, tt.action, tt.before, tt.after)
			assert.Equal(t, tt.expenseId, l.ExpenseId)
			assert.Equal(t, tt.action, l.Action)
			assert.False(t, l.CreatedAt.IsZero())
			assert.Equal(t, "UTC", l.CreatedAt.Location().String())
		})
	}
}

func TestLogAction_String(t *testing.T) {
	assert.Equal(t, "create", LogActionCreate.String())
	assert.Equal(t, "update", LogActionUpdate.String())
	assert.Equal(t, "delete", LogActionDelete.String())
	assert.Equal(t, "unspecified", LogActionUnspecified.String())
}

package utils

import (
	"errors"
	"time"
)

var errOffsetTime = errors.New("must be an RFC 3339 timestamp with a UTC offset, e.g. 2026-11-07T13:20:00+01:00")

// ParseOffsetTime parses an RFC 3339 timestamp that carries a UTC offset
// ("Z" or "±hh:mm"). Timestamps without an offset are rejected. The offset
// is kept, so the local date and time the value was recorded in survive.
func ParseOffsetTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, errOffsetTime
	}
	return t, nil
}

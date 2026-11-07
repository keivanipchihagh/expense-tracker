package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger builds a slog logger that writes to w.
//   - level:  debug, info, warn or error (case-insensitive).
//   - format: json (one JSON object per line) or text (key=value lines).
func NewLogger(w io.Writer, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("unsupported log level %q: want debug, info, warn or error", level)
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	case "text":
		handler = slog.NewTextHandler(w, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q: want json or text", format)
	}

	return slog.New(handler), nil
}

// Setup builds a logger writing to stdout and installs it as the slog default.
func Setup(level, format string) error {
	l, err := NewLogger(os.Stdout, level, format)
	if err != nil {
		return err
	}
	slog.SetDefault(l)
	return nil
}

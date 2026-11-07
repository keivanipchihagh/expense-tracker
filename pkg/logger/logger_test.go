package logger

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogger_Format(t *testing.T) {
	tests := []struct {
		name    string
		format  string
		check   func(t *testing.T, out string)
		wantErr bool
	}{
		{
			name:   "json",
			format: "json",
			check: func(t *testing.T, out string) {
				var line map[string]any
				require.NoError(t, json.Unmarshal([]byte(out), &line))
				assert.Equal(t, "hello", line["msg"])
				assert.Equal(t, "INFO", line["level"])
				assert.Equal(t, "v", line["k"])
			},
		},
		{
			name:   "text",
			format: "text",
			check: func(t *testing.T, out string) {
				assert.Contains(t, out, "level=INFO")
				assert.Contains(t, out, "msg=hello")
				assert.Contains(t, out, "k=v")
			},
		},
		{
			name:   "case insensitive",
			format: "JSON",
			check: func(t *testing.T, out string) {
				assert.True(t, json.Valid([]byte(out)))
			},
		},
		{name: "unsupported", format: "xml", wantErr: true},
		{name: "empty", format: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l, err := NewLogger(&buf, "info", tt.format)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			l.Info("hello", "k", "v")
			tt.check(t, strings.TrimSpace(buf.String()))
		})
	}
}

func TestNewLogger_Level(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		wantDebug bool
		wantInfo  bool
		wantErr   bool
	}{
		{name: "debug logs everything", level: "debug", wantDebug: true, wantInfo: true},
		{name: "info drops debug", level: "info", wantInfo: true},
		{name: "warn drops info", level: "warn"},
		{name: "error drops info", level: "error"},
		{name: "case insensitive", level: "DEBUG", wantDebug: true, wantInfo: true},
		{name: "unsupported", level: "loud", wantErr: true},
		{name: "empty", level: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l, err := NewLogger(&buf, tt.level, "text")
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			l.Debug("debug-line")
			l.Info("info-line")

			assert.Equal(t, tt.wantDebug, strings.Contains(buf.String(), "debug-line"))
			assert.Equal(t, tt.wantInfo, strings.Contains(buf.String(), "info-line"))
		})
	}
}

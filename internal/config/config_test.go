package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantLevel   string
		wantFormat  string
		wantVersion string
	}{
		{
			name:        "defaults",
			env:         map[string]string{},
			wantLevel:   "info",
			wantFormat:  "json",
			wantVersion: "dev",
		},
		{
			name: "values from prefixed environment variables",
			env: map[string]string{
				"EXPENSE_TRACKER_LOGGER_LEVEL":  "debug",
				"EXPENSE_TRACKER_LOGGER_FORMAT": "text",
				"EXPENSE_TRACKER_APP_VERSION":   "v1.2.3",
			},
			wantLevel:   "debug",
			wantFormat:  "text",
			wantVersion: "v1.2.3",
		},
		{
			name: "unprefixed variables are ignored",
			env: map[string]string{
				"LOGGER_LEVEL": "debug",
				"APP_VERSION":  "v9.9.9",
			},
			wantLevel:   "info",
			wantFormat:  "json",
			wantVersion: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := NewConfig()
			require.NoError(t, err)

			assert.Equal(t, tt.wantLevel, cfg.Logger.Level)
			assert.Equal(t, tt.wantFormat, cfg.Logger.Format)
			assert.Equal(t, tt.wantVersion, cfg.App.Version)
		})
	}
}

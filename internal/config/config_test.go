package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfig(t *testing.T) {
	defaults := Config{
		AppVersion:         "dev",
		LogLevel:           "info",
		HttpPort:           8000,
		HttpRequestTimeout: 30 * time.Second,
		HttpMaxBodyBytes:   1 << 20,
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults",
			env:  map[string]string{},
			want: defaults,
		},
		{
			name: "values from environment variables",
			env: map[string]string{
				"APP_VERSION":          "v1.2.3",
				"LOG_LEVEL":            "debug",
				"HTTP_PORT":            "9000",
				"HTTP_REQUEST_TIMEOUT": "5s",
				"HTTP_MAX_BODY_BYTES":  "2048",
			},
			want: Config{
				AppVersion:         "v1.2.3",
				LogLevel:           "debug",
				HttpPort:           9000,
				HttpRequestTimeout: 5 * time.Second,
				HttpMaxBodyBytes:   2048,
			},
		},
		{
			name: "similarly named variables are ignored",
			env: map[string]string{
				"PORT":    "9000",
				"VERSION": "v9.9.9",
				"LEVEL":   "debug",
			},
			want: defaults,
		},
		{
			name:    "invalid port",
			env:     map[string]string{"HTTP_PORT": "abc"},
			wantErr: true,
		},
		{
			name:    "invalid duration",
			env:     map[string]string{"HTTP_REQUEST_TIMEOUT": "soon"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := NewConfig()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg)
		})
	}
}

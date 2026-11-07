package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPassword = "correct-horse-battery-staple"

func TestNewConfig(t *testing.T) {
	defaults := Config{
		AppVersion:         "dev",
		LogLevel:           "info",
		HttpPort:           8000,
		HttpRequestTimeout: 30 * time.Second,
		HttpMaxBodyBytes:   1 << 20,
		SqlitePath:         "data/expense.db",
		PublicUrl:          "http://localhost:8000",
		AuthPassword:       testPassword,
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr string
	}{
		{
			name: "defaults",
			env:  map[string]string{"AUTH_PASSWORD": testPassword},
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
				"SQLITE_PATH":          "/data/expense.db",
				"PUBLIC_URL":           "https://expenses.example.com/",
				"AUTH_PASSWORD":        testPassword,
			},
			want: Config{
				AppVersion:         "v1.2.3",
				LogLevel:           "debug",
				HttpPort:           9000,
				HttpRequestTimeout: 5 * time.Second,
				HttpMaxBodyBytes:   2048,
				SqlitePath:         "/data/expense.db",
				PublicUrl:          "https://expenses.example.com",
				AuthPassword:       testPassword,
			},
		},
		{
			name: "similarly named variables are ignored",
			env: map[string]string{
				"AUTH_PASSWORD": testPassword,
				"PORT":          "9000",
				"VERSION":       "v9.9.9",
				"LEVEL":         "debug",
			},
			want: defaults,
		},
		{name: "invalid port", env: map[string]string{"AUTH_PASSWORD": testPassword, "HTTP_PORT": "abc"}, wantErr: "parse environment"},
		{name: "invalid duration", env: map[string]string{"AUTH_PASSWORD": testPassword, "HTTP_REQUEST_TIMEOUT": "soon"}, wantErr: "parse environment"},
		{name: "missing password", env: map[string]string{}, wantErr: "AUTH_PASSWORD"},
		{name: "template password", env: map[string]string{"AUTH_PASSWORD": "change-me"}, wantErr: "template value"},
		{name: "short password", env: map[string]string{"AUTH_PASSWORD": "short"}, wantErr: "at least 16 characters"},
		{name: "relative public url", env: map[string]string{"AUTH_PASSWORD": testPassword, "PUBLIC_URL": "expenses.example.com"}, wantErr: "PUBLIC_URL"},
		{name: "non-http public url", env: map[string]string{"AUTH_PASSWORD": testPassword, "PUBLIC_URL": "ftp://example.com"}, wantErr: "PUBLIC_URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make sure a password from the developer's shell doesn't leak in.
			t.Setenv("AUTH_PASSWORD", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := NewConfig()
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg)
		})
	}
}

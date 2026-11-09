package config

import (
	"os"
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
		SignupEnabled:      true,
		OpenRouterModel:    "nvidia/nemotron-3.5-super-vl:free",
		OpenRouterBaseUrl:  "https://openrouter.ai/api/v1",
	}

	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr string
	}{
		{
			name: "defaults: nothing is required",
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
				"SQLITE_PATH":          "/data/expense.db",
				"PUBLIC_URL":           "https://expenses.example.com/",
				"SIGNUP_ENABLED":       "false",
				"OWNER_EMAIL":          "keivan@example.com",
				"OPENROUTER_API_KEY":   "sk-or-test",
				"OPENROUTER_MODEL":     "google/gemma-4-31b-it:free",
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
				SignupEnabled:      false,
				OwnerEmail:         "keivan@example.com",
				OpenRouterApiKey:   "sk-or-test",
				OpenRouterModel:    "google/gemma-4-31b-it:free",
				OpenRouterBaseUrl:  "https://openrouter.ai/api/v1",
				AuthPassword:       testPassword,
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
		{name: "invalid port", env: map[string]string{"AUTH_PASSWORD": testPassword, "HTTP_PORT": "abc"}, wantErr: "parse environment"},
		{name: "invalid duration", env: map[string]string{"AUTH_PASSWORD": testPassword, "HTTP_REQUEST_TIMEOUT": "soon"}, wantErr: "parse environment"},
		{name: "template password", env: map[string]string{"AUTH_PASSWORD": "change-me"}, wantErr: "template value"},
		{name: "short password", env: map[string]string{"AUTH_PASSWORD": "short"}, wantErr: "at least 10 characters"},
		{name: "invalid signup switch", env: map[string]string{"SIGNUP_ENABLED": "maybe"}, wantErr: "parse environment"},
		{name: "relative public url", env: map[string]string{"AUTH_PASSWORD": testPassword, "PUBLIC_URL": "expenses.example.com"}, wantErr: "PUBLIC_URL"},
		{name: "non-http public url", env: map[string]string{"AUTH_PASSWORD": testPassword, "PUBLIC_URL": "ftp://example.com"}, wantErr: "PUBLIC_URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make sure a password from the developer's shell doesn't leak in.
			for _, k := range []string{"AUTH_PASSWORD", "OWNER_EMAIL", "SIGNUP_ENABLED", "OPENROUTER_API_KEY", "OPENROUTER_MODEL", "OPENROUTER_BASE_URL"} {
				t.Setenv(k, "") // restores the original value after the test
				require.NoError(t, os.Unsetenv(k))
			}
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

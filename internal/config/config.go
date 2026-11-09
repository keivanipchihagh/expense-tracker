package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// minPasswordLength is the shortest AUTH_PASSWORD accepted, the same as
// any account password.
const minPasswordLength = 10

// Config is read from the environment. Each tag is the exact variable name.
type Config struct {
	// AppVersion is injected at image build time (APP_VERSION build arg).
	AppVersion string `envconfig:"APP_VERSION" default:"dev"`

	// LogLevel is one of debug, info, warn, error.
	LogLevel string `envconfig:"LOG_LEVEL" default:"info"`

	// HttpPort is the port the HTTP server listens on inside the container.
	HttpPort int `envconfig:"HTTP_PORT" default:"8000"`
	// HttpRequestTimeout caps every request server-side. Zero disables it.
	HttpRequestTimeout time.Duration `envconfig:"HTTP_REQUEST_TIMEOUT" default:"30s"`
	// HttpMaxBodyBytes caps the request body size. Zero disables it.
	HttpMaxBodyBytes int64 `envconfig:"HTTP_MAX_BODY_BYTES" default:"1048576"`

	// SqlitePath is the SQLite database file. Its directory must exist.
	SqlitePath string `envconfig:"SQLITE_PATH" default:"data/expense.db"`

	// PublicUrl is the base URL clients reach the server at, without a
	// trailing slash. It appears in OAuth metadata and redirects.
	PublicUrl string `envconfig:"PUBLIC_URL" default:"http://localhost:8000"`
	// SignupEnabled lets anyone who reaches the server create an account.
	// Turn it off once your accounts exist.
	SignupEnabled bool `envconfig:"SIGNUP_ENABLED" default:"true"`

	// OwnerEmail and AuthPassword are read only to give data
	// from before accounts existed an owner: on startup, if expenses without
	// an owner exist, this account is created (or found by email) with
	// AuthPassword as its password, and gets them. Unused otherwise.
	OwnerEmail   string `envconfig:"OWNER_EMAIL"`
	AuthPassword string `envconfig:"AUTH_PASSWORD"`
}

// NewConfig reads the service configuration from the environment and
// rejects unsafe or malformed values.
func NewConfig() (Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}

	cfg.PublicUrl = strings.TrimSuffix(cfg.PublicUrl, "/")
	u, err := url.Parse(cfg.PublicUrl)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("PUBLIC_URL %q must be an absolute http(s) URL", cfg.PublicUrl)
	}

	if cfg.AuthPassword == "change-me" {
		return Config{}, errors.New("AUTH_PASSWORD is still the template value; set your own or remove it")
	}
	if cfg.AuthPassword != "" && len([]rune(cfg.AuthPassword)) < minPasswordLength {
		return Config{}, fmt.Errorf("AUTH_PASSWORD must be at least %d characters", minPasswordLength)
	}

	return cfg, nil
}

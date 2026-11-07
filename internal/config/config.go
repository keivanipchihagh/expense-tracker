package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

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
}

// NewConfig reads the service configuration from the environment.
func NewConfig() (Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	return cfg, nil
}

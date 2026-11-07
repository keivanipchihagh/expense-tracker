package config

import (
	"fmt"

	"github.com/keivanipchihagh/expense-tracker/pkg/configs"
	"github.com/kelseyhightower/envconfig"
)

// Prefix is prepended to every environment variable the service reads,
// e.g. Logger.Level is read from EXPENSE_TRACKER_LOGGER_LEVEL.
const Prefix = "expense_tracker"

type Config struct {
	Logger configs.LoggerConfig `envconfig:"logger"`
	App    configs.AppConfig    `envconfig:"app"`
}

// NewConfig reads the service configuration from the environment.
func NewConfig() (Config, error) {
	var cfg Config
	if err := envconfig.Process(Prefix, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	return cfg, nil
}

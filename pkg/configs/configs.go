package configs

type LoggerConfig struct {
	// Level is one of debug, info, warn, error.
	Level string `envconfig:"level" default:"info"`
	// Format is json or text.
	Format string `envconfig:"format" default:"json"`
}

type AppConfig struct {
	// Version is injected at image build time (APP_VERSION build arg).
	Version string `envconfig:"version" default:"dev"`
}

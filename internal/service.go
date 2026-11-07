package internal

import (
	"log/slog"

	"github.com/keivanipchihagh/expense-tracker/internal/config"
	"github.com/keivanipchihagh/expense-tracker/pkg/logger"
	"github.com/keivanipchihagh/expense-tracker/pkg/service"
)

type Service struct {
	service *service.Service
}

// Start loads the configuration and configures logging.
func (s *Service) Start(svc *service.Service) {
	s.service = svc

	// Load configuration
	cfg, err := config.NewConfig()
	if err != nil {
		service.Fatal("failed to load service config", "error", err)
	}

	// Configure logging
	if err := logger.Setup(cfg.Logger.Level, cfg.Logger.Format); err != nil {
		service.Fatal("failed to configure logging", "error", err)
	}

	slog.Info("service started", "service", svc.Name, "version", cfg.App.Version)
}

// Close releases the service's resources.
func (s *Service) Close() {
	slog.Info("service stopped", "service", s.service.Name)
}

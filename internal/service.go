package internal

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/keivanipchihagh/expense-tracker/api"
	"github.com/keivanipchihagh/expense-tracker/internal/config"
	"github.com/keivanipchihagh/expense-tracker/internal/handler"
	"github.com/keivanipchihagh/expense-tracker/pkg/db/sqlite"
	pkgHttp "github.com/keivanipchihagh/expense-tracker/pkg/http"
	"github.com/keivanipchihagh/expense-tracker/pkg/repositories"
)

// Run loads the configuration, configures logging, opens the database,
// starts the HTTP server and blocks until SIGINT or SIGTERM, then shuts
// everything down.
func Run() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Load configuration
	cfg, err := config.NewConfig()
	if err != nil {
		fatal("failed to load config", "error", err)
	}

	// Configure logging: JSON to stdout at the configured level
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		fatal("invalid log level, want debug, info, warn or error", "level", cfg.LogLevel)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	// Open database
	db, err := sqlite.NewClient(ctx, cfg.SqlitePath)
	if err != nil {
		fatal("failed to open database", "path", cfg.SqlitePath, "error", err)
	}

	// Initialize repositories
	logRepo, err := repositories.NewLogRepository(ctx, db)
	if err != nil {
		fatal("failed to initialize log repository", "error", err)
	}
	expenseRepo, err := repositories.NewExpenseRepository(ctx, db, logRepo)
	if err != nil {
		fatal("failed to initialize expense repository", "error", err)
	}
	oauthRepo, err := repositories.NewOAuthRepository(ctx, db)
	if err != nil {
		fatal("failed to initialize oauth repository", "error", err)
	}
	sessionRepo, err := repositories.NewSessionRepository(ctx, db)
	if err != nil {
		fatal("failed to initialize session repository", "error", err)
	}

	userRepo, err := repositories.NewUserRepository(ctx, db)
	if err != nil {
		fatal("failed to initialize user repository", "error", err)
	}

	// Initialize handler and API
	h := handler.NewHandler(expenseRepo, oauthRepo, sessionRepo, userRepo, cfg.PublicUrl, cfg.SignupEnabled)

	// Give data from before accounts existed an owner
	if err := adoptLegacyData(ctx, db, h, cfg); err != nil {
		fatal("failed to assign existing data to an owner", "error", err)
	}
	server := api.NewServer(h, cfg.AppVersion, cfg.PublicUrl)

	// Start HTTP server: the API needs a bearer token, the UI a session
	httpServer := pkgHttp.NewHttpServer(server.Handler(), cfg.HttpRequestTimeout, cfg.HttpMaxBodyBytes)
	addr := fmt.Sprintf("0.0.0.0:%d", cfg.HttpPort)
	if err := httpServer.Start(addr); err != nil {
		fatal("failed to start http server", "addr", addr, "error", err)
	}
	slog.Info("service started", "version", cfg.AppVersion, "addr", addr, "public_url", cfg.PublicUrl, "database", cfg.SqlitePath)

	// Wait for a termination signal, then shut down
	<-ctx.Done()
	slog.Info("termination signal received, shutting down")

	httpServer.Close()
	if err := db.Close(); err != nil {
		slog.Error("failed to close database", "error", err)
	}
	slog.Info("service stopped")
}

// fatal logs msg at error level and exits with status 1.
func fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

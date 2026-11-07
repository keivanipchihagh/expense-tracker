package service

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Service carries the name and lifetime context of a running service.
// Ctx is cancelled when the process receives SIGINT or SIGTERM.
type Service struct {
	Name string
	Ctx  context.Context
}

// ServiceInterface is the lifecycle every runnable service implements.
// Start builds and starts its components and must not block; Close stops
// them and releases resources.
type ServiceInterface interface {
	Start(service *Service)
	Close()
}

// Run starts the service, blocks until SIGINT or SIGTERM, then calls Close.
func Run(name string, svc ServiceInterface) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc.Start(&Service{Name: name, Ctx: ctx})

	<-ctx.Done()
	slog.Info("termination signal received, shutting down")

	svc.Close()
}

// Fatal logs msg at error level and exits the process with status 1.
func Fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

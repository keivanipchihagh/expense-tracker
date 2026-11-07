package http

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
)

// shutdownTimeout bounds how long Close waits for in-flight requests.
const shutdownTimeout = 10 * time.Second

// HttpServer wraps an http.Server with the standard middleware chain.
type HttpServer struct {
	*http.Server
}

// NewHttpServer wraps handler with the middleware chain, outermost first:
// recover → logging → security headers → timeout → max body size.
func NewHttpServer(handler http.Handler, requestTimeout time.Duration, maxBodyBytes int64) *HttpServer {
	chain := MiddlewareChain(
		RecoverMiddleware(),
		LoggingMiddleware(),
		SecurityHeadersMiddleware(),
		TimeoutMiddleware(requestTimeout),
		MaxBodyBytesMiddleware(maxBodyBytes),
	)

	return &HttpServer{
		Server: &http.Server{
			Handler:           chain(handler),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
}

// Start binds addr and serves in the background. A bind failure is
// returned; a later serve failure is logged and exits the process.
func (s *HttpServer) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.Addr = ln.Addr().String()

	go func() {
		if err := s.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()
	return nil
}

// Close stops accepting connections and waits for in-flight requests.
func (s *HttpServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		slog.Error("http server shutdown failed", "error", err)
	}
}

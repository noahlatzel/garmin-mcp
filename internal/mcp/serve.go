package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// shutdownTimeout bounds how long in-flight requests may drain after SIGTERM;
// systemd's own stop timeout is the backstop.
const shutdownTimeout = 10 * time.Second

// ResourceServerConfig configures ServeResourceServer. The caller resolves the
// values from its service-specific environment (documented in each cmd's doc
// comment).
type ResourceServerConfig struct {
	// Addr is the listen address, e.g. 127.0.0.1:8081 (loopback only; Caddy is
	// the sole public listener).
	Addr string
	// Service names the service in the health payload and startup log.
	Service string
	// Auth wraps the MCP handler with request authentication. In single-user
	// deployments this is StaticTokenMiddleware(token).
	Auth func(http.Handler) http.Handler
}

// ServeResourceServer serves an MCP server over Streamable HTTP. MCP traffic is
// gated by cfg.Auth, while /healthz stays unauthenticated for probes. It blocks
// until the listener fails or a SIGINT/SIGTERM arrives, then drains in-flight
// requests (bounded by shutdownTimeout) so systemd restarts don't kill active
// tool calls.
func ServeResourceServer(cfg ResourceServerConfig, server *mcp.Server) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.Handle("/healthz", HealthHandler(cfg.Service))
	mux.Handle("/", cfg.Auth(handler))

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	slog.Info("serving Streamable HTTP", "service", cfg.Service, "addr", cfg.Addr)
	return ListenAndServeGraceful(cfg.Service, httpServer)
}

// ListenAndServeGraceful runs srv until it fails or SIGINT/SIGTERM arrives,
// then shuts it down, draining in-flight requests for up to shutdownTimeout.
func ListenAndServeGraceful(service string, srv *http.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		slog.Info("shutting down, draining requests", "service", service)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

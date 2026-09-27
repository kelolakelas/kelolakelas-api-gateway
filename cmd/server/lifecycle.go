package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// serveUntilDone serves HTTP on an already bound listener until ctx is cancelled
// (SIGINT/SIGTERM through signal.NotifyContext) or the server fails (KEL-71).
//
// On cancellation the server stops accepting connections and Shutdown lets every
// in-flight request, including a proxied one still waiting on a downstream, finish.
// Shutdown does not cancel request contexts, so the proxy's per-request upstream
// deadline stays the only bound on a single exchange. When shutdownTimeout elapses
// first the remaining connections are closed and an error is returned, so the
// process always exits within the bound.
func serveUntilDone(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("serve HTTP: %w", err)
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	slog.Info("Shutdown signal received, draining HTTP", "timeout", shutdownTimeout.String())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var shutdownErr error
	if err := server.Shutdown(shutdownCtx); err != nil {
		shutdownErr = errors.Join(fmt.Errorf("HTTP shutdown did not finish in time, connections closed: %w", err), server.Close())
	}
	if err := errors.Join(shutdownErr, <-serveErr); err != nil {
		return err
	}
	slog.Info("API Gateway stopped gracefully")
	return nil
}

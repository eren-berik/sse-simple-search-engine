package main

import (
	"context"
	"fmt"
	"time"
)

const shutdownTimeout = 10 * time.Second

// serve runs the HTTP server until ctx is cancelled or the server fails, then shuts down gracefully.
func (a *application) serve(ctx context.Context) error {
	serverErr := make(chan error, 1)
	go func() {
		a.log.Info("http server listening", "addr", a.cfg.HTTPAddr)
		serverErr <- a.http.Listen(a.cfg.HTTPAddr)
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		a.log.Info("shutting down")
		if err := a.http.ShutdownWithTimeout(shutdownTimeout); err != nil {
			a.log.Error("http shutdown", "error", err)
		}
		return nil
	}
}
